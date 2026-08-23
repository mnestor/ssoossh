package service

// Test methodology: unit tests for CertRequestService against a real
// in-memory sqlite *gorm.DB (AutoMigrate'd from model.CertificateRequest —
// this exercises the service's query/expiry logic, not full migration
// correctness, which server/bootstrap/db_test.go covers). Tests run in
// parallel where they don't share a DB/service instance.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/dbtime"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// newTestCertRequestService opens an in-memory sqlite DB migrated for
// model.CertificateRequest and returns a CertRequestService backed by it,
// with RequestTTL as given (0 disables expiry). The wake-topic broker is a
// real, non-persistent gochannel pair — matching server/pubsub.New exactly
// (see its Persistent:false doc comment for why) — closed on test cleanup.
func newTestCertRequestService(t *testing.T, ttl time.Duration) *CertRequestService {
	t.Helper()
	return newTestCertRequestServiceWithOptions(t, config.CertificateOptions{RequestTTL: ttl})
}

// pgAdminOnce guards creation of the single admin handle used to create
// and drop per-test schemas. The per-test pools cannot do it themselves:
// their own search_path points at a schema that does not exist yet.
var (
	pgAdminOnce sync.Once
	pgAdminDB   *gorm.DB
	pgAdminErr  error
	pgSchemaSeq atomic.Uint64
)

// pgAdmin returns the shared admin connection to the live Postgres named by
// dsn, opening it once per process.
func pgAdmin(dsn string) (*gorm.DB, error) {
	pgAdminOnce.Do(func() {
		pgAdminDB, pgAdminErr = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if pgAdminErr != nil {
			return
		}
		sqlDB, err := pgAdminDB.DB()
		if err != nil {
			pgAdminErr = err
			return
		}
		// Schema create/drop only; a couple of connections is plenty and
		// leaves the server's slots for the per-test pools.
		sqlDB.SetMaxOpenConns(4)
	})
	return pgAdminDB, pgAdminErr
}

// pgSchemaName derives a unique, valid Postgres identifier for t. Postgres
// truncates identifiers at 63 bytes, so the test name is trimmed and a
// process-unique counter appended to keep it collision-free even when two
// subtests sanitize to the same string.
func pgSchemaName(t *testing.T) string {
	t.Helper()
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, t.Name())

	const maxLen = 40
	if len(sanitized) > maxLen {
		sanitized = sanitized[:maxLen]
	}
	return fmt.Sprintf("t_%s_%d", sanitized, pgSchemaSeq.Add(1))
}

// dsnWithSearchPath returns dsn with search_path pinned to schema. It has to
// travel on the DSN rather than a `SET search_path` statement: the pool opens
// several physical connections and a SET only affects the one it ran on, so
// a later query on a sibling connection would silently resolve against the
// wrong schema.
func dsnWithSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// newTestPostgresDB gives t its own schema on the live Postgres named by
// SSOOSSH_TEST_POSTGRES_DSN, so the package's t.Parallel() tests run
// concurrently against one server without seeing each other's rows. The
// schema is dropped on cleanup.
//
// The instance must be disposable: these tests create and drop schemas
// freely.
func newTestPostgresDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()

	admin, err := pgAdmin(dsn)
	if err != nil {
		t.Fatalf("failed to open the admin connection to live postgres: %v", err)
	}

	schema := pgSchemaName(t)
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("failed to create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error; err != nil {
			t.Errorf("failed to drop schema %s: %v", schema, err)
		}
	})

	scopedDSN, err := dsnWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("failed to scope the dsn to schema %s: %v", schema, err)
	}

	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{NowFunc: dbtime.NowFunc})
	if err != nil {
		t.Fatalf("failed to open live postgres: %v", err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		t.Fatalf("failed to register the UTC timestamp plugin: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	// Small per-test pools: with tests running in parallel these multiply,
	// and Postgres defaults to 100 connection slots.
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(1)

	// Registered after the DROP cleanup so it runs first (cleanups are
	// last-in-first-out): the schema cannot be dropped while this pool
	// still holds connections inside it.
	t.Cleanup(func() { _ = sqlDB.Close() })

	return db
}

// newTestDB opens a fresh in-memory sqlite *gorm.DB. Constrained to a
// single open connection, matching server/bootstrap/db.go's onConnFn for
// in-memory SQLite: a pool that opens more than one physical connection to
// ":memory:" hands out a genuinely separate, unmigrated database on the
// second connection (each connection is its own in-memory instance), which
// surfaces as sporadic "no such table" failures under concurrent access —
// exactly what this avoids.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	// With SSOOSSH_TEST_POSTGRES_DSN set, the whole package runs against a
	// live Postgres instead — same tests, real dialect semantics (row
	// locking instead of SQLite's serialized writes, native TIMESTAMPTZ,
	// enforced FKs under concurrency). Used with a disposable container.
	if dsn := os.Getenv("SSOOSSH_TEST_POSTGRES_DSN"); dsn != "" {
		return newTestPostgresDB(t, dsn)
	}

	// Mirrors bootstrap.openWithRetry: the UTC timestamp normalization is
	// part of how this project talks to SQLite, not an optional extra, so
	// tests exercise the same configuration production runs. See package
	// dbtime for why comparing local-offset timestamps against SQLite's
	// string-compared DATETIME columns is wrong.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{NowFunc: dbtime.NowFunc})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		t.Fatalf("failed to register the UTC timestamp plugin: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

// newTestCertRequestServiceWithOptions is newTestCertRequestService but
// lets Approve tests control the full per-type policy (Extensions,
// ValidDuration, RequireGroup), not just RequestTTL.
func newTestCertRequestServiceWithOptions(t *testing.T, opts config.CertificateOptions) *CertRequestService {
	t.Helper()
	return newTestCertRequestServiceWithConfig(t, &config.Config{CertOptions: opts})
}

// newTestCertRequestServiceWithConfig is newTestCertRequestServiceWithOptions
// but takes the full *config.Config, for tests (like FIPS) that need to
// control fields beyond CertOptions.
func newTestCertRequestServiceWithConfig(t *testing.T, cfg *config.Config) *CertRequestService {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.CertificateRequestDecision{}, &model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(cfg, db, channel, channel)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}
	return svc
}

// closeUnderlyingDB closes db's connection, so any subsequent query fails
// with a real "sql: database is closed" error — the most direct way to
// exercise this package's generic-DB-error branches without a mock: every
// call still goes through the real *gorm.DB and the real sqlite driver,
// just against a connection that's genuinely gone.
func closeUnderlyingDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("failed to close the database: %v", err)
	}
}

// seedUser inserts the users row Approve resolves the approving identity
// against, returning its ID. Approve binds a request to a user (see
// bindRequester), so every approval path needs one to exist.
func seedUser(t *testing.T, db *gorm.DB, subject string) string {
	t.Helper()

	user := model.User{
		ID:        uuid.NewString(),
		Subject:   subject,
		Username:  "seeded-" + subject,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user %q: %v", subject, err)
	}
	return user.ID
}

// TestCertRequestService_ShouldSurfaceGenericDBErrors covers the
// generic-database-error branch in CreateRequest, Detail, Approve,
// bindRequester (both its guarded UPDATE and its racing-claim re-read),
// resolveUserID, approveServiceEnrollment, approveForSigning, and Deny —
// each distinct from that same method's not-found/not-pending handling,
// which is tested elsewhere. Closing the underlying connection (see
// closeUnderlyingDB) makes every one of these fail the same real way, so
// one table covers all of them without a mock for each.
func TestCertRequestService_ShouldSurfaceGenericDBErrors(t *testing.T) {
	t.Parallel()

	t.Run("CreateRequest", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, 0)
		closeUnderlyingDB(t, svc.db)

		if _, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."}); err == nil {
			t.Error("CreateRequest() error = nil, want error")
		}
	})

	t.Run("Detail", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		seedUser(t, svc.db, identity.Subject)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		if _, err := svc.Detail(context.Background(), requestID, identity); err == nil {
			t.Error("Detail() error = nil, want error")
		}
	})

	t.Run("Approve", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		seedUser(t, svc.db, identity.Subject)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
			t.Error("Approve() error = nil, want error")
		}
	})

	t.Run("bindRequester's guarded UPDATE", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		seedUser(t, svc.db, identity.Subject)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		req := &model.CertificateRequest{ID: requestID, UserID: nil}
		if err := svc.bindRequester(context.Background(), req, identity); err == nil {
			t.Error("bindRequester() error = nil, want error")
		}
	})

	t.Run("resolveUserID", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, 0)
		closeUnderlyingDB(t, svc.db)

		if _, err := svc.resolveUserID(context.Background(), &Identity{Subject: "sub-alice"}); err == nil {
			t.Error("resolveUserID() error = nil, want error")
		}
	})

	t.Run("approveServiceEnrollment", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeService, PublicKey: "ssh-ed25519 AAAA... svc"})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}
		var req model.CertificateRequest
		if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
			t.Fatalf("failed to load request: %v", err)
		}
		closeUnderlyingDB(t, svc.db)
		policy, _ := svc.policyFor(model.CertificateTypeService)
		if err := svc.approveServiceEnrollment(context.Background(), req, RequestedOptions{}, &Identity{Username: "approver", Subject: "sub-approver"}, policy, DecisionContext{}); err == nil {
			t.Error("approveServiceEnrollment() error = nil, want error")
		}
	})

	t.Run("approveForSigning", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		requestID := mustCreateUserRequest(t, svc)
		var req model.CertificateRequest
		if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
			t.Fatalf("failed to reload request: %v", err)
		}
		closeUnderlyingDB(t, svc.db)

		if err := svc.approveForSigning(context.Background(), req, identity, svc.policies[model.CertificateTypeUser], RequestedOptions{}, DecisionContext{}); err == nil {
			t.Error("approveForSigning() error = nil, want error")
		}
	})

	t.Run("Deny", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, 0)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err == nil {
			t.Error("Deny() error = nil, want error")
		}
	})
}

