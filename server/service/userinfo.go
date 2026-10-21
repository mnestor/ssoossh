package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// The OIDC UserInfo call (authentication.userinfo): what a login and a
// claims echo do between verifying the ID token and mapping its claims,
// and the shared health state that pauses the call when the endpoint keeps
// failing.
//
// Three layers, kept apart so the failure policy can change without
// touching the rest: UserInfoPolicy decides (pause after how many
// failures, probe how often); UserInfoHealthService keeps the counter and
// makes the state transitions in the database; the transitions it reports
// are what trigger the audit events and the admin mail.

// Claim sources: where a merged claim came from.
const (
	ClaimSourceIDToken  = "id_token"
	ClaimSourceUserInfo = "userinfo"
)

// UserInfoStatus says what happened to the UserInfo call on one login or
// echo.
type UserInfoStatus string

const (
	// UserInfoUsed: the call succeeded and filled what the ID token lacked
	// (possibly nothing).
	UserInfoUsed UserInfoStatus = "used"
	// UserInfoOff: authentication.userinfo is false.
	UserInfoOff UserInfoStatus = "off"
	// UserInfoNoEndpoint: the provider's discovery names no
	// userinfo_endpoint, so there is nothing to call.
	UserInfoNoEndpoint UserInfoStatus = "no_endpoint"
	// UserInfoPaused: the failure policy has paused the calls and this
	// login was not the window's probe.
	UserInfoPaused UserInfoStatus = "paused"
	// UserInfoFailed: the call failed; the login used the ID token alone.
	UserInfoFailed UserInfoStatus = "failed"
)

// userInfoErrorLength caps the error text kept on the health row and sent
// in the pause mail. A provider's error page can be long.
const userInfoErrorLength = 500

// UserInfoPolicy is the failure policy: after how many consecutive
// failures the calls pause, and how often, while paused, one login probes
// the endpoint. The only place these numbers live.
type UserInfoPolicy struct {
	PauseAfter int
	ProbeEvery time.Duration
}

// DefaultUserInfoPolicy pauses after 10 failures in a row and probes every
// 15 minutes.
func DefaultUserInfoPolicy() UserInfoPolicy {
	return UserInfoPolicy{PauseAfter: 10, ProbeEvery: 15 * time.Minute}
}

// ShouldPause reports whether failures consecutive failures pause the
// calls.
func (p UserInfoPolicy) ShouldPause(failures int) bool {
	return failures >= p.PauseAfter
}

// NextProbe is when the next probe may run, for a window starting at from.
func (p UserInfoPolicy) NextProbe(from time.Time) time.Time {
	return from.Add(p.ProbeEvery)
}

// UserInfoHealthService keeps the UserInfo endpoint's health in the
// database, shared by every instance: the failure counter, the pause, and
// the probe claim. Each transition is one conditional UPDATE, so exactly
// one instance makes it and that instance alone audits and mails.
type UserInfoHealthService struct {
	db     *gorm.DB
	policy UserInfoPolicy
	now    func() time.Time

	// endpoint and serverURL go into the mail.
	endpoint  string
	serverURL string

	// auditor, notifier and admins are wired after construction, as on the
	// other services, and nil in tests that do not exercise them. Mail is
	// off unless mailOn and both of notifier and admins are set.
	auditor  *AuditService
	notifier Notifier
	admins   AdminRecipientResolver
	mailOn   bool
}

// NewUserInfoHealthService builds the health store over db for the
// endpoint discovery named.
func NewUserInfoHealthService(db *gorm.DB, endpoint, serverURL string) *UserInfoHealthService {
	return &UserInfoHealthService{
		db: db, policy: DefaultUserInfoPolicy(), now: time.Now,
		endpoint: endpoint, serverURL: serverURL,
	}
}

// SetAuditor wires the audit recorder.
func (s *UserInfoHealthService) SetAuditor(a *AuditService) { s.auditor = a }

