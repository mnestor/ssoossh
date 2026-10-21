package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/test/testdb"
)

// newEchoCodeService builds the code store over a fresh database, auditing
// into it.
func newEchoCodeService(t *testing.T) (*IdentityEchoCodeService, *gorm.DB) {
	t.Helper()
	db := testdb.New(t)
	s := NewIdentityEchoCodeService(db)
	s.SetAuditor(NewAuditService(&config.Config{}, db))
	return s, db
}

// auditPayloads returns the payloads recorded for action.
func auditPayloads(t *testing.T, db *gorm.DB, action AuditAction) []string {
	t.Helper()
	var rows []model.AuditEvent
	if err := db.Where("action = ?", string(action)).Order("created_at").Find(&rows).Error; err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Payload)
	}
	return out
}

func TestIdentityEchoCodes_ShouldStoreOnlyTheCodesHash(t *testing.T) {
	t.Parallel()

	s, db := newEchoCodeService(t)
	minted, err := s.Mint(context.Background(), MintRequest{RequestedBy: "ssoossh@ssh-1"})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}

	var row model.IdentityEchoCode
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("read the row: %v", err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should store the SHA-256 of the code", ok: row.CodeHash == hashEchoCode(minted.Code)},
		{name: "should not store the code anywhere on the row", ok: !strings.Contains(row.CodeHash+row.RequestedBy+row.Outcome, minted.Code)},
		{name: "should not put the code in the audit event", ok: !strings.Contains(strings.Join(auditPayloads(t, db, AuditAuthEchoCodeMinted), ""), minted.Code)},
		{name: "should carry 128 bits", ok: len(minted.Code) == 22},
		{name: "should expire after the default ttl", ok: row.ExpiresAt.Sub(row.CreatedAt) == DefaultEchoCodeTTL},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s: row %+v", tt.name, row)
		}
	}
}

func TestIdentityEchoCodes_ShouldReplaceTheLiveCodeWhenASecondOneIsMinted(t *testing.T) {
	t.Parallel()

	s, db := newEchoCodeService(t)
	ctx := context.Background()
	first, err := s.Mint(ctx, MintRequest{})
	if err != nil {
		t.Fatalf("first Mint() error = %v", err)
	}
	if _, err := s.Mint(ctx, MintRequest{}); err != nil {
		t.Fatalf("second Mint() error = %v", err)
	}

	_, redeemErr := s.Redeem(ctx, first.Code, "203.0.113.9")
	_, getErr := s.Get(ctx, first.Row.ID)
	expired := auditPayloads(t, db, AuditAuthEchoCodeExpired)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should refuse the first code", ok: redeemErr != nil},
		{name: "should report the first as replaced", ok: errors.Is(getErr, ErrEchoCodeReplaced)},
		{name: "should audit the replaced code as expired", ok: len(expired) == 1 && strings.Contains(expired[0], EchoCodeExpiredReplaced)},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s (redeem %v, get %v, expired %v)", tt.name, redeemErr, getErr, expired)
		}
	}
}

func TestIdentityEchoCodes_ShouldConsumeACodeOnceWhenTwoInstancesRedeemItAtTheSameTime(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	first, second := NewIdentityEchoCodeService(db), NewIdentityEchoCodeService(db)
	minted, err := first.Mint(context.Background(), MintRequest{})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			instance := first
			if i%2 == 1 {
				instance = second
			}
			_, results[i] = instance.Redeem(context.Background(), minted.Code, "203.0.113.9")
		}(i)
	}
	wg.Wait()

	won := 0
	for _, err := range results {
		var refused *errorresponses.NotFoundError
		switch {
		case err == nil:
			won++
		case !errors.As(err, &refused):
			t.Errorf("a losing redeem failed with %v, want the refusal", err)
		}
	}
	if won != 1 {
		t.Errorf("%d redeems won, want exactly one", won)
	}
}