// TestNewCertRequestService_ShouldRejectAMalformedKeyIDTemplate covers the
// newKeyIDTemplates error propagating out of the constructor: a bad
// template (unparseable syntax, or referencing a field
// keyIDTemplateData doesn't have) must fail startup rather than the first
// approval.
func TestNewCertRequestService_ShouldRejectAMalformedKeyIDTemplate(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	opts := config.CertificateOptions{}
	opts.User.KeyIDTemplate = "{{.NoSuchField}}"

	if _, err := NewCertRequestService(&config.Config{CertOptions: opts}, db, channel, channel); err == nil {
		t.Error("NewCertRequestService() error = nil, want error for a key ID template referencing an unknown field")
	}
}

func TestCertRequestService_Wait_ShouldReturnErrorForUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)

	_, _, _, err := svc.Wait(context.Background(), "does-not-exist")

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected a *errorresponses.NotFoundError, got %T: %v", err, err)
	}
}

func TestCertRequestService_Wait_ShouldUnblockOnDeny(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()

	// Give Wait a moment to actually block before denying, so this
	// exercises the "already blocked" path rather than racing CreateRequest.
	time.Sleep(10 * time.Millisecond)

	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusDenied {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusDenied)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock after Deny")
	}
}

func TestCertRequestService_Wait_ShouldReturnCachedOutcomeOnLateReconnect(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Resolve it with nobody blocked in Wait yet — simulates the web UI
	// approving/denying before the client's SSE connection ever attaches.
	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	status, _, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusDenied {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusDenied)
	}
}

func TestCertRequestService_Wait_ShouldResumeAfterContextCancellation(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Simulate a dropped SSE connection: Wait's context is canceled while
	// the request is still pending.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := svc.Wait(cancelCtx, requestID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// A fresh Wait call (the reconnect) for the same requestID must still
	// work, not error with "no such waiter".
	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()

	time.Sleep(10 * time.Millisecond)
	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from reconnected Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusDenied {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusDenied)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnected Wait did not unblock after Deny")
	}
}

// TestCertRequestService_Wait_ShouldReportApprovedCertificateAsUnavailable
// is the regression test for the ephemeral-certificate design: certificates
// are never persisted, so a client reaching a terminal "approved" row with
// nothing cached (i.e. after a restart) has genuinely missed its delivery.
// It must be told so, not handed a successful outcome with an empty
// certificate it might not check.
func TestCertRequestService_Wait_ShouldReportApprovedCertificateAsUnavailable(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Resolve the row directly, leaving the in-memory cache cold — exactly
	// the state a restarted server comes back to.
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusApproved).Error; err != nil {
		t.Fatalf("failed to mark request approved: %v", err)
	}

	_, certificate, _, err := svc.Wait(context.Background(), requestID)

	var unavailable *errorresponses.CertificateUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("expected a *errorresponses.CertificateUnavailableError, got %T: %v", err, err)
	}
	if certificate != "" {
		t.Errorf("expected no certificate alongside the error, got %q", certificate)
	}
}

// TestCertRequestService_Wait_ShouldRebuildEnrollmentTokenFromTheRow is the
// counterpart: unlike a certificate, an enrollment token *is* durable, so a
// cold cache must be answered from the database rather than returning an
// empty code.
func TestCertRequestService_Wait_ShouldRebuildEnrollmentTokenFromTheRow(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Updates(map[string]any{
			"status":           model.CertificateRequestStatusEnrolled,
			"enrollment_token": "token-abc",
		}).Error; err != nil {
		t.Fatalf("failed to mark request enrolled: %v", err)
	}

	status, _, code, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusEnrolled {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusEnrolled)
	}
	if code != "token-abc" {
		t.Errorf("got code %q, want %q", code, "token-abc")
	}
}

func TestCertRequestService_Wait_ShouldExpireRequestPastTTL(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Millisecond)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	status, _, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusExpired {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusExpired)
	}
}

// TestCertRequestService_Wait_ShouldWakeOnTTLTimerFiring verifies that the
// TTL timer added to Wait's select fires and causes the loop to unblock,
// marking the request expired. This is the mechanism that prevents clients
// from being blocked indefinitely waiting for approval on a long-stale request.
// The test waits the full TTL duration and verifies Wait returns, without any
// external wake message (no Approve/Deny/pub/sub).
func TestCertRequestService_Wait_ShouldWakeOnTTLTimerFiring(t *testing.T) {
	t.Parallel()

	// The TTL has to be short enough to keep the test quick but long enough
	// that scheduler jitter cannot eat it. At 50ms this test was flaky: under
	// parallel load more than 50ms could elapse between CreateRequest and the
	// Wait call below, so the request was already past its TTL on the first
	// reconcileStatus pass, Wait returned without ever blocking on the timer,
	// and the elapsed assertion failed for load rather than for behaviour.
	const ttl = 500 * time.Millisecond
	svc := newTestCertRequestService(t, ttl)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// The TTL deadline is measured from the request's CreatedAt AS STORED,
	// not from this test's own clock. The database truncates sub-millisecond
	// precision, so the timer legitimately fires up to ~1ms before
	// wall-clock-creation + ttl. Asserting elapsed against time.Now() taken
	// here failed on a fast machine for exactly that reason (observed:
	// 499.4ms elapsed against a 500ms TTL). Read the stored CreatedAt back
	// and assert against the deadline the system actually computes.
	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to read back the created request: %v", err)
	}
	deadline := stored.CreatedAt.Add(ttl)

	// Bounded only so a broken timer cannot hang the suite forever. The
	// ceiling is deliberately generous rather than tight to the TTL: under
	// CPU contention a 200ms deadline beat the 50ms timer's own work and
	// Wait returned ctx.Err(), failing the test for load rather than for
	// behaviour. The real assertions are the returned status and the lower
	// bound on elapsed; Go's own test timeout catches a genuine hang.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, _, _, err := svc.Wait(ctx, requestID)
	returnedAt := time.Now()

	// The key assertion: the timer fired and Wait woke up, returning StatusExpired,
	// roughly when the TTL should expire (at least 50ms, since that's the TTL).
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusExpired {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusExpired)
	}
	if returnedAt.Before(deadline) {
		t.Errorf("Wait returned at %v, before the TTL deadline %v (stored CreatedAt %v + %v)",
			returnedAt, deadline, stored.CreatedAt, ttl)
	}
}

// TestCertRequestService_Wait_ShouldReceiveWakeMessageViaPubSub exercises
// the actual pub/sub wake path (not just the resolved-cache fast path):
// Wait is left blocked in its select on the subscription for a beat before
// Deny publishes, so the unblock has to come from the subscribed message
// arriving, not from resolved already being populated when Wait's loop
// started.
func TestCertRequestService_Wait_ShouldReceiveWakeMessageViaPubSub(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()

	// Long enough for Wait to have subscribed and reached its blocking
	// select before Deny (and therefore notifyWaiter's publish) runs.
	time.Sleep(50 * time.Millisecond)

	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusDenied {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusDenied)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock via the pub/sub wake message")
	}
}

