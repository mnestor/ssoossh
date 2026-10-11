package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// Detail level columns on users, one per place a level applies.
const (
	webDetailLevelColumn   = "web_detail_level"
	emailDetailLevelColumn = "email_detail_level"
)

// DefaultDetailLevel is the level a person starts with: advanced for SOC
// and admin, who compare the ids and policy detail the simplified pages
// drop, and simplified for everyone else.
//
// Read once, at the first login that finds a level unset, and stored. A
// later change of groups moves nothing: the level is the person's from
// then on, and a promotion that silently changed every page they read
// would be a surprise rather than a default.
func DefaultDetailLevel(admin *config.AdminConfig, groups []string) model.DetailLevel {
	if admin.GrantsSOC(groups) {
		return model.DetailLevelAdvanced
	}
	return model.DetailLevelSimplified
}

// fillDetailLevels stores level in whichever of the two detail level
// columns is still unset for subject's users row, and leaves a set one
// alone. Conditional on IS NULL in the statement itself, so two logins
// racing cannot overwrite a choice made between them.
func fillDetailLevels(ctx context.Context, db *gorm.DB, subject string, level model.DetailLevel) error {
	for _, column := range []string{webDetailLevelColumn, emailDetailLevelColumn} {
		err := db.WithContext(ctx).Model(&model.User{}).
			Where("subject = ? AND "+column+" IS NULL", subject).
			Update(column, level).Error
		if err != nil {
			return fmt.Errorf("failed to set the default %s: %w", column, err)
		}
	}
	return nil
}

// DetailLevels is a person's two detail levels, both resolved.
type DetailLevels struct {
	Web   model.DetailLevel
	Email model.DetailLevel
}

// DetailLevelProvider is the controllers' view of DetailLevelService: read
// both levels and change either, for the caller only. An interface so the
// HTTP contract can be tested without a database behind it.
type DetailLevelProvider interface {
	Levels(ctx context.Context, identity *Identity) (DetailLevels, error)
	SetWeb(ctx context.Context, identity *Identity, level model.DetailLevel) error
	SetEmail(ctx context.Context, identity *Identity, level model.DetailLevel) error
}

// DetailLevelService reads and writes the web and email detail levels
// stored on users.
type DetailLevelService struct {
	db    *gorm.DB
	admin *config.AdminConfig
}

// NewDetailLevelService constructs the service. admin supplies the role
// rule DefaultDetailLevel reads.
func NewDetailLevelService(db *gorm.DB, admin *config.AdminConfig) *DetailLevelService {
	return &DetailLevelService{db: db, admin: admin}
}

// Levels returns the identity's two levels. A level still unset, which is
// a session that began before the columns existed, is filled from this
// session's roles and stored, exactly as a fresh login would have done.
func (s *DetailLevelService) Levels(ctx context.Context, identity *Identity) (DetailLevels, error) {
	user, err := s.resolveUser(ctx, identity)
	if err != nil {
		return DetailLevels{}, err
	}

	if user.WebDetailLevel == nil || user.EmailDetailLevel == nil {
		if err := fillDetailLevels(ctx, s.db, identity.Subject, DefaultDetailLevel(s.admin, identity.Groups)); err != nil {
			return DetailLevels{}, err
		}
		// Re-read rather than assume: another request may have filled or
		// changed one in between, and what is stored is the answer.
		if user, err = s.resolveUser(ctx, identity); err != nil {
			// not covered: the same read succeeded moments ago.
			return DetailLevels{}, err
		}
	}

	return DetailLevels{
		Web:   model.OrSimplified(user.WebDetailLevel),
		Email: model.OrSimplified(user.EmailDetailLevel),
	}, nil
}

// SetWeb stores the identity's web UI detail level.
func (s *DetailLevelService) SetWeb(ctx context.Context, identity *Identity, level model.DetailLevel) error {
	return s.set(ctx, identity, webDetailLevelColumn, level)
}

// SetEmail stores the identity's email detail level.
func (s *DetailLevelService) SetEmail(ctx context.Context, identity *Identity, level model.DetailLevel) error {
	return s.set(ctx, identity, emailDetailLevelColumn, level)
}

// set validates level and writes it to column on the identity's users row.
// A level outside the two is the caller's input and renders as a 400; a
// session with no users row fails closed as a 403, the same rule every
// other user-scoped write follows.
func (s *DetailLevelService) set(ctx context.Context, identity *Identity, column string, level model.DetailLevel) error {
	if !level.Valid() {
		return &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("unknown detail level %q", level)}
	}
	if identity == nil {
		// not covered: every caller comes from middleware.Identity, which
		// reports absence separately.
		return &errorresponses.UnauthorizedError{}
	}

	result := s.db.WithContext(ctx).Model(&model.User{}).
		Where("subject = ?", identity.Subject).
		Update(column, level)
	if result.Error != nil {
		return fmt.Errorf("failed to save the %s: %w", column, result.Error)
	}
	if result.RowsAffected == 0 {
		return &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
	}
	return nil
}

// resolveUser loads the identity's users row by OIDC subject. A miss means
// the session outlived its user record and fails closed.
func (s *DetailLevelService) resolveUser(ctx context.Context, identity *Identity) (model.User, error) {
	if identity == nil {
		// not covered: every caller comes from middleware.Identity, which
		// reports absence separately.
		return model.User{}, &errorresponses.UnauthorizedError{}
	}

	var user model.User
	err := s.db.WithContext(ctx).
		Select("id", "subject", webDetailLevelColumn, emailDetailLevelColumn).
		First(&user, "subject = ?", identity.Subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
		}
		return model.User{}, fmt.Errorf("failed to read the detail levels: %w", err)
	}
	return user, nil
}

// fillDetailLevelsAtLogin stores the default levels for a person logging
// in, from the roles this login carries. Logged rather than returned: a
// display default is not worth failing a login over, and the next read
// through DetailLevelService.Levels fills anything this missed.
func (s *AuthService) fillDetailLevelsAtLogin(ctx context.Context, identity *Identity) {
	level := DefaultDetailLevel(&s.config.Admin, identity.Groups)
	if err := fillDetailLevels(ctx, s.db, identity.Subject, level); err != nil {
		slog.WarnContext(ctx, "failed to store the default detail levels at login",
			slog.String("subject", identity.Subject), slog.Any("error", err))
	}
}