func TestIdentityEchoCodes_ShouldAuditTheUseWithTheObservedAddressAndNoClaims(t *testing.T) {
	t.Parallel()

	s, db := newEchoCodeService(t)
	minted, _ := s.Mint(context.Background(), MintRequest{RequestedBy: "ssoossh@ssh-1"})
	if _, err := s.Redeem(context.Background(), minted.Code, "203.0.113.9"); err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}

	used := auditPayloads(t, db, AuditAuthEchoCodeUsed)
	if len(used) != 1 || !strings.Contains(used[0], "203.0.113.9") || !strings.Contains(used[0], minted.Row.ID) || !strings.Contains(used[0], "ssoossh@ssh-1") {
		t.Errorf("used events = %v, want one naming the code, the requester and the address", used)
	}
}

func TestIdentityEchoCodes_ShouldKeepOnlyTheFirstOutcome(t *testing.T) {
	t.Parallel()

	s, _ := newEchoCodeService(t)
	ctx := context.Background()
	minted, _ := s.Mint(ctx, MintRequest{})
	s.RecordOutcome(ctx, minted.Row.ID, strings.Repeat("x", 2*maxEchoOutcomeLength))
	s.RecordOutcome(ctx, minted.Row.ID, "")

	row, err := s.Get(ctx, minted.Row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if row.CompletedAt == nil || len(row.Outcome) != maxEchoOutcomeLength {
		t.Errorf("got completed %v and an outcome of %d bytes, want the first, capped", row.CompletedAt, len(row.Outcome))
	}
}

func TestIdentityEchoCodes_ShouldStoreValidUTF8WhenTheCapSplitsACharacter(t *testing.T) {
	t.Parallel()

	s, _ := newEchoCodeService(t)
	ctx := context.Background()
	minted, _ := s.Mint(ctx, MintRequest{})
	// One ASCII byte ahead of two-byte characters puts the cap inside one.
	s.RecordOutcome(ctx, minted.Row.ID, "x"+strings.Repeat("\u00e9", maxEchoOutcomeLength))

	row, err := s.Get(ctx, minted.Row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !utf8.ValidString(row.Outcome) || len(row.Outcome) != maxEchoOutcomeLength-1 {
		t.Errorf("got an outcome of %d bytes, valid UTF-8 %t, want %d valid bytes", len(row.Outcome), utf8.ValidString(row.Outcome), maxEchoOutcomeLength-1)
	}
}

func TestIdentityEchoCodes_ShouldRetireOnlyAnUnusedCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		redeemFirst bool
		wantRetired bool
		wantEvents  int
	}{
		{name: "should retire and audit an unused code", wantRetired: true, wantEvents: 1},
		{name: "should leave a used code alone", redeemFirst: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, db := newEchoCodeService(t)
			ctx := context.Background()
			minted, _ := s.Mint(ctx, MintRequest{})
			if tt.redeemFirst {
				if _, err := s.Redeem(ctx, minted.Code, "203.0.113.9"); err != nil {
					t.Fatalf("Redeem() error = %v", err)
				}
			}

			retired, err := s.End(ctx, minted.Row.ID, EchoCodeExpiredCancelled, AuditSubjectForCLI())
			if err != nil {
				t.Fatalf("End() error = %v", err)
			}
			_, redeemErr := s.Redeem(ctx, minted.Code, "203.0.113.9")
			if retired != tt.wantRetired || len(auditPayloads(t, db, AuditAuthEchoCodeExpired)) != tt.wantEvents || redeemErr == nil {
				t.Errorf("retired = %t, events = %d, later redeem error = %v", retired, len(auditPayloads(t, db, AuditAuthEchoCodeExpired)), redeemErr)
			}
		})
	}
}

func TestIdentityEchoCodes_ShouldTreatAMissingCodeAsNotRetired(t *testing.T) {
	t.Parallel()

	s, _ := newEchoCodeService(t)
	retired, err := s.End(context.Background(), "no-such-id", EchoCodeExpiredTimedOut, nil)
	if retired || err != nil {
		t.Errorf("End() = %t, %v, want false and no error", retired, err)
	}
}

func TestIdentityEchoCodes_ShouldHonourTheRequestedTTL(t *testing.T) {
	t.Parallel()

	s, _ := newEchoCodeService(t)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	minted, err := s.Mint(context.Background(), MintRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	s.now = func() time.Time { return now.Add(2 * time.Minute) }

	if _, err := s.Redeem(context.Background(), minted.Code, "203.0.113.9"); err == nil {
		t.Error("Redeem() after the ttl succeeded, want the refusal")
	}
}
