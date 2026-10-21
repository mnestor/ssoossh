package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// The bootstrap identity echo's one-time codes. A code is minted on the
// server host (`ssoosshd identity echo`), carried to a browser in a link's
// fragment, and redeemed once for the authorization URL of a claims echo.
// See docs/proposals/identity-bootstrap-echo.md.
//
// A code is not a credential for anything else. Redeeming one starts an
// echo, which shows whoever signs in their own claims and establishes no
// session, no users row and no role; nothing reachable over HTTP mints
// one. Host access, which already includes the config and the database,
// is the authority it hands on.

const (
	// echoCodeBytes is 128 bits from crypto/rand, so the code is stored as a
	// plain SHA-256: there is no space small enough to search.
	echoCodeBytes = 16

	// DefaultEchoCodeTTL is how long a code lives unless the command says
	// otherwise: long enough to open a link and sign in, short enough that
	// a forgotten one is soon gone.
	DefaultEchoCodeTTL = 15 * time.Minute

	// maxEchoOutcomeLength caps the error recorded on a row. A provider's
	// error page can be long, and the operator needs its first line.
	maxEchoOutcomeLength = 1000
)

// Reasons a code ended unused, recorded on auth.echo_code_expired.
const (
	EchoCodeExpiredTimedOut  = "timed_out"
	EchoCodeExpiredReplaced  = "replaced"
	EchoCodeExpiredCancelled = "cancelled"
)

// IdentityEchoCodes is the half of the code store the HTTP layer drives:
// the redeem endpoint consumes a code, and the OIDC callback records how
// the echo it started ended. *IdentityEchoCodeService implements it.
type IdentityEchoCodes interface {
	// Redeem consumes code, recording sourceIP (the address the server
	// observed), and returns the code's id. A wrong, used or expired code
	// returns the same error, so the endpoint tells a guesser nothing.
	Redeem(ctx context.Context, code, sourceIP string) (string, error)
	// RecordOutcome stores how the echo started from code id ended:
	// outcome is empty for verified claims, otherwise the error that came
	// before any claims. Best effort: a failure is logged.
	RecordOutcome(ctx context.Context, id, outcome string)
}

// IdentityEchoCodeService mints, redeems and reports on the bootstrap echo's
// codes, against the identity_echo_codes table.
type IdentityEchoCodeService struct {
	db *gorm.DB
	// auditor records the minted, used and expired events. Nil in tests
	// that do not exercise auditing.
	auditor *AuditService
	// now is time.Now outside tests.
	now func() time.Time
}

// NewIdentityEchoCodeService builds the code store over db.
func NewIdentityEchoCodeService(db *gorm.DB) *IdentityEchoCodeService {
	return &IdentityEchoCodeService{db: db, now: time.Now}
}

// SetAuditor wires the audit recorder. Called once at startup.
func (s *IdentityEchoCodeService) SetAuditor(a *AuditService) { s.auditor = a }

// audit records one event when an auditor is wired.
func (s *IdentityEchoCodeService) audit(ctx context.Context, event AuditEvent) {
	if s.auditor != nil {
		s.auditor.Record(ctx, event)
	}
}

// MintedEchoCode is a freshly minted code: the plain code, shown once, and
// the row it was stored as.
type MintedEchoCode struct {
	Code string
	Row  model.IdentityEchoCode
}

// MintRequest is one code to mint.
type MintRequest struct {
	// TTL is how long the code lives; DefaultEchoCodeTTL when zero.
	TTL time.Duration
	// RequestedBy is stored on the row: the OS account and the host.
	RequestedBy string
	// Actor and Detail go on auth.echo_code_minted (see cliAuditDetail in
	// server/cmd for what the command passes).
	Actor  *AuditSubject
	Detail map[string]any
}