// TestCertRequestService_Wait_ShouldReturnCtxErrOnGenuineCancellation covers
// the blocking select's ctx.Done() arm specifically — distinct from
// TestCertRequestService_Wait_ShouldResumeAfterContextCancellation, which
// cancels ctx *before* calling Wait and so never reaches the select at all
// (lookupRequest's own DB query fails first, on the already-canceled
// context). This cancels concurrently, after Wait is genuinely blocked.
func TestCertRequestService_Wait_ShouldReturnCtxErrOnGenuineCancellation(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, _, _, err = svc.Wait(ctx, requestID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// failingSubscriber is a message.Subscriber whose Subscribe call always
// fails, standing in for a broker that's unreachable.
type failingSubscriber struct{}

func (failingSubscriber) Subscribe(_ context.Context, _ string) (<-chan *message.Message, error) {
	return nil, errors.New("subscription unavailable")
}
func (failingSubscriber) Close() error { return nil }

func TestCertRequestService_Wait_ShouldSurfaceASubscribeFailure(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.CertificateRequestDecision{}, &model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{}, db, channel, failingSubscriber{})
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if _, _, _, err := svc.Wait(context.Background(), requestID); err == nil {
		t.Error("Wait() error = nil, want error when the subscriber is unreachable")
	}
}

func TestCertRequestService_Approve_ShouldNarrowExtensionsAndPublishSigningJob(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{
			Extensions:    []string{"permit-pty"},
			ValidDuration: time.Hour,
		},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-agent-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	messages, err := svc.subscriber.Subscribe(ctx, certmsg.SignQueueTopic)
	if err != nil {
		t.Fatalf("unexpected error subscribing to the sign queue: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", Email: "alice@example.com"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusSigning {
		t.Errorf("got status %q, want %q", req.Status, model.CertificateRequestStatusSigning)
	}

	select {
	case msg := <-messages:
		var job certmsg.SigningJob
		if err := json.Unmarshal(msg.Payload, &job); err != nil {
			t.Fatalf("failed to decode signing job: %v", err)
		}
		msg.Ack()

		if job.RequestID != requestID {
			t.Errorf("got RequestID %q, want %q", job.RequestID, requestID)
		}
		if len(job.RequestedOptions.Extensions) != 1 || job.RequestedOptions.Extensions[0] != "permit-pty" {
			t.Errorf("expected extensions to be narrowed to [\"permit-pty\"], got %v", job.RequestedOptions.Extensions)
		}
		if len(job.Principals) != 1 || job.Principals[0] != "alice" {
			t.Errorf("expected principals to be [\"alice\"], got %v", job.Principals)
		}
		if job.KeyID != "alice" {
			t.Errorf("got KeyID %q, want %q (default template is username)", job.KeyID, "alice")
		}
		if !job.ValidBefore.After(job.ValidAfter) {
			t.Errorf("expected ValidBefore to be after ValidAfter, got %v / %v", job.ValidBefore, job.ValidAfter)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the signing job to be published")
	}
}

func TestCertRequestService_Approve_ShouldRejectWhenIdentityLacksRequiredGroup(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{RequireGroup: "admins", ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", Groups: []string{"engineers"}}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected an error approving without the required group, got nil")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusPending {
		t.Errorf("expected the request to remain pending after a rejected approval, got %q", req.Status)
	}
}

// TestCertRequestService_Approve_ShouldDenyPAMWhenRequireGroupUnconfigured
// pins the one deliberate divergence from every other certificate type: an
// unset cert_options.pam.require_group denies rather than opens, because
// "who may sudo" has to fail closed (see CertOptionsPAM.RequireGroup). The
// identity here has no groups at all, so this also proves the rejection
// isn't just an ordinary group-membership failure in disguise.
func TestCertRequestService_Approve_ShouldDenyPAMWhenRequireGroupUnconfigured(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		PAM: config.CertOptionsPAM{ValidDuration: 30 * time.Second},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAA...",
		Username:  "mnestor",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "mike.nestor", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected an error approving a PAM request with no configured require_group, got nil")
	}
}

// TestCertRequestService_Approve_ShouldQueuePAMRequestWithLocalUsernameAsPrincipal
// is the assertion the phase 4 plan calls out by name: the issued
// certificate must name the local account the module authenticated
// (req.Username), not the approver's OIDC username, with the two set to
// different values. Also checks the PAM-only defaults: extensions dropped
// (nothing configured-permitted) and the PAM key ID template rather than
// the user one.
func TestCertRequestService_Approve_ShouldQueuePAMRequestWithLocalUsernameAsPrincipal(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		PAM: config.CertOptionsPAM{RequireGroup: "sudoers", ValidDuration: 30 * time.Second},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAA...",
		Username:  "mnestor",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	messages, err := svc.subscriber.Subscribe(ctx, certmsg.SignQueueTopic)
	if err != nil {
		t.Fatalf("unexpected error subscribing to the sign queue: %v", err)
	}

	identity := &Identity{Username: "mike.nestor", Subject: "sub-1", Groups: []string{"sudoers"}}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	select {
	case msg := <-messages:
		var job certmsg.SigningJob
		if err := json.Unmarshal(msg.Payload, &job); err != nil {
			t.Fatalf("failed to decode signing job: %v", err)
		}
		msg.Ack()

		if len(job.Principals) != 1 || job.Principals[0] != "mnestor" {
			t.Errorf("expected principals to be [\"mnestor\"] (the local username), got %v", job.Principals)
		}
		// KeyID is the audit-log label and stays keyed on the approver's
		// identity, same as every other type — it's the *principal* that
		// must diverge to the local username (checked above). This is what
		// makes "pam:mike.nestor" distinguishable from a login by the same
		// person in an sshd/sudo audit log (see CertOptionsPAM.KeyIDTemplate).
		if job.KeyID != "pam:mike.nestor" {
			t.Errorf("got KeyID %q, want %q (PAM's own default template, keyed on the approver)", job.KeyID, "pam:mike.nestor")
		}
		if len(job.RequestedOptions.Extensions) != 0 {
			t.Errorf("expected no extensions (none configured-permitted), got %v", job.RequestedOptions.Extensions)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the signing job to be published")
	}
}

func TestCertRequestService_Approve_ShouldEnrollServiceRequestsInsteadOfQueueing(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{
			Extensions:    []string{"permit-pty"},
			ValidDuration: time.Hour,
		},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-agent-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Wait is already blocked before Approve runs, to prove the wake
	// comes directly from Approve itself (no signer/queue round trip) —
	// same shape as TestCertRequestService_Wait_ShouldReceiveWakeMessageViaPubSub.
	type waitResult struct {
		status model.CertificateRequestStatus
		code   string
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, code, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, code, err}
	}()
	time.Sleep(50 * time.Millisecond)

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	var res waitResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock after Approve enrolled the request")
	}
	if res.err != nil {
		t.Fatalf("unexpected error from Wait: %v", res.err)
	}
	if res.status != model.CertificateRequestStatusEnrolled {
		t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusEnrolled)
	}
	if res.code == "" {
		t.Error("expected a non-empty enrollment code")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusEnrolled {
		t.Errorf("got DB status %q, want %q", req.Status, model.CertificateRequestStatusEnrolled)
	}
	if req.EnrollmentToken == "" {
		t.Error("expected EnrollmentToken to be set on the row")
	}
	if req.EnrollmentToken != res.code {
		t.Errorf("expected Wait's code to match the persisted EnrollmentToken, got %q vs %q", res.code, req.EnrollmentToken)
	}

	var narrowed RequestedOptions
	if err := json.Unmarshal([]byte(req.RequestedOptions), &narrowed); err != nil {
		t.Fatalf("failed to decode persisted requested options: %v", err)
	}
	if len(narrowed.Extensions) != 1 || narrowed.Extensions[0] != "permit-pty" {
		t.Errorf("expected the persisted options to be narrowed to [\"permit-pty\"], got %v", narrowed.Extensions)
	}
}

func TestCertRequestService_Approve_ShouldReturnNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)

	err := svc.Approve(context.Background(), "does-not-exist", &Identity{Username: "alice", Subject: "sub-alice"}, DecisionContext{})

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected a *errorresponses.NotFoundError, got %T: %v", err, err)
	}
}

// TestCertRequestService_Approve_ShouldRejectHostCertificates covers
// Approve's default switch case: host certificates aren't issuable yet (the
// signer only handles user and PAM), so approving one must fail immediately
// rather than queuing a job the signer would refuse.
func TestCertRequestService_Approve_ShouldRejectHostCertificates(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeHost,
		PublicKey: "ssh-ed25519 AAAA... host",
		Hostname:  "web-01.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected approving a host certificate request to fail: host issuance isn't supported yet")
	}
}

// TestCertRequestService_Approve_ShouldSurfaceACorruptOptionsColumn is
// Approve's counterpart to the same Detail test: a request row whose
// requested_options is somehow unparseable must fail approval rather than
// silently treat the requester as having asked for nothing.
func TestCertRequestService_Approve_ShouldSurfaceACorruptOptionsColumn(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("requested_options", "not json").Error; err != nil {
		t.Fatalf("failed to corrupt requested_options: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Error("Approve() error = nil, want error for an unparseable requested_options column")
	}
}

func TestCertRequestService_Approve_ShouldErrorWhenNotPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error on first approve: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected an error approving an already-signing request, got nil")
	}
}

// TestCertRequestService_PolicyFor_ShouldRejectAnUnsupportedCertificateType
// covers policyFor's defense-in-depth guard: every route into CreateRequest
// hardcodes a known model.CertificateType (see
// server/controller/certrequests.go), so this only fires for a corrupted or
// hand-edited database row.
func TestCertRequestService_PolicyFor_ShouldRejectAnUnsupportedCertificateType(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)

	if _, err := svc.policyFor(model.CertificateType("bogus")); err == nil {
		t.Error("policyFor() error = nil, want error for an unsupported certificate type")
	}
}

func TestCertRequestService_Deny_ShouldErrorWhenNotPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}
	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error on first deny: %v", err)
	}

	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err == nil {
		t.Fatal("expected an error denying an already-denied request, got nil")
	}
}

// TestCertRequestService_Approve_ShouldRefuseARequestPastTTL keeps the
// TTL-expiry assertion that used to run through ListPending, repointed at
// Approve now that requests are never listed. This is the half that matters:
// an expired request must not be approvable, whatever route reaches it.
func TestCertRequestService_Approve_ShouldRefuseARequestPastTTL(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Millisecond)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	// Seed the user upfront so that bindRequester succeeds — this ensures the
	// TTL check is the actual rejection reason, not user lookup failure.
	// Without this, TTL check removal is masked by bindRequester's "user not
	// found" error, and the test passes even when TTL enforcement is broken.
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	err = svc.Approve(context.Background(), requestID, identity, DecisionContext{})
	if err == nil {
		t.Error("expected a TTL-expired request to be refused by Approve")
	}
	// Strengthen assertion: verify the rejection is TTL-related, not due to
	// missing user or other pre-TTL-check failures
	if !strings.Contains(err.Error(), "not pending") {
		t.Errorf("expected TTL expiry to reject with 'not pending' message, got: %v", err)
	}
}

// TestCertRequestService_RaceCondition_ApproveThenExpire covers the race where
// Approve wins and transitions the request to Signing before any TTL-expiry
// path runs. Verification: the status stays Signing, and expire() is a no-op
// (only updates rows with status=Pending).
func TestCertRequestService_RaceCondition_ApproveThenExpire(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 50*time.Millisecond)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Approve immediately before TTL expires
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	// Verify status is Signing
	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusSigning {
		t.Fatalf("expected status Signing after approve, got %q", req.Status)
	}

	// Sleep past the TTL
	time.Sleep(100 * time.Millisecond)

	// Verify status is still Signing (expire is a no-op on non-Pending rows)
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusSigning {
		t.Errorf("expected status Signing after TTL passes, got %q (expire should be a no-op)", req.Status)
	}
}