// SetNotifications wires the pause and resume mail: the publisher, who the
// admins are, and whether mail.enabled is on.
func (s *UserInfoHealthService) SetNotifications(n Notifier, admins AdminRecipientResolver, mailOn bool) {
	s.notifier, s.admins, s.mailOn = n, admins, mailOn
}

// ensureRow creates the one health row if no instance has yet.
func (s *UserInfoHealthService) ensureRow(ctx context.Context) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.UserInfoHealth{ID: model.UserInfoHealthID}).Error
}

// State reads the health row, or the healthy zero state when no call has
// been recorded yet.
func (s *UserInfoHealthService) State(ctx context.Context) (model.UserInfoHealth, error) {
	var row model.UserInfoHealth
	err := s.db.WithContext(ctx).First(&row, "id = ?", model.UserInfoHealthID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.UserInfoHealth{ID: model.UserInfoHealthID}, nil
	}
	if err != nil {
		return row, fmt.Errorf("failed to read the UserInfo endpoint's health: %w", err)
	}
	return row, nil
}

// Gate decides whether this login calls UserInfo: always while the calls
// are not paused; while paused, only the one login per window that claims
// the probe. probe reports that this call is that probe.
//
// A database error answers "call": the state could not be read, and the
// call failing on its own is the safer way to find out than skipping
// claims the provider would have sent.
func (s *UserInfoHealthService) Gate(ctx context.Context) (call, probe bool) {
	row, err := s.State(ctx)
	if err != nil {
		slog.WarnContext(ctx, "calling UserInfo without knowing whether it is paused", slog.Any("error", err))
		return true, false
	}
	if !row.Paused {
		return true, false
	}

	now := s.now().UTC()
	result := s.db.WithContext(ctx).Model(&model.UserInfoHealth{}).
		Where("id = ? AND paused = ? AND (next_probe_at IS NULL OR next_probe_at <= ?)", model.UserInfoHealthID, true, now).
		Update("next_probe_at", s.policy.NextProbe(now))
	if result.Error != nil {
		slog.WarnContext(ctx, "failed to claim the UserInfo probe, so this login skips the call", slog.Any("error", result.Error))
		return false, false
	}
	if result.RowsAffected == 1 {
		return true, true
	}
	return false, false
}

// RecordSuccess resets the failure counter and, when the calls were
// paused, resumes them: the instance whose update lifts the pause audits
// auth.userinfo_resumed and mails every admin, once.
func (s *UserInfoHealthService) RecordSuccess(ctx context.Context) {
	if err := s.ensureRow(ctx); err != nil {
		slog.WarnContext(ctx, "failed to record a UserInfo success", slog.Any("error", err))
		return
	}
	now := s.now().UTC()
	db := s.db.WithContext(ctx)

	var paused model.UserInfoHealth
	if err := db.First(&paused, "id = ?", model.UserInfoHealthID).Error; err != nil {
		slog.WarnContext(ctx, "failed to record a UserInfo success", slog.Any("error", err))
		return
	}

	if err := db.Model(&model.UserInfoHealth{}).Where("id = ?", model.UserInfoHealthID).
		Updates(map[string]any{"consecutive_failures": 0, "last_success_at": now}).Error; err != nil {
		slog.WarnContext(ctx, "failed to record a UserInfo success", slog.Any("error", err))
		return
	}
	if !paused.Paused {
		return
	}

	result := db.Model(&model.UserInfoHealth{}).
		Where("id = ? AND paused = ?", model.UserInfoHealthID, true).
		Updates(map[string]any{"paused": false, "paused_at": nil, "next_probe_at": nil})
	if result.Error != nil {
		slog.WarnContext(ctx, "failed to resume the UserInfo calls", slog.Any("error", result.Error))
		return
	}
	if result.RowsAffected != 1 {
		return
	}
	pausedAt := now
	if paused.PausedAt != nil {
		pausedAt = paused.PausedAt.UTC()
	}
	slog.InfoContext(ctx, "resumed the OIDC UserInfo calls: a probe succeeded", slog.Time("paused_at", pausedAt))
	s.audit(ctx, AuditAuthUserInfoResumed, map[string]any{"endpoint": s.endpoint, "paused_at": pausedAt})
	s.mailAdmins(ctx, notify.KindUserInfoResumed, &notify.UserInfoResumed{
		Endpoint: s.endpoint, PausedAt: pausedAt, ResumedAt: now, ServerURL: s.serverURL,
	})
}