// Mint stores a new code, replacing every other: one live code at a time,
// so a link printed earlier and forgotten stops working the moment a new
// one is minted. The replaced codes that were still unused are audited as
// expired.
func (s *IdentityEchoCodeService) Mint(ctx context.Context, req MintRequest) (*MintedEchoCode, error) {
	ttl := req.TTL
	if ttl <= 0 {
		ttl = DefaultEchoCodeTTL
	}

	raw := make([]byte, echoCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return nil, fmt.Errorf("failed to generate an identity echo code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)

	now := s.now().UTC()
	row := model.IdentityEchoCode{
		ID:          uuid.NewString(),
		CodeHash:    hashEchoCode(code),
		RequestedBy: req.RequestedBy,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}

	var replaced []model.IdentityEchoCode
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("used_at IS NULL AND expires_at > ?", now).Find(&replaced).Error; err != nil {
			return err
		}
		if err := tx.Where("1 = 1").Delete(&model.IdentityEchoCode{}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return nil, fmt.Errorf("failed to store the identity echo code: %w", err)
	}

	for _, old := range replaced {
		s.auditExpired(ctx, old, EchoCodeExpiredReplaced, nil)
	}

	detail := map[string]any{}
	for k, v := range req.Detail {
		detail[k] = v
	}
	detail["code_id"] = row.ID
	detail["requested_by"] = row.RequestedBy
	detail["expires_at"] = row.ExpiresAt
	s.audit(ctx, AuditEvent{Action: AuditAuthEchoCodeMinted, Actor: req.Actor, Detail: detail})

	return &MintedEchoCode{Code: code, Row: row}, nil
}

// errEchoCodeRefused is the one answer to a code that is wrong, used or
// expired. The same words for all three: whoever holds a live code needs
// no hint, and whoever is guessing should get none.
func errEchoCodeRefused() error {
	return &errorresponses.NotFoundError{Resource: "a live identity echo link for that code"}
}

// Redeem implements IdentityEchoCodes.
//
// The consume is one conditional update, so when two instances (or two
// tabs) redeem the same code at once, exactly one update matches the
// unused row and the other finds nothing to change.
func (s *IdentityEchoCodeService) Redeem(ctx context.Context, code, sourceIP string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", errEchoCodeRefused()
	}
	hash := hashEchoCode(code)
	now := s.now().UTC()

	result := s.db.WithContext(ctx).Model(&model.IdentityEchoCode{}).
		Where("code_hash = ? AND used_at IS NULL AND expires_at > ?", hash, now).
		Updates(map[string]any{"used_at": now, "used_from": sourceIP})
	if result.Error != nil {
		return "", fmt.Errorf("failed to redeem the identity echo code: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return "", errEchoCodeRefused()
	}

	var row model.IdentityEchoCode
	if err := s.db.WithContext(ctx).First(&row, "code_hash = ?", hash).Error; err != nil {
		// not covered: the row was updated a statement ago, and only a
		// new mint deletes it; a mint landing between the two statements
		// is a race no test can schedule.
		return "", fmt.Errorf("failed to read the redeemed identity echo code: %w", err)
	}

	s.audit(ctx, AuditEvent{
		Action: AuditAuthEchoCodeUsed,
		Detail: map[string]any{
			"code_id":      row.ID,
			"requested_by": row.RequestedBy,
			"source_ip":    sourceIP,
		},
	})
	return row.ID, nil
}

// RecordOutcome implements IdentityEchoCodes. Only the first outcome
// counts: a code starts one echo.
func (s *IdentityEchoCodeService) RecordOutcome(ctx context.Context, id, outcome string) {
	outcome = clipUTF8(outcome, maxEchoOutcomeLength)
	err := s.db.WithContext(ctx).Model(&model.IdentityEchoCode{}).
		Where("id = ? AND completed_at IS NULL", id).
		Updates(map[string]any{"completed_at": s.now().UTC(), "outcome": outcome}).Error
	if err != nil {
		slog.WarnContext(ctx, "failed to record how an identity echo ended",
			slog.String("code_id", id), slog.Any("error", err))
	}
}

// ErrEchoCodeReplaced is what Get returns for a code a newer mint deleted.
var ErrEchoCodeReplaced = errors.New("a newer identity echo link replaced this one")

// Get reads code id's row, for the waiting command to report on.
func (s *IdentityEchoCodeService) Get(ctx context.Context, id string) (*model.IdentityEchoCode, error) {
	var row model.IdentityEchoCode
	err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, ErrEchoCodeReplaced
	case err != nil:
		return nil, fmt.Errorf("failed to read the identity echo code: %w", err)
	}
	return &row, nil
}

// End retires code id if it is still unused, by moving its expiry to now,
// and audits auth.echo_code_expired with reason. actor is the command line
// for a cancelled code, nil for one that timed out. Reports whether the
// code was still unused: a code redeemed meanwhile is left as it is.
func (s *IdentityEchoCodeService) End(ctx context.Context, id, reason string, actor *AuditSubject) (bool, error) {
	var row model.IdentityEchoCode
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read the identity echo code: %w", err)
	}
	now := s.now().UTC()
	expiresAt := row.ExpiresAt
	if expiresAt.After(now) {
		expiresAt = now
	}
	result := s.db.WithContext(ctx).Model(&model.IdentityEchoCode{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("expires_at", expiresAt)
	if result.Error != nil {
		return false, fmt.Errorf("failed to retire the identity echo code: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return false, nil
	}
	s.auditExpired(ctx, row, reason, actor)
	return true, nil
}

// auditExpired records one code ending unused. A code that timed out is
// the server's doing; a cancelled one the command line's.
func (s *IdentityEchoCodeService) auditExpired(ctx context.Context, row model.IdentityEchoCode, reason string, actor *AuditSubject) {
	s.audit(ctx, AuditEvent{
		Action: AuditAuthEchoCodeExpired,
		Actor:  actor,
		System: actor == nil,
		Detail: map[string]any{
			"code_id":      row.ID,
			"requested_by": row.RequestedBy,
			"reason":       reason,
		},
	})
}

// hashEchoCode is the stored form of a code.
func hashEchoCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