// TestCertRequestService_RaceCondition_ExpireThenApprove covers the race where
// expire() wins and transitions the request to Expired before Approve runs.
// Verification: approval fails cleanly and no certificate is issued. This is
// the critical direction where a bug would mint credentials to a long-stale
// request.
func TestCertRequestService_RaceCondition_ExpireThenApprove(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 50*time.Millisecond)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Sleep past the TTL so the request expires
	time.Sleep(100 * time.Millisecond)

	// Manually expire the request (simulates the auto-expiry timer firing in Wait)
	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading request: %v", err)
	}
	svc.expire(context.Background(), requestID)

	// Verify the request is now expired
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusExpired {
		t.Fatalf("expected status Expired after manual expiry, got %q", req.Status)
	}

	// Try to approve the expired request — this should fail
	approveErr := svc.Approve(context.Background(), requestID, identity, DecisionContext{})
	if approveErr == nil {
		t.Fatal("expected Approve to fail on an expired request")
	}

	// Verify status is still Expired (Approve didn't change it)
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusExpired {
		t.Errorf("expected status Expired after failed approve, got %q (a successful approve should not have changed it)", req.Status)
	}
}

// TestCertRequestService_Approve_ShouldBindTheRequestToTheApprovingUser pins
// the claim half of the binding: an unclaimed request becomes the approver's.
func TestCertRequestService_Approve_ShouldBindTheRequestToTheApprovingUser(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	wantUserID := seedUser(t, svc.db, identity.Subject)

	requestID := mustCreateUserRequest(t, svc)

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if req.UserID == nil {
		t.Fatal("expected the request to be bound to a user")
	}
	if *req.UserID != wantUserID {
		t.Errorf("got user_id %q, want %q", *req.UserID, wantUserID)
	}
}

// TestCertRequestService_Approve_ShouldRejectAnApproverWhoIsNotTheRequester
// is the reason the binding exists. The certificate carries the *approver's*
// principals over the *requester's* public key, so letting a second user
// approve someone else's pending request would hand that requester a
// certificate impersonating the approver.
func TestCertRequestService_Approve_ShouldRejectAnApproverWhoIsNotTheRequester(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	alice := &Identity{Username: "alice", Subject: "sub-alice"}
	bob := &Identity{Username: "bob", Subject: "sub-bob"}
	seedUser(t, svc.db, alice.Subject)
	seedUser(t, svc.db, bob.Subject)

	requestID := mustCreateUserRequest(t, svc)

	// Bind it to alice while leaving it pending — the state a request is in
	// once alice has opened the approval page but not yet decided.
	if err := svc.bindRequester(context.Background(), &model.CertificateRequest{ID: requestID}, alice); err != nil {
		t.Fatalf("unexpected error binding the request to alice: %v", err)
	}

	// Bob must not be able to act on it, even though he is authenticated.
	err := svc.Approve(context.Background(), requestID, bob, DecisionContext{})
	if err == nil {
		t.Fatal("expected bob's approve to fail on a request bound to alice")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("got error %v, want a ForbiddenError", err)
	}
}

// TestCertRequestService_Approve_ShouldRejectAnIdentityWithNoUserRecord
// fails closed: without a users row there is nothing to bind to, so the
// request must not be approved rather than silently left unbound.
func TestCertRequestService_Approve_ShouldRejectAnIdentityWithNoUserRecord(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID := mustCreateUserRequest(t, svc)

	err := svc.Approve(context.Background(), requestID, &Identity{Username: "ghost", Subject: "sub-ghost"}, DecisionContext{})
	if err == nil {
		t.Fatal("expected approve to fail for an identity with no users row")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("got error %v, want a ForbiddenError", err)
	}
}

// TestCertRequestService_Approve_ShouldEnforceRequireGroupOnUserCertificates
// covers both directions of the newly reachable gate, including the
// backward-compatible case where an empty value restricts nobody.
func TestCertRequestService_Approve_ShouldEnforceRequireGroupOnUserCertificates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		requireGroup string
		groups       []string
		wantErr      bool
	}{
		{
			name:         "should allow anyone when require_group is unset",
			requireGroup: "",
			groups:       nil,
			wantErr:      false,
		},
		{
			name:         "should allow a member of the required group",
			requireGroup: "ssh-users",
			groups:       []string{"other", "ssh-users"},
			wantErr:      false,
		},
		{
			name:         "should reject a non-member",
			requireGroup: "ssh-users",
			groups:       []string{"other"},
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := config.CertificateOptions{}
			opts.User.ValidDuration = time.Hour
			opts.User.RequireGroup = tt.requireGroup

			svc := newTestCertRequestServiceWithOptions(t, opts)
			identity := &Identity{Username: "alice", Subject: "sub-alice", Groups: tt.groups}
			seedUser(t, svc.db, identity.Subject)

			requestID := mustCreateUserRequest(t, svc)

			err := svc.Approve(context.Background(), requestID, identity, DecisionContext{})
			if tt.wantErr && err == nil {
				t.Error("expected approve to be rejected by require_group")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// mustCreateUserRequest creates a pending user certificate request and
// returns its ID.
func mustCreateUserRequest(t *testing.T, svc *CertRequestService) string {
	t.Helper()

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA test",
	})
	if err != nil {
		t.Fatalf("failed to create certificate request: %v", err)
	}
	return requestID
}

// TestBindRequester_ShouldAtomicallyClaimUnclaimed Requests covers
// the WHERE user_id IS NULL guard that prevents race-condition claim overwrites.
// Two concurrent approvers racing to bind the same unclaimed request should
// result in exactly one successful binding. The guard ensures RowsAffected==0
// for the loser, preventing a claim overwrite.
//
// Because the in-memory SQLite pool uses SetMaxOpenConns(1), actual concurrency
// is serialized. To test the atomic predicate directly, we simulate the race
// by having the first approver bind successfully, then manually clear the
// binding, and then verify a second approver's direct UPDATE (skipping the
// early req.UserID!=nil check) is prevented by the WHERE guard.
// TestApprove_ShouldRejectDuplicateBindingAttempt verifies that after a
// request is bound to one approver, a second approver cannot override that
// binding. This is the sequential-test validation of the atomic claim-on-approve
// predicate (WHERE user_id IS NULL in bindRequester).
//
// Note: The atomic predicate prevents concurrent claim races, which cannot be
// directly tested with in-memory SQLite (SetMaxOpenConns(1) serializes access,
// and multiple connections to ":memory:" are separate databases). However,
// this test validates the critical property: once bound, the request cannot
// be re-bound by another approver. The mutation test (removing AND user_id IS NULL)
// proved this guard has zero test coverage in the sequential suite.
func TestApprove_ShouldRejectDuplicateBindingAttempt(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	alice := &Identity{Username: "alice", Subject: "sub-alice"}
	bob := &Identity{Username: "bob", Subject: "sub-bob"}
	seedUser(t, svc.db, alice.Subject)
	seedUser(t, svc.db, bob.Subject)

	requestID := mustCreateUserRequest(t, svc)

	// Alice binds the request by directly calling bindRequester
	req := &model.CertificateRequest{ID: requestID}
	if err := svc.bindRequester(context.Background(), req, alice); err != nil {
		t.Fatalf("unexpected error binding to alice: %v", err)
	}

	// Bob attempts to bind the same request
	// The WHERE user_id IS NULL guard should prevent this
	req2 := &model.CertificateRequest{ID: requestID}
	err := svc.bindRequester(context.Background(), req2, bob)
	if err == nil {
		t.Fatal("expected bob's bindRequester to fail on a request already bound to alice")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("expected ForbiddenError, got %v", err)
	}
}

// TestCertRequestService_Detail_ShouldBindOnFirstView is the reason Detail
// binds at all: the approval page loads before anyone decides, so a request
// should be owned from the moment its owner opens it rather than only once
// they click approve.
func TestCertRequestService_Detail_ShouldBindOnFirstView(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	wantUserID := seedUser(t, svc.db, identity.Subject)

	requestID := mustCreateUserRequest(t, svc)

	if _, err := svc.Detail(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if req.UserID == nil || *req.UserID != wantUserID {
		t.Errorf("expected viewing the request to bind it to alice, got %v", req.UserID)
	}
}

// TestCertRequestService_Detail_ShouldRejectAViewerWhoIsNotTheOwner means a
// second user finds out on page load rather than after clicking approve.
func TestCertRequestService_Detail_ShouldRejectAViewerWhoIsNotTheOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	alice := &Identity{Username: "alice", Subject: "sub-alice"}
	bob := &Identity{Username: "bob", Subject: "sub-bob"}
	seedUser(t, svc.db, alice.Subject)
	seedUser(t, svc.db, bob.Subject)

	requestID := mustCreateUserRequest(t, svc)

	if _, err := svc.Detail(context.Background(), requestID, alice); err != nil {
		t.Fatalf("unexpected error on alice's view: %v", err)
	}

	_, err := svc.Detail(context.Background(), requestID, bob)
	if err == nil {
		t.Fatal("expected bob's view of alice's request to be refused")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("got error %v, want a ForbiddenError", err)
	}
}

// TestCertRequestService_Detail_ShouldReportRequestedAndNarrowedSeparately
// is what lets the approval page show a human that an option they asked for
// is being trimmed — the hard constraint is that this is visible *before*
// approval, not discovered afterwards.
func TestCertRequestService_Detail_ShouldReportRequestedAndNarrowedSeparately(t *testing.T) {
	t.Parallel()

	opts := config.CertificateOptions{}
	opts.User.ValidDuration = 10 * time.Hour
	opts.User.Extensions = []string{"permit-pty"}

	svc := newTestCertRequestServiceWithOptions(t, opts)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA test",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-port-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	detail, err := svc.Detail(context.Background(), requestID, identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(detail.Requested.Extensions) != 2 {
		t.Errorf("got requested extensions %v, want both as submitted", detail.Requested.Extensions)
	}
	if len(detail.Narrowed.Extensions) != 1 || detail.Narrowed.Extensions[0] != "permit-pty" {
		t.Errorf("got narrowed extensions %v, want only the configured-permitted one", detail.Narrowed.Extensions)
	}
	if detail.ValidDuration != 10*time.Hour {
		t.Errorf("got valid duration %v, want 10h", detail.ValidDuration)
	}
	if len(detail.Principals) != 1 || detail.Principals[0] != "alice" {
		t.Errorf("got principals %v, want [alice]", detail.Principals)
	}
}