// RecordFailure counts one failed call. A failed probe restarts the probe
// window from now. When the count reaches the policy's threshold, the
// instance whose update sets the pause audits auth.userinfo_paused and
// mails every admin, once; later failures while paused send nothing more.
func (s *UserInfoHealthService) RecordFailure(ctx context.Context, errText string, probe bool) {
	if err := s.ensureRow(ctx); err != nil {
		slog.WarnContext(ctx, "failed to record a UserInfo failure", slog.Any("error", err))
		return
	}
	now := s.now().UTC()
	db := s.db.WithContext(ctx)

	updates := map[string]any{
		"consecutive_failures": gorm.Expr("consecutive_failures + 1"),
		"last_error":           errText,
		"last_failure_at":      now,
	}
	if probe {
		updates["next_probe_at"] = s.policy.NextProbe(now)
	}
	if err := db.Model(&model.UserInfoHealth{}).Where("id = ?", model.UserInfoHealthID).Updates(updates).Error; err != nil {
		slog.WarnContext(ctx, "failed to record a UserInfo failure", slog.Any("error", err))
		return
	}

	var row model.UserInfoHealth
	if err := db.First(&row, "id = ?", model.UserInfoHealthID).Error; err != nil {
		slog.WarnContext(ctx, "failed to read the UserInfo failure count", slog.Any("error", err))
		return
	}
	if row.Paused || !s.policy.ShouldPause(row.ConsecutiveFailures) {
		return
	}

	nextProbe := s.policy.NextProbe(now)
	result := db.Model(&model.UserInfoHealth{}).
		Where("id = ? AND paused = ?", model.UserInfoHealthID, false).
		Updates(map[string]any{"paused": true, "paused_at": now, "next_probe_at": nextProbe})
	if result.Error != nil {
		slog.WarnContext(ctx, "failed to pause the UserInfo calls", slog.Any("error", result.Error))
		return
	}
	if result.RowsAffected != 1 {
		return
	}
	slog.WarnContext(ctx, "paused the OIDC UserInfo calls after consecutive failures; logins use the ID token's claims alone until a probe succeeds",
		slog.Int("consecutive_failures", row.ConsecutiveFailures), slog.Time("next_probe_at", nextProbe))
	s.audit(ctx, AuditAuthUserInfoPaused, map[string]any{
		"endpoint": s.endpoint, "consecutive_failures": row.ConsecutiveFailures,
		"last_error": errText, "next_probe_at": nextProbe,
	})
	s.mailAdmins(ctx, notify.KindUserInfoPaused, &notify.UserInfoPaused{
		Endpoint: s.endpoint, PausedAt: now, ConsecutiveFailures: row.ConsecutiveFailures,
		LastError: errText, NextProbeAt: nextProbe, ServerURL: s.serverURL,
	})
}

// audit records one transition as a system event.
func (s *UserInfoHealthService) audit(ctx context.Context, action AuditAction, detail map[string]any) {
	if s.auditor != nil {
		s.auditor.Record(ctx, AuditEvent{Action: action, System: true, Detail: detail})
	}
}