func TestCertRequestService_Detail_ShouldReturnNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	seedUser(t, svc.db, "sub-alice")

	_, err := svc.Detail(context.Background(), "does-not-exist", &Identity{Subject: "sub-alice"})

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("got error %v, want a NotFoundError", err)
	}
}

// TestCertRequestService_Detail_ShouldSurfaceACorruptOptionsColumn covers
// the requested_options decode failure: the column is free-form JSON with
// no DB-level schema behind it, so a row that somehow ended up with
// unparseable content must surface as an error rather than panic or
// silently zero out the requester's options.
func TestCertRequestService_Detail_ShouldSurfaceACorruptOptionsColumn(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("requested_options", "not json").Error; err != nil {
		t.Fatalf("failed to corrupt requested_options: %v", err)
	}

	if _, err := svc.Detail(context.Background(), requestID, identity); err == nil {
		t.Error("Detail() error = nil, want error for an unparseable requested_options column")
	}
}

// seedRequestWithUnsupportedType stores a pending request whose type is not
// one the enum defines, and returns its ID.
//
// The schema's chk_certificate_requests_type CHECK constraint now refuses
// such a row, so it cannot be produced through CreateRequest — which is the
// point of the constraint. The Go-side guard in resolveCertOptions is
// defence in depth behind it and still needs its own coverage: a database
// created before the constraint existed, a direct SQL write, or a
// CertificateType added to enums.go without the matching migration can all
// still put an unrecognized type in front of that code. SQLite's
// ignore_check_constraints pragma is what lets the test build that row
// without weakening the schema everything else in the file runs against.
func seedRequestWithUnsupportedType(t *testing.T, db *gorm.DB) string {
	t.Helper()

	// Suspending the CHECK is dialect-specific. SQLite has a pragma for it;
	// Postgres has no equivalent session switch, so drop the constraint for
	// the life of the test instead. Each Postgres test owns its own schema
	// (see newTestPostgresDB), so dropping it here cannot affect anything
	// running in parallel.
	if db.Dialector.Name() == "postgres" {
		if err := db.Exec(`ALTER TABLE certificate_requests DROP CONSTRAINT IF EXISTS chk_certificate_requests_type`).Error; err != nil {
			t.Fatalf("failed to drop the type check constraint: %v", err)
		}
	} else {
		if err := db.Exec(`PRAGMA ignore_check_constraints = ON`).Error; err != nil {
			t.Fatalf("failed to suspend check constraints: %v", err)
		}
		t.Cleanup(func() {
			if err := db.Exec(`PRAGMA ignore_check_constraints = OFF`).Error; err != nil {
				t.Errorf("failed to restore check constraints: %v", err)
			}
		})
	}

	req := model.CertificateRequest{
		ID:        uuid.NewString(),
		Type:      model.CertificateType("bogus"),
		PublicKey: "ssh-ed25519 AAAA...",
		Status:    model.CertificateRequestStatusPending,
		CreatedAt: time.Now(),
	}
	if err := db.Create(&req).Error; err != nil {
		t.Fatalf("failed to seed a request with an unsupported type: %v", err)
	}
	return req.ID
}

// TestCertRequestService_Detail_ShouldSurfaceAnUnsupportedStoredType covers
// Detail's own resolveCertOptions error path: a row that somehow ended up
// with an unrecognized type must surface as an error rather than a
// zero-value policy.
func TestCertRequestService_Detail_ShouldSurfaceAnUnsupportedStoredType(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID := seedRequestWithUnsupportedType(t, svc.db)

	if _, err := svc.Detail(context.Background(), requestID, identity); err == nil {
		t.Error("Detail() error = nil, want error for an unsupported stored certificate type")
	}
}

// TestCertRequestService_Approve_ShouldSurfaceAnUnsupportedStoredType is
// Approve's counterpart to the Detail test above.
func TestCertRequestService_Approve_ShouldSurfaceAnUnsupportedStoredType(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID := seedRequestWithUnsupportedType(t, svc.db)

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Error("Approve() error = nil, want error for an unsupported stored certificate type")
	}
}

// TestCreateRequest_ShouldRejectATypeTheSchemaDoesNotAllow is the other side
// of the pair above: through the normal path, the CHECK constraint is what
// stops an unrecognized type from ever reaching the table.
func TestCreateRequest_ShouldRejectATypeTheSchemaDoesNotAllow(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)

	_, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateType("bogus"),
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err == nil {
		t.Error("CreateRequest() error = nil, want a CHECK constraint violation for an undefined certificate type")
	}
}

// TestBindRequester_ShouldNoOpForARepeatViewByTheSameOwner covers the
// already-bound-to-this-user early return: Detail's binding is idempotent
// for the owner (a second page load isn't a second claim attempt).
func TestBindRequester_ShouldNoOpForARepeatViewByTheSameOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if _, err := svc.Detail(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error on first view: %v", err)
	}
	// Second view by the same owner must succeed too, not just the first.
	if _, err := svc.Detail(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error on repeat view by the owner: %v", err)
	}
}

// TestBindRequester_ShouldDetectARacingClaim covers the guarded-UPDATE's
// RowsAffected==0 path deterministically: rather than racing two real
// goroutines (inherently timing-dependent), it seeds the DB row as already
// claimed by a different user before calling bindRequester with an in-memory
// req that still thinks the row is unclaimed — exactly the state a second
// caller would observe if it lost a real race between reading the row and
// issuing its own guarded UPDATE.
func TestBindRequester_ShouldDetectARacingClaim(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	winnerID := seedUser(t, svc.db, "sub-winner")
	loser := &Identity{Username: "loser", Subject: "sub-loser"}
	seedUser(t, svc.db, loser.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("user_id", winnerID).Error; err != nil {
		t.Fatalf("failed to seed the winning claim: %v", err)
	}

	req := &model.CertificateRequest{ID: requestID, UserID: nil}
	err := svc.bindRequester(context.Background(), req, loser)

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("got error %v (%T), want a ForbiddenError for a request claimed by someone else", err, err)
	}
}

// TestApproveServiceEnrollment_ShouldRefuseARequestThatIsNoLongerPending
// covers approveServiceEnrollment's own guarded-UPDATE RowsAffected==0
// branch directly. Approve's own top-level pending check (line ~291) always
// catches a non-pending row first when called through Approve — including
// with a genuine second Approve call — so this branch is only reachable via
// a real race between two concurrent Approve calls both passing that check
// before either commits. Calling approveServiceEnrollment directly (an
// unexported, in-package-testable method) after flipping the row's status
// out from under it reproduces exactly that race deterministically.
func TestApproveServiceEnrollment_ShouldRefuseARequestThatIsNoLongerPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeService, PublicKey: "ssh-ed25519 AAAA... svc"})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusDenied).Error; err != nil {
		t.Fatalf("failed to flip the request's status: %v", err)
	}
	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to load request: %v", err)
	}
	policy, _ := svc.policyFor(model.CertificateTypeService)
	if err := svc.approveServiceEnrollment(context.Background(), req, RequestedOptions{}, &Identity{Username: "approver", Subject: "sub-approver"}, policy, DecisionContext{}); err == nil {
		t.Error("approveServiceEnrollment() error = nil, want error for a request that lost the pending race")
	}
}

// TestApproveForSigning_ShouldRefuseARequestThatIsNoLongerPending is
// approveForSigning's counterpart to
// TestApproveServiceEnrollment_ShouldRefuseARequestThatIsNoLongerPending —
// same reasoning, same technique: Approve's own top-level check makes this
// guard only reachable via a genuine race, reproduced deterministically by
// calling approveForSigning directly after flipping the row's status.
func TestApproveForSigning_ShouldRefuseARequestThatIsNoLongerPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	requestID := mustCreateUserRequest(t, svc)
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusDenied).Error; err != nil {
		t.Fatalf("failed to flip the request's status: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to reload request: %v", err)
	}

	if err := svc.approveForSigning(context.Background(), req, identity, svc.policies[model.CertificateTypeUser], RequestedOptions{}, DecisionContext{}); err == nil {
		t.Error("approveForSigning() error = nil, want error for a request that lost the pending race")
	}
}

// failingPublisher is a message.Publisher whose Publish call always fails,
// standing in for a broker that's unreachable at approval time.
type failingPublisher struct{}

func (failingPublisher) Publish(_ string, _ ...*message.Message) error {
	return errors.New("publish unavailable")
}
func (failingPublisher) Close() error { return nil }

// TestCertRequestService_Approve_ShouldSurfaceAPublishFailure covers
// approveForSigning's own publish error: the row is already marked Signing
// by this point (left for the invalidation sweep to catch, per the doc
// comment on the call site) — Approve itself must still report the failure
// to its caller rather than claim success.
func TestCertRequestService_Approve_ShouldSurfaceAPublishFailure(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.CertificateRequestDecision{}, &model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{CertOptions: config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	}}, db, failingPublisher{}, channel)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Error("Approve() error = nil, want error when publishing the signing job fails")
	}
}

// TestCertRequestService_Deny_ShouldApplyTheTTLFilterWhenConfigured covers
// Deny's ttlCutoff branch: with RequestTTL set, a pending-but-expired
// request must not be denied by this path — Wait's own lazy expiry owns
// that transition (see reconcileStatus) — so Deny reports it as already
// non-pending, same as any other terminal request.
func TestCertRequestService_Deny_ShouldApplyTheTTLFilterWhenConfigured(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Minute)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Back-date CreatedAt past the TTL directly in the DB — mirrors how
	// TestCertRequestService_Wait_ShouldExpireRequestPastTTL ages a row.
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("created_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("failed to back-date created_at: %v", err)
	}

	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err == nil {
		t.Fatal("expected Deny to refuse a request past its TTL, got nil")
	}
}

// TestCertRequestService_Wait_ShouldReportATerminalStatusFoundColdInTheDB
// covers reconcileStatus's default case (denied/expired/failed) — reachable
// whenever a Wait call finds a request already resolved in the DB but not
// yet in the in-memory resolved cache, e.g. a reconnect after a process
// restart. Constructed directly here (bypassing Deny/expire, which already
// populate the cache themselves) so the cache is guaranteed cold.
func TestCertRequestService_Wait_ShouldReportATerminalStatusFoundColdInTheDB(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Updates(map[string]any{"status": model.CertificateRequestStatusFailed, "resolved_at": time.Now()}).Error; err != nil {
		t.Fatalf("failed to mark the request failed directly in the DB: %v", err)
	}

	status, _, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusFailed {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusFailed)
	}
}

// TestExpire_ShouldBeANoOpTheSecondTime covers expire's own
// RowsAffected==0 early return: called again on a request it (or a Deny/
// Approve racing it) already resolved, it must do nothing rather than
// double-notify — expire is unexported and called from multiple sites
// (Approve's TTL check, reconcileStatus), so this exercises it directly
// rather than threading the call through one specific caller.
func TestExpire_ShouldBeANoOpTheSecondTime(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	svc.expire(context.Background(), requestID)

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to reload request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusExpired {
		t.Fatalf("got status %q after first expire(), want %q", req.Status, model.CertificateRequestStatusExpired)
	}

	// Second call: RowsAffected must be 0 (already expired), and this must
	// not panic or otherwise misbehave.
	svc.expire(context.Background(), requestID)
}

// TestNotifyWaiter_ShouldNotPanicWhenPublishingFails covers notifyWaiter's
// own Publish error branch: a publish failure here is logged, not fatal to
// the caller (Deny/expire's own DB update already succeeded — see the
// function's doc comment), so this only asserts it doesn't panic.
func TestNotifyWaiter_ShouldNotPanicWhenPublishingFails(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.CertificateRequestDecision{}, &model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{}, db, failingPublisher{}, channel)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}

	svc.notifyWaiter("req-1", requestOutcome{status: model.CertificateRequestStatusDenied})

	svc.mu.Lock()
	_, cached := svc.resolved["req-1"]
	svc.mu.Unlock()
	if !cached {
		t.Error("expected the outcome to be cached even though publishing failed")
	}
}

// TestCertRequestService_Wait_MultiInstance_ShouldDecodeWakeMessageCertificate
// is the regression test for the multi-instance safety fix: a client waiting
// on instance B should receive a certificate issued on instance A via the
// wake message, without needing to round-trip to the database. This exercises
// the payload decoding path in Wait added by docs/dev/multi-instance-safety-plan.md.
//
// The test constructs two CertRequestService instances over the same database
// and transport, creates a request on one, and has the other's Wait decode the
// certificate from the wake message. This is the decisive regression test
// named in the safety plan: it fails before the fix and passes after.
func TestCertRequestService_Wait_MultiInstance_ShouldDecodeWakeMessageCertificate(t *testing.T) {
	t.Parallel()

	// Shared database and transport for two "instances" (really just two
	// service objects). In a real multi-instance setup, these would be
	// separate processes; here they're in-process but isolated.
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.CertificateRequestDecision{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	// Instance A: creates the request and issues the certificate.
	instanceA, err := NewCertRequestService(&config.Config{}, db, channel, channel)
	if err != nil {
		t.Fatalf("failed to construct instance A: %v", err)
	}

	// Instance B: waits for the request to resolve. In multi-instance, the
	// client's SSE connection lands on a different instance than the one
	// handling approval.
	instanceB, err := NewCertRequestService(&config.Config{}, db, channel, channel)
	if err != nil {
		t.Fatalf("failed to construct instance B: %v", err)
	}

	// Create request on instance A.
	requestID, err := instanceA.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request on instance A: %v", err)
	}

	// Instance B starts waiting before instance A publishes the certificate.
	// This captures the multi-instance scenario: the waiting client and the
	// issuing instance are separate.
	type waitResult struct {
		status      model.CertificateRequestStatus
		certificate string
		code        string
		err         error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, certificate, code, err := instanceB.Wait(context.Background(), requestID)
		done <- waitResult{status, certificate, code, err}
	}()

	// Long enough for instance B's Wait to have subscribed and reached its
	// blocking select before instance A publishes.
	time.Sleep(50 * time.Millisecond)

	// Instance A marks the request as Approved in the database (simulating
	// what the approval handler does), then publishes a wake message with
	// the certificate payload. The wake message is optimized delivery of
	// the certificate bytes, but the DB status is what authorizes the
	// delivery (see tryHandleWakeMessage — it verifies the DB before
	// trusting the message payload).
	if err := instanceA.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusApproved).Error; err != nil {
		t.Fatalf("failed to mark request approved in database: %v", err)
	}

	testCertificate := "ssh-cert-v01@openssh.com AAAAg..."
	instanceA.notifyWaiter(requestID, requestOutcome{
		status:      model.CertificateRequestStatusApproved,
		certificate: testCertificate,
	})

	// Instance B's Wait should unblock and return the certificate from the
	// wake message, with the DB serving as the authority for the approval
	// decision. This tests multi-instance delivery: instance B receives a
	// certificate issued on instance A, with authorization verified through
	// the shared database.
	var res waitResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("instance B's Wait did not unblock after instance A published the wake message")
	}

	if res.err != nil {
		t.Fatalf("unexpected error from instance B's Wait: %v", res.err)
	}
	if res.status != model.CertificateRequestStatusApproved {
		t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusApproved)
	}
	if res.certificate != testCertificate {
		t.Errorf("got certificate %q, want %q", res.certificate, testCertificate)
	}
	if res.code != "" {
		t.Errorf("got code %q, want empty string", res.code)
	}
}

// boolPtr returns a pointer to b, for the tri-state FIPS setting.
func boolPtr(b bool) *bool { return &b }

// generateTestECDSAAuthorizedKey returns a throwaway P-384 ECDSA public key
// in authorized_keys format: FIPS-approved, unlike
// generateTestSSHPrivateKey's ed25519 output.
func generateTestECDSAAuthorizedKey(t *testing.T) string {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ecdsa key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("failed to derive public key: %v", err)
	}
	return strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
}

func TestCertRequestService_Approve_FIPS(t *testing.T) {
	t.Parallel()

	certOpts := config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	}

	t.Run("should reject a non-FIPS-approved client key when FIPS is enabled", func(t *testing.T) {
		t.Parallel()

		svc := newTestCertRequestServiceWithConfig(t, &config.Config{CertOptions: certOpts, FIPS: boolPtr(true)})

		_, edKey := generateTestSSHPrivateKey(t)
		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeUser,
			PublicKey: edKey,
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}

		identity := &Identity{Username: "alice", Subject: "sub-fips-reject"}
		seedUser(t, svc.db, identity.Subject)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
			t.Fatal("expected Approve to reject a non-FIPS-approved key when FIPS is enabled")
		}
	})

	t.Run("should accept a FIPS-approved client key when FIPS is enabled", func(t *testing.T) {
		t.Parallel()

		svc := newTestCertRequestServiceWithConfig(t, &config.Config{CertOptions: certOpts, FIPS: boolPtr(true)})

		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeUser,
			PublicKey: generateTestECDSAAuthorizedKey(t),
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}

		identity := &Identity{Username: "bob", Subject: "sub-fips-accept"}
		seedUser(t, svc.db, identity.Subject)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
			t.Errorf("unexpected error approving a FIPS-approved key: %v", err)
		}
	})

	t.Run("should reject an unparseable client public key when FIPS is enabled", func(t *testing.T) {
		t.Parallel()

		svc := newTestCertRequestServiceWithConfig(t, &config.Config{CertOptions: certOpts, FIPS: boolPtr(true)})

		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeUser,
			PublicKey: "not a valid public key",
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}

		identity := &Identity{Username: "dave", Subject: "sub-fips-unparseable"}
		seedUser(t, svc.db, identity.Subject)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
			t.Fatal("expected Approve to reject an unparseable public key when FIPS is enabled")
		}
	})

	t.Run("should not restrict the client key type when FIPS is disabled", func(t *testing.T) {
		t.Parallel()

		svc := newTestCertRequestServiceWithConfig(t, &config.Config{CertOptions: certOpts, FIPS: boolPtr(false)})

		_, edKey := generateTestSSHPrivateKey(t)
		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeUser,
			PublicKey: edKey,
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}

		identity := &Identity{Username: "carol", Subject: "sub-fips-off"}
		seedUser(t, svc.db, identity.Subject)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
			t.Errorf("unexpected error approving a non-approved key with FIPS disabled: %v", err)
		}
	})
}

// decisionFor reads back requestID's certificate_request_decisions row,
// failing the test if it's missing — the shared assertion helper for every
// test below that expects Approve/Deny to have recorded one.
func decisionFor(t *testing.T, db *gorm.DB, requestID string) model.CertificateRequestDecision {
	t.Helper()
	var decision model.CertificateRequestDecision
	if err := db.First(&decision, "certificate_request_id = ?", requestID).Error; err != nil {
		t.Fatalf("expected a decision row for request %q: %v", requestID, err)
	}
	return decision
}