// mailAdmins sends one always-sent message to every admin, read from each
// admin's last login as the other admin mail is (AdminRecipients).
func (s *UserInfoHealthService) mailAdmins(ctx context.Context, kind notify.Kind, payload any) {
	if !s.mailOn || s.notifier == nil || s.admins == nil {
		slog.WarnContext(ctx, "mail is off, so the admins were not emailed about the UserInfo calls", slog.String("kind", string(kind)))
		return
	}
	admins, err := s.admins.AdminRecipients(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to find the admins to email about the UserInfo calls", slog.Any("error", err))
		return
	}
	if len(admins) == 0 {
		slog.WarnContext(ctx, "no admin with an email address has logged in to be told about the UserInfo calls", slog.String("kind", string(kind)))
		return
	}
	for i := range admins {
		s.notifier.Notify(ctx, kind, admins[i].ID, payload)
	}
}

// userInfoMerge is the result of the UserInfo step of one login or echo:
// the claims a mapping reads, where each came from, and what happened to
// the call.
type userInfoMerge struct {
	claims  map[string]any
	sources map[string]string
	status  UserInfoStatus
}

// errUserInfoSubjectMismatch fails a login whose UserInfo response names a
// different subject than its ID token. OpenID Connect Core 5.3.2: the
// UserInfo sub MUST exactly match the ID token's, or its values MUST NOT be
// used. Not an endpoint failure: the endpoint answered.
var errUserInfoSubjectMismatch = errors.New("the OIDC UserInfo response names a different subject (sub) than the ID token, so the login was refused")

// mergeUserInfo runs the UserInfo step: call the endpoint with the access
// token from the exchange (unless the config, discovery or the pause says
// not to) and fill in the claims the ID token lacks. The ID token wins on
// every key it carries.
//
// A failed call is not a failed login: it is logged and counted, and the
// ID token's claims go on alone. Only a subject mismatch is an error.
func (s *AuthService) mergeUserInfo(ctx context.Context, token *oauth2.Token, idClaims map[string]any) (*userInfoMerge, error) {
	merged := &userInfoMerge{claims: make(map[string]any, len(idClaims)), sources: make(map[string]string, len(idClaims))}
	for k, v := range idClaims {
		merged.claims[k] = v
		merged.sources[k] = ClaimSourceIDToken
	}

	switch {
	case !s.config.AuthConfig.UserInfo:
		merged.status = UserInfoOff
		return merged, nil
	case s.provider.UserInfoEndpoint() == "":
		merged.status = UserInfoNoEndpoint
		return merged, nil
	}

	probe := false
	if s.userInfoHealth != nil {
		var call bool
		call, probe = s.userInfoHealth.Gate(ctx)
		if !call {
			merged.status = UserInfoPaused
			return merged, nil
		}
	}

	if s.httpClient != nil {
		ctx = oidc.ClientContext(ctx, s.httpClient)
	}
	info, err := s.provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	var claims map[string]any
	if err == nil {
		err = info.Claims(&claims)
	}
	if err != nil {
		errText := scrubUserInfoError(err.Error(), token.AccessToken)
		slog.WarnContext(ctx, "the OIDC UserInfo call failed; this login uses the ID token's claims alone",
			slog.String("error", errText), slog.Bool("probe", probe))
		if s.userInfoHealth != nil {
			s.userInfoHealth.RecordFailure(ctx, errText, probe)
		}
		merged.status = UserInfoFailed
		return merged, nil
	}

	// The ID token's sub is required and verified, so it is a string.
	if idSub, ok := idClaims["sub"].(string); !ok || info.Subject != idSub {
		return nil, errUserInfoSubjectMismatch
	}
	if s.userInfoHealth != nil {
		s.userInfoHealth.RecordSuccess(ctx)
	}
	for k, v := range claims {
		if _, ok := merged.claims[k]; !ok {
			merged.claims[k] = v
			merged.sources[k] = ClaimSourceUserInfo
		}
	}
	merged.status = UserInfoUsed
	return merged, nil
}

// scrubUserInfoError makes a call's error safe to store and mail: the
// access token cut out wherever it appears, and the text capped.
func scrubUserInfoError(text, accessToken string) string {
	if accessToken != "" {
		text = strings.ReplaceAll(text, accessToken, "[access token]")
	}
	return clipUTF8(text, userInfoErrorLength)
}