// TestCertRequestService_Approve_ShouldPersistFullDecisionAudit is the core
// assertion behind docs/dev/changes-next.md's "keep the
// entire set of data about a user that approves/rejects a request": every
// field on Identity — not just Subject/Username — plus the full connection
// context, lands in the decision row Approve's signing branch writes.
func TestCertRequestService_Approve_ShouldPersistFullDecisionAudit(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{
		Username:        "alice",
		Subject:         "sub-1",
		Email:           "alice@example.com",
		Groups:          []string{"engineering", "sre"},
		OtherAccounts:   []string{"alice.other"},
		ServiceAccounts: []string{"svc-backup"},
	}
	seedUser(t, svc.db, identity.Subject)

	dc := DecisionContext{
		SourceIP:       "198.51.100.7",
		UserAgent:      "curl/8.0.0",
		AcceptLanguage: "en-US",
		ForwardedFor:   "198.51.100.7, 10.0.0.1",
	}
	before := time.Now()
	if err := svc.Approve(context.Background(), requestID, identity, dc); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	decision := decisionFor(t, svc.db, requestID)
	if decision.Outcome != model.CertificateRequestDecisionApproved {
		t.Errorf("got outcome %q, want %q", decision.Outcome, model.CertificateRequestDecisionApproved)
	}
	if decision.Subject != identity.Subject {
		t.Errorf("got Subject %q, want %q", decision.Subject, identity.Subject)
	}
	if decision.Username != identity.Username {
		t.Errorf("got Username %q, want %q", decision.Username, identity.Username)
	}
	if decision.Email != identity.Email {
		t.Errorf("got Email %q, want %q", decision.Email, identity.Email)
	}
	assertJSONStringSlice(t, "Groups", decision.Groups, identity.Groups)
	assertJSONStringSlice(t, "OtherAccounts", decision.OtherAccounts, identity.OtherAccounts)
	assertJSONStringSlice(t, "ServiceAccounts", decision.ServiceAccounts, identity.ServiceAccounts)
	if decision.SourceIP != dc.SourceIP {
		t.Errorf("got SourceIP %q, want %q", decision.SourceIP, dc.SourceIP)
	}
	if decision.UserAgent != dc.UserAgent {
		t.Errorf("got UserAgent %q, want %q", decision.UserAgent, dc.UserAgent)
	}
	if decision.AcceptLanguage != dc.AcceptLanguage {
		t.Errorf("got AcceptLanguage %q, want %q", decision.AcceptLanguage, dc.AcceptLanguage)
	}
	if decision.ForwardedFor != dc.ForwardedFor {
		t.Errorf("got ForwardedFor %q, want %q", decision.ForwardedFor, dc.ForwardedFor)
	}
	if decision.DecidedAt.Before(before) {
		t.Errorf("got DecidedAt %v, want it no earlier than %v", decision.DecidedAt, before)
	}

	// decided_by_subject is a denormalized copy for indexed search (see
	// the migration's comment) — it must never drift from the source of
	// truth inside the row it's copied from.
	if decision.Subject != identity.Subject {
		t.Errorf("decision.Subject %q does not match identity.Subject %q", decision.Subject, identity.Subject)
	}
	var viaColumn model.CertificateRequestDecision
	if err := svc.db.First(&viaColumn, "certificate_request_id = ? AND subject = ?", requestID, identity.Subject).Error; err != nil {
		t.Errorf("expected the row to be findable by its denormalized subject column: %v", err)
	}
}

// assertJSONStringSlice decodes raw (a JSON-encoded []string column) and
// compares it against want.
func assertJSONStringSlice(t *testing.T, field, raw string, want []string) {
	t.Helper()
	var got []string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("failed to decode %s: %v", field, err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %s %v, want %v", field, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %s %v, want %v", field, got, want)
			break
		}
	}
}

// TestCertRequestService_Approve_ShouldPersistDecisionAuditOnEnrollment
// covers the service-enrollment branch (approveServiceEnrollment), which
// writes its decision row through a different code path than the signing
// branch above.
func TestCertRequestService_Approve_ShouldPersistDecisionAuditOnEnrollment(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", Email: "alice@example.com"}
	seedUser(t, svc.db, identity.Subject)
	dc := DecisionContext{SourceIP: "198.51.100.7", UserAgent: "curl/8.0.0"}
	if err := svc.Approve(context.Background(), requestID, identity, dc); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	decision := decisionFor(t, svc.db, requestID)
	if decision.Outcome != model.CertificateRequestDecisionApproved {
		t.Errorf("got outcome %q, want %q", decision.Outcome, model.CertificateRequestDecisionApproved)
	}
	if decision.Subject != identity.Subject || decision.SourceIP != dc.SourceIP {
		t.Errorf("decision row does not match: %+v", decision)
	}
}

// TestCertRequestService_Deny_ShouldPersistDecisionAudit mirrors the
// Approve assertions above for the deny path.
func TestCertRequestService_Deny_ShouldPersistDecisionAudit(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Deny never calls bindRequester (see Deny's doc comment), so no
	// seedUser call is needed here — unlike every Approve test above.
	identity := &Identity{Username: "bob", Subject: "sub-denier", Email: "bob@example.com"}
	dc := DecisionContext{SourceIP: "203.0.113.9", UserAgent: "Mozilla/5.0"}
	if err := svc.Deny(context.Background(), requestID, identity, dc); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	decision := decisionFor(t, svc.db, requestID)
	if decision.Outcome != model.CertificateRequestDecisionDenied {
		t.Errorf("got outcome %q, want %q", decision.Outcome, model.CertificateRequestDecisionDenied)
	}
	if decision.Subject != identity.Subject {
		t.Errorf("got Subject %q, want %q", decision.Subject, identity.Subject)
	}
	if decision.SourceIP != dc.SourceIP {
		t.Errorf("got SourceIP %q, want %q", decision.SourceIP, dc.SourceIP)
	}
}

// TestCertRequestService_Approve_ShouldRollBackStatusWhenDecisionInsertFails
// is the regression test for the transaction wrapping approveForSigning
// added around its status update and decision insert: forcing the insert
// to fail (a duplicate certificate_request_id, via the UNIQUE constraint)
// must leave the status update rolled back too, not half-applied.
func TestCertRequestService_Approve_ShouldRollBackStatusWhenDecisionInsertFails(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)

	// Pre-seed a decision row for this exact request so Approve's own
	// insert collides with the UNIQUE constraint on certificate_request_id
	// and fails.
	if err := svc.db.Create(&model.CertificateRequestDecision{
		ID:                   uuid.NewString(),
		CertificateRequestID: requestID,
		Outcome:              model.CertificateRequestDecisionApproved,
		DecidedAt:            time.Now(),
	}).Error; err != nil {
		t.Fatalf("failed to seed a colliding decision row: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected Approve to fail when the decision insert collides, got nil")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusPending {
		t.Errorf("got status %q after a rolled-back approval, want %q — the status update was not rolled back with the failed insert", req.Status, model.CertificateRequestStatusPending)
	}
}

// TestCertRequestService_ApproveServiceEnrollment_ShouldRollBackWhenDecisionInsertFails
// is TestCertRequestService_Approve_ShouldRollBackStatusWhenDecisionInsertFails's
// counterpart for the enrollment branch, which writes its decision through
// a separate transaction in approveServiceEnrollment.
func TestCertRequestService_ApproveServiceEnrollment_ShouldRollBackWhenDecisionInsertFails(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)

	if err := svc.db.Create(&model.CertificateRequestDecision{
		ID:                   uuid.NewString(),
		CertificateRequestID: requestID,
		Outcome:              model.CertificateRequestDecisionApproved,
		DecidedAt:            time.Now(),
	}).Error; err != nil {
		t.Fatalf("failed to seed a colliding decision row: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected Approve to fail when the decision insert collides, got nil")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusPending {
		t.Errorf("got status %q after a rolled-back enrollment, want %q — the status update was not rolled back with the failed insert", req.Status, model.CertificateRequestStatusPending)
	}
	if req.EnrollmentToken != "" {
		t.Errorf("got a non-empty EnrollmentToken %q after a rolled-back enrollment, want it unset", req.EnrollmentToken)
	}
}

// TestCertRequestService_Deny_ShouldRollBackStatusWhenDecisionInsertFails
// mirrors the same regression test for Deny's own transaction.
func TestCertRequestService_Deny_ShouldRollBackStatusWhenDecisionInsertFails(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.db.Create(&model.CertificateRequestDecision{
		ID:                   uuid.NewString(),
		CertificateRequestID: requestID,
		Outcome:              model.CertificateRequestDecisionDenied,
		DecidedAt:            time.Now(),
	}).Error; err != nil {
		t.Fatalf("failed to seed a colliding decision row: %v", err)
	}

	identity := &Identity{Username: "bob", Subject: "sub-denier"}
	if err := svc.Deny(context.Background(), requestID, identity, DecisionContext{}); err == nil {
		t.Fatal("expected Deny to fail when the decision insert collides, got nil")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusPending {
		t.Errorf("got status %q after a rolled-back denial, want %q — the status update was not rolled back with the failed insert", req.Status, model.CertificateRequestStatusPending)
	}
}

// TestLookupDecision_ShouldSurfaceAGenericDBError covers lookupDecision's
// own error branch directly (as opposed to Detail's propagation of it,
// which needs per-query fault injection this codebase doesn't have — see
// exclude-from-coverage.txt). Called in isolation, lookupDecision is a
// single query, so closing the connection first reaches it directly.
func TestLookupDecision_ShouldSurfaceAGenericDBError(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	closeUnderlyingDB(t, svc.db)

	if _, err := svc.lookupDecision(context.Background(), "some-id"); err == nil {
		t.Error("lookupDecision() error = nil, want error")
	}
}

// TestCertRequestService_Approve_DecisionAuditShouldBeImmutableToLaterUserChanges
// is the regression test for the immutability requirement itself: a
// decision row is built entirely from the *Identity passed into
// Approve/Deny, never re-derived from the users table, so a later change to
// that table's row must never be reflected in a decision already recorded.
func TestCertRequestService_Approve_DecisionAuditShouldBeImmutableToLaterUserChanges(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", Email: "alice@example.com"}
	userID := seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	// Simulate the user's account being renamed after the fact — e.g. a
	// later OIDC login under a changed preferred_username.
	if err := svc.db.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{"username": "alice-renamed", "email": "alice-new@example.com"}).Error; err != nil {
		t.Fatalf("failed to simulate a user record update: %v", err)
	}

	decision := decisionFor(t, svc.db, requestID)
	if decision.Username != "alice" {
		t.Errorf("got decision Username %q after a later user rename, want it frozen at %q", decision.Username, "alice")
	}
	if decision.Email != "alice@example.com" {
		t.Errorf("got decision Email %q after a later user update, want it frozen at %q", decision.Email, "alice@example.com")
	}
}

// TestCertRequestService_Detail_ShouldReturnNilDecisionForAPendingRequest
// covers lookupDecision's "not found is not an error" case directly: most
// requests being viewed haven't been decided yet.
func TestCertRequestService_Detail_ShouldReturnNilDecisionForAPendingRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	detail, err := svc.Detail(context.Background(), requestID, identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.Decision != nil {
		t.Errorf("expected a nil Decision for a pending request, got %+v", detail.Decision)
	}
}

// TestCertRequestService_Detail_ShouldReturnTheDecisionAfterApproval closes
// the loop on the read path: once Approve has recorded a decision, Detail
// must surface it.
func TestCertRequestService_Detail_ShouldReturnTheDecisionAfterApproval(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	detail, err := svc.Detail(context.Background(), requestID, identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.Decision == nil {
		t.Fatal("expected a non-nil Decision after approval")
	}
	if detail.Decision.Outcome != model.CertificateRequestDecisionApproved {
		t.Errorf("got outcome %q, want %q", detail.Decision.Outcome, model.CertificateRequestDecisionApproved)
	}
}

// TestCertRequestService_CreateRequest_ShouldPersistLocalIdentity covers
// Phase 2 problem 1: a user-type request's local OS identity is stored
// as-submitted.
func TestCertRequestService_CreateRequest_ShouldPersistLocalIdentity(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:          model.CertificateTypeUser,
		PublicKey:     "ssh-ed25519 AAAA...",
		LocalUsername: "alice",
		LocalHostname: "alice-laptop",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.LocalUsername != "alice" || req.LocalHostname != "alice-laptop" {
		t.Errorf("got LocalUsername/LocalHostname %q/%q, want %q/%q", req.LocalUsername, req.LocalHostname, "alice", "alice-laptop")
	}
}

// TestCertRequestService_CreateRequest_ShouldUnionSourceIPIntoSourceAddresses
// covers Phase 2 problem 2: the server-observed SourceIP is folded into the
// stored SourceAddresses list rather than left as a second, disconnected
// value — the union docs/dev/ssoossh-context.md describes for a client behind
// NAT.
// TestCertRequestService_CreateRequest_ShouldNormalizeSourceAddresses covers
// the case that took the approval page down: net.IP.String() drops an IPv6
// zone, so one link-local address carried by several interfaces arrives as
// the same string twice. Link-local is dropped outright — it says nothing
// about reaching this machine from anywhere else — and the rest is deduped.
// The server does not trust the client to send a clean list, because any
// client version decides what it sends.
func TestCertRequestService_CreateRequest_ShouldNormalizeSourceAddresses(t *testing.T) {
	t.Parallel()

	const linkLocal = "fe80::e8a7:34ff:fe9f:c7a9"

	tests := []struct {
		name     string
		sourceIP string
		reported []string
		want     []string
	}{
		{
			name:     "should drop an IPv6 link-local address",
			reported: []string{"10.0.0.5", linkLocal, linkLocal},
			want:     []string{"10.0.0.5"},
		},
		{
			name:     "should drop an IPv4 link-local address",
			reported: []string{"10.0.0.5", "169.254.10.20"},
			want:     []string{"10.0.0.5"},
		},
		{
			name:     "should keep a value that does not parse as an IP",
			reported: []string{"not-an-ip", "10.0.0.5"},
			want:     []string{"not-an-ip", "10.0.0.5"},
		},
		{
			name:     "should yield nothing when every reported address is link-local",
			reported: []string{linkLocal, "169.254.10.20"},
			want:     nil,
		},
		{
			name:     "should keep the first occurrence of each address in order",
			reported: []string{"10.0.0.5", "192.168.1.20", "10.0.0.5"},
			want:     []string{"10.0.0.5", "192.168.1.20"},
		},
		{
			name:     "should leave an already-unique list untouched",
			reported: []string{"10.0.0.5", "192.168.1.20"},
			want:     []string{"10.0.0.5", "192.168.1.20"},
		},
		{
			name:     "should handle an empty list",
			reported: nil,
			want:     nil,
		},
		{
			name:     "should union the observed source IP after dropping link-local",
			sourceIP: "203.0.113.9",
			reported: []string{linkLocal, linkLocal},
			want:     []string{"203.0.113.9"},
		},
		{
			// The observed address is a fact the server established, not a
			// claim the client made, so it is recorded even when link-local.
			name:     "should keep a link-local address the server itself observed",
			sourceIP: linkLocal,
			reported: []string{"10.0.0.5"},
			want:     []string{"10.0.0.5", linkLocal},
		},
		{
			name:     "should not re-add an observed source IP the client already reported twice",
			sourceIP: "10.0.0.5",
			reported: []string{"10.0.0.5", "10.0.0.5"},
			want:     []string{"10.0.0.5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestService(t, 0)
			requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
				Type:      model.CertificateTypeUser,
				PublicKey: "ssh-ed25519 AAAA...",
				SourceIP:  tt.sourceIP,
				RequestedOptions: RequestedOptions{
					SourceAddresses: tt.reported,
				},
			})
			if err != nil {
				t.Fatalf("unexpected error creating request: %v", err)
			}

			var req model.CertificateRequest
			if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
				t.Fatalf("unexpected error reading back the request: %v", err)
			}
			var opts RequestedOptions
			if err := json.Unmarshal([]byte(req.RequestedOptions), &opts); err != nil {
				t.Fatalf("failed to decode persisted requested options: %v", err)
			}
			if !slices.Equal(opts.SourceAddresses, tt.want) {
				t.Errorf("got SourceAddresses %v, want %v", opts.SourceAddresses, tt.want)
			}
		})
	}
}

func TestCertRequestService_CreateRequest_ShouldUnionSourceIPIntoSourceAddresses(t *testing.T) {
	t.Parallel()

	t.Run("should append SourceIP when not already reported", func(t *testing.T) {
		t.Parallel()

		svc := newTestCertRequestService(t, 0)
		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeUser,
			PublicKey: "ssh-ed25519 AAAA...",
			SourceIP:  "203.0.113.9",
			RequestedOptions: RequestedOptions{
				SourceAddresses: []string{"10.0.0.5"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}

		var req model.CertificateRequest
		if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
			t.Fatalf("unexpected error reading back the request: %v", err)
		}
		var opts RequestedOptions
		if err := json.Unmarshal([]byte(req.RequestedOptions), &opts); err != nil {
			t.Fatalf("failed to decode persisted requested options: %v", err)
		}
		want := []string{"10.0.0.5", "203.0.113.9"}
		if len(opts.SourceAddresses) != len(want) {
			t.Fatalf("got SourceAddresses %v, want %v", opts.SourceAddresses, want)
		}
		for i := range want {
			if opts.SourceAddresses[i] != want[i] {
				t.Errorf("got SourceAddresses %v, want %v", opts.SourceAddresses, want)
				break
			}
		}
	})

	t.Run("should not duplicate SourceIP when already reported", func(t *testing.T) {
		t.Parallel()

		svc := newTestCertRequestService(t, 0)
		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeUser,
			PublicKey: "ssh-ed25519 AAAA...",
			SourceIP:  "203.0.113.9",
			RequestedOptions: RequestedOptions{
				SourceAddresses: []string{"203.0.113.9"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}

		var req model.CertificateRequest
		if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
			t.Fatalf("unexpected error reading back the request: %v", err)
		}
		var opts RequestedOptions
		if err := json.Unmarshal([]byte(req.RequestedOptions), &opts); err != nil {
			t.Fatalf("failed to decode persisted requested options: %v", err)
		}
		if len(opts.SourceAddresses) != 1 || opts.SourceAddresses[0] != "203.0.113.9" {
			t.Errorf("got SourceAddresses %v, want exactly [\"203.0.113.9\"] with no duplicate", opts.SourceAddresses)
		}
	})
}

// TestTTLCutoff_ShouldBeExpressedInUTC is strandedCutoff's counterpart —
// see TestStrandedCutoff_ShouldBeExpressedInUTC in sweep_test.go and
// package dbtime for why the zone matters.
func TestTTLCutoff_ShouldBeExpressedInUTC(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)

	if got := svc.ttlCutoff().Location(); got != time.UTC {
		t.Errorf("ttlCutoff() location = %v, want %v", got, time.UTC)
	}
}
