package service

// Test methodology: the same in-memory sqlite harness the rest of
// CertRequestService's tests use (see newTestCertRequestService), plus the
// one thing AutoMigrate cannot build — the partial unique index on
// user_code, which is a migration artifact. Tests that depend on
// live-rows-only uniqueness create it explicitly and say so; test/migration
// is what proves the real schema carries it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// consoleOptions returns certificate options with a console budget shorter
// than the global one, which is the configuration the type exists to have.
func consoleOptions(global, console time.Duration, networks ...string) config.CertificateOptions {
	return config.CertificateOptions{
		ClientTimeout: global,
		Console: config.CertOptionsConsole{
			ClientTimeout:   console,
			ValidDuration:   30 * time.Second,
			AllowedNetworks: networks,
		},
	}
}

// addUserCodeIndex builds the partial unique index the console migration
// adds. AutoMigrate works from the model's struct tags, and a GORM tag
// cannot express a WHERE clause, so a test that needs live-rows-only
// uniqueness has to create it.
func addUserCodeIndex(t *testing.T, db *gorm.DB) {
	t.Helper()

	err := db.Exec(`CREATE UNIQUE INDEX idx_certificate_requests_user_code
		ON certificate_requests(user_code)
		WHERE user_code <> '' AND status IN ('pending', 'signing')`).Error
	if err != nil {
		t.Fatalf("failed to create the user_code index: %v", err)
	}
}

// newConsoleRequest creates one console request and returns it, failing the
// test rather than making every caller handle the error.
func newConsoleRequest(t *testing.T, svc *CertRequestService, p NewCertRequestParams) CreatedRequest {
	t.Helper()

	p.Type = model.CertificateTypeConsole
	if p.PublicKey == "" {
		p.PublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestConsoleKey console@web01"
	}
	if p.Username == "" {
		p.Username = "alice"
	}
	created, err := svc.CreateRequest(context.Background(), p)
	if err != nil {
		t.Fatalf("failed to create a console request: %v", err)
	}
	return created
}

func TestCreateRequest_ShouldMintAUserCodeForAConsoleRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	if len(created.UserCode) != userCodeLength {
		t.Fatalf("user code %q is %d characters, want %d", created.UserCode, len(created.UserCode), userCodeLength)
	}
	if strings.Contains(created.UserCode, "-") {
		t.Errorf("user code %q carries a separator; the stored form is normalized", created.UserCode)
	}

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	if stored.UserCode != created.UserCode {
		t.Errorf("stored user code %q does not match the returned one %q", stored.UserCode, created.UserCode)
	}
}

// Every other type has to keep returning exactly what it returned before,
// which is what makes the wire change additive.
func TestCreateRequest_ShouldNotMintAUserCodeForOtherTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		certType model.CertificateType
	}{
		{name: "user", certType: model.CertificateTypeUser},
		{name: "service", certType: model.CertificateTypeService},
		{name: "pam", certType: model.CertificateTypePAM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
			created, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
				Type:      tt.certType,
				PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey test@host",
				Username:  "alice",
			})
			if err != nil {
				t.Fatalf("unexpected error creating a %s request: %v", tt.certType, err)
			}
			if created.UserCode != "" {
				t.Errorf("a %s request minted user code %q, want none", tt.certType, created.UserCode)
			}
		})
	}
}

// ExpiresAt is what a client bounds its own wait by, so it has to come from
// the type's budget rather than the global one.
func TestCreateRequest_ShouldReportTheTypesOwnDeadline(t *testing.T) {
	t.Parallel()

	const global = 5 * time.Minute
	const console = 2 * time.Minute

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(global, console))

	consoleCreated := newConsoleRequest(t, svc, NewCertRequestParams{})
	userCreated, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey test@host",
	})
	if err != nil {
		t.Fatalf("unexpected error creating a user request: %v", err)
	}

	consoleWindow := time.Until(consoleCreated.ExpiresAt)
	userWindow := time.Until(userCreated.ExpiresAt)

	// The human's share is budget - 2*(budget/10): 96s of a 2m console
	// budget, 240s of a 5m global one.
	if want := config.ApprovalTTLFor(console); consoleWindow > want || consoleWindow < want-time.Minute {
		t.Errorf("console request expires in %s, want about %s", consoleWindow, want)
	}
	if want := config.ApprovalTTLFor(global); userWindow > want || userWindow < want-time.Minute {
		t.Errorf("user request expires in %s, want about %s", userWindow, want)
	}
	if consoleWindow >= userWindow {
		t.Errorf("console window %s is not shorter than the global one %s", consoleWindow, userWindow)
	}
}

// Unset inherits the global, so a deployment that never configures the type
// still gets a coherent deadline rather than a zero one.
func TestCreateRequest_ShouldInheritTheGlobalBudgetWhenTheTypeSetsNone(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 0))
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	want := config.ApprovalTTLFor(5 * time.Minute)
	if got := time.Until(created.ExpiresAt); got > want || got < want-time.Minute {
		t.Errorf("console request expires in %s, want about %s", got, want)
	}
}

func TestCreateRequest_ShouldGateConsoleRequestsOnTheSourceNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		networks []string
		sourceIP string
		wantErr  bool
	}{
		{name: "no gate permits anything", networks: nil, sourceIP: "203.0.113.9"},
		{name: "an address inside the gate is permitted", networks: []string{"10.20.0.0/16"}, sourceIP: "10.20.3.4"},
		{name: "an address in the second network is permitted", networks: []string{"10.20.0.0/16", "192.168.50.0/24"}, sourceIP: "192.168.50.7"},
		{name: "an IPv4-mapped IPv6 address matches its IPv4 network", networks: []string{"10.20.0.0/16"}, sourceIP: "::ffff:10.20.3.4"},
		{name: "an IPv6 address inside an IPv6 network is permitted", networks: []string{"2001:db8::/32"}, sourceIP: "2001:db8::1"},
		{name: "an address outside the gate is refused", networks: []string{"10.20.0.0/16"}, sourceIP: "203.0.113.9", wantErr: true},
		{name: "an unparseable address is refused when a gate is set", networks: []string{"10.20.0.0/16"}, sourceIP: "not-an-address", wantErr: true},
		{name: "an absent address is refused when a gate is set", networks: []string{"10.20.0.0/16"}, sourceIP: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute, tt.networks...))
			_, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
				Type:      model.CertificateTypeConsole,
				PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey console@web01",
				Username:  "alice",
				SourceIP:  tt.sourceIP,
			})

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			var forbidden *errorresponses.ForbiddenError
			if !errors.As(err, &forbidden) {
				t.Fatalf("got error %v, want a ForbiddenError", err)
			}
		})
	}
}

// The gate refuses before anything is written, which is what "fails before
// a certificate is minted" means in practice: no row for a human to find,
// nothing to approve.
func TestCreateRequest_ShouldNotPersistARequestTheNetworkGateRefuses(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute, "10.20.0.0/16"))
	if _, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeConsole,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey console@web01",
		Username:  "alice",
		SourceIP:  "203.0.113.9",
	}); err == nil {
		t.Fatal("expected the network gate to refuse the request")
	}

	var count int64
	if err := svc.db.Model(&model.CertificateRequest{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count requests: %v", err)
	}
	if count != 0 {
		t.Errorf("%d request rows were written, want none", count)
	}
}

// The gate is per type: nothing about a network restriction on console
// logins should reach a `sudo` or an SSH login.
func TestCreateRequest_ShouldNotApplyTheConsoleNetworkGateToOtherTypes(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute, "10.20.0.0/16"))
	if _, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey sudo@web01",
		Username:  "alice",
		SourceIP:  "203.0.113.9",
	}); err != nil {
		t.Errorf("a PAM request was refused by the console network gate: %v", err)
	}
}

func TestCreateRequest_ShouldStoreTheSelfReportedConsoleContext(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	created := newConsoleRequest(t, svc, NewCertRequestParams{
		Hostname:   "web01",
		PAMService: "login",
		TTY:        "tty1",
		RemoteHost: "198.51.100.7",
	})

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}

	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{name: "hostname", got: stored.Hostname, want: "web01"},
		{name: "pam_service", got: stored.PAMService, want: "login"},
		{name: "tty", got: stored.TTY, want: "tty1"},
		{name: "remote_host", got: stored.RemoteHost, want: "198.51.100.7"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
}

// The context fields are chosen by an unauthenticated caller, so their
// length is bounded on the way in rather than at each place that renders
// them.
func TestCreateRequest_ShouldBoundTheSelfReportedConsoleContext(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	long := strings.Repeat("h", maxContextFieldLen*3)
	created := newConsoleRequest(t, svc, NewCertRequestParams{
		Hostname:   long,
		PAMService: long,
		TTY:        long,
		RemoteHost: long,
	})

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	for _, field := range []struct {
		name string
		got  string
	}{
		{name: "hostname", got: stored.Hostname},
		{name: "pam_service", got: stored.PAMService},
		{name: "tty", got: stored.TTY},
		{name: "remote_host", got: stored.RemoteHost},
	} {
		if len(field.got) != maxContextFieldLen {
			t.Errorf("%s is %d bytes, want it truncated to %d", field.name, len(field.got), maxContextFieldLen)
		}
	}
}

// The retry is the reason the unique index is safe to rely on: without it a
// collision reaches a console as a server error, which is indistinguishable
// from ssoosshd being down.
func TestCreateRequest_ShouldRerollAUserCodeThatCollidesWithALiveRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	addUserCodeIndex(t, svc.db)

	// Hand out the same code twice, then a distinct one: the first request
	// takes it, the second collides once and succeeds on the re-roll.
	codes := []string{"K7M4QP2X", "K7M4QP2X", "A1B2C3D4"}
	var handed int
	svc.mintUserCode = func() (string, error) {
		code := codes[handed]
		handed++
		return code, nil
	}

	first := newConsoleRequest(t, svc, NewCertRequestParams{})
	second := newConsoleRequest(t, svc, NewCertRequestParams{})

	if first.UserCode != "K7M4QP2X" {
		t.Errorf("first code = %q, want K7M4QP2X", first.UserCode)
	}
	if second.UserCode != "A1B2C3D4" {
		t.Errorf("second code = %q, want the re-rolled A1B2C3D4", second.UserCode)
	}
	if handed != len(codes) {
		t.Errorf("minted %d codes, want %d (one collision, one re-roll)", handed, len(codes))
	}
}

// A generator that never produces a free code has to fail the request
// rather than loop, and the error has to name what happened.
func TestCreateRequest_ShouldGiveUpAfterRepeatedUserCodeCollisions(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	addUserCodeIndex(t, svc.db)

	svc.mintUserCode = func() (string, error) { return "K7M4QP2X", nil }
	newConsoleRequest(t, svc, NewCertRequestParams{})

	_, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeConsole,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey console@web01",
		Username:  "alice",
	})
	if err == nil {
		t.Fatal("expected the second request to fail after exhausting its attempts")
	}
	if !strings.Contains(err.Error(), "user code") {
		t.Errorf("error %q does not mention the user code", err)
	}
}

// Uniqueness is over live rows only, so a code freed by a resolved request
// can be minted again — which is what keeps a long-lived deployment from
// filling the space.
func TestUserCodeIndex_ShouldAllowReuseOnceTheRequestIsResolved(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	addUserCodeIndex(t, svc.db)

	svc.mintUserCode = func() (string, error) { return "K7M4QP2X", nil }
	first := newConsoleRequest(t, svc, NewCertRequestParams{})

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", first.ID).
		Update("status", model.CertificateRequestStatusDenied).Error; err != nil {
		t.Fatalf("failed to resolve the first request: %v", err)
	}

	second := newConsoleRequest(t, svc, NewCertRequestParams{})
	if second.UserCode != "K7M4QP2X" {
		t.Errorf("second code = %q, want the freed K7M4QP2X", second.UserCode)
	}
}

func TestResolveUserCode_ShouldReturnTheRequestAndClaimIt(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	userID := seedUser(t, svc.db, "sub-approver")
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	got, err := svc.ResolveUserCode(context.Background(), FormatUserCode(created.UserCode), &Identity{
		Subject:  "sub-approver",
		Username: "approver",
	})
	if err != nil {
		t.Fatalf("unexpected error resolving the code: %v", err)
	}
	if got != created.ID {
		t.Errorf("resolved to %q, want %q", got, created.ID)
	}

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	if stored.UserID == nil || *stored.UserID != userID {
		t.Errorf("request is bound to %v, want %q — resolving a code has to claim it", stored.UserID, userID)
	}
}

// Everything a human plausibly types has to reach the same request. This is
// the same normalization consolecode_test.go pins, checked end to end so a
// caller cannot bypass it.
func TestResolveUserCode_ShouldAcceptTheCodeHoweverItIsTyped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rewrite func(code string) string
	}{
		{name: "as stored", rewrite: func(c string) string { return c }},
		{name: "grouped for display", rewrite: FormatUserCode},
		{name: "lower case", rewrite: strings.ToLower},
		{name: "with surrounding space", rewrite: func(c string) string { return "  " + FormatUserCode(c) + " " }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
			seedUser(t, svc.db, "sub-approver")
			created := newConsoleRequest(t, svc, NewCertRequestParams{})

			got, err := svc.ResolveUserCode(context.Background(), tt.rewrite(created.UserCode), &Identity{
				Subject:  "sub-approver",
				Username: "approver",
			})
			if err != nil {
				t.Fatalf("unexpected error resolving %q: %v", tt.rewrite(created.UserCode), err)
			}
			if got != created.ID {
				t.Errorf("resolved to %q, want %q", got, created.ID)
			}
		})
	}
}

func TestResolveUserCode_ShouldRejectAMalformedCode(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	seedUser(t, svc.db, "sub-approver")

	_, err := svc.ResolveUserCode(context.Background(), "nope", &Identity{Subject: "sub-approver"})

	var invalid *errorresponses.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("got error %v, want an InvalidRequestError", err)
	}
}

func TestResolveUserCode_ShouldReportNotFoundForACodeNoLiveRequestCarries(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	seedUser(t, svc.db, "sub-approver")

	_, err := svc.ResolveUserCode(context.Background(), "K7M4QP2X", &Identity{Subject: "sub-approver"})

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("got error %v, want a NotFoundError", err)
	}
}

// A resolved request is not reachable by its code any more, which is also
// what makes the live-rows-only index safe.
func TestResolveUserCode_ShouldNotResolveATerminalRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status model.CertificateRequestStatus
	}{
		{name: "approved", status: model.CertificateRequestStatusApproved},
		{name: "denied", status: model.CertificateRequestStatusDenied},
		{name: "expired", status: model.CertificateRequestStatusExpired},
		{name: "failed", status: model.CertificateRequestStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
			seedUser(t, svc.db, "sub-approver")
			created := newConsoleRequest(t, svc, NewCertRequestParams{})

			if err := svc.db.Model(&model.CertificateRequest{}).
				Where("id = ?", created.ID).
				Update("status", tt.status).Error; err != nil {
				t.Fatalf("failed to move the request to %s: %v", tt.status, err)
			}

			_, err := svc.ResolveUserCode(context.Background(), created.UserCode, &Identity{Subject: "sub-approver"})

			var notFound *errorresponses.NotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("got error %v, want a NotFoundError", err)
			}
		})
	}
}

// Expired is its own answer because it sends the user somewhere different:
// back to the machine, not back to the keyboard.
func TestResolveUserCode_ShouldReportAnExpiredRequestAsGone(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	seedUser(t, svc.db, "sub-approver")
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	// Age the row past the console budget's approval window, which is 96s
	// of the 2m configured above.
	aged := time.Now().Add(-10 * time.Minute).UTC()
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", created.ID).
		Update("created_at", aged).Error; err != nil {
		t.Fatalf("failed to age the request: %v", err)
	}

	_, err := svc.ResolveUserCode(context.Background(), created.UserCode, &Identity{Subject: "sub-approver"})

	var expired *errorresponses.ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("got error %v, want an ExpiredError", err)
	}

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	if stored.Status != model.CertificateRequestStatusExpired {
		t.Errorf("status = %s, want %s — resolving past the deadline should also record it", stored.Status, model.CertificateRequestStatusExpired)
	}
}

// One code, one request, one shot: a second session submitting the same
// code is refused the same way a second browser opening /approve/<id> is.
func TestResolveUserCode_ShouldRefuseASecondIdentity(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	seedUser(t, svc.db, "sub-first")
	seedUser(t, svc.db, "sub-second")
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	if _, err := svc.ResolveUserCode(context.Background(), created.UserCode, &Identity{Subject: "sub-first"}); err != nil {
		t.Fatalf("the first resolve failed: %v", err)
	}

	_, err := svc.ResolveUserCode(context.Background(), created.UserCode, &Identity{Subject: "sub-second"})

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("got error %v, want a ForbiddenError", err)
	}
}

// The same session resubmitting — a double-click, a refresh — must not be
// refused: it already owns the request.
func TestResolveUserCode_ShouldBeRepeatableByTheClaimingIdentity(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	seedUser(t, svc.db, "sub-approver")
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	identity := &Identity{Subject: "sub-approver"}
	if _, err := svc.ResolveUserCode(context.Background(), created.UserCode, identity); err != nil {
		t.Fatalf("the first resolve failed: %v", err)
	}
	got, err := svc.ResolveUserCode(context.Background(), created.UserCode, identity)
	if err != nil {
		t.Fatalf("the second resolve by the same identity failed: %v", err)
	}
	if got != created.ID {
		t.Errorf("resolved to %q, want %q", got, created.ID)
	}
}

// A session whose users row is gone cannot claim anything, so it cannot
// resolve a code either.
func TestResolveUserCode_ShouldRefuseAnIdentityWithNoUserRecord(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	_, err := svc.ResolveUserCode(context.Background(), created.UserCode, &Identity{Subject: "sub-nobody"})

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("got error %v, want a ForbiddenError", err)
	}
}

// Approval is bounded by the type's own budget, not the global one: a
// console request that a user request would still accept is refused.
func TestApprove_ShouldExpireAConsoleRequestOnItsOwnBudget(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(time.Hour, 2*time.Minute))
	seedUser(t, svc.db, "sub-approver")
	created := newConsoleRequest(t, svc, NewCertRequestParams{})

	// Five minutes old: inside the hour-long global window, well past the
	// console type's 96-second one.
	aged := time.Now().Add(-5 * time.Minute).UTC()
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", created.ID).
		Update("created_at", aged).Error; err != nil {
		t.Fatalf("failed to age the request: %v", err)
	}

	err := svc.Approve(context.Background(), created.ID, &Identity{Subject: "sub-approver", Username: "approver"}, DecisionContext{}, ApprovalSelection{})
	if err == nil {
		t.Fatal("expected the approval to be refused for a request past the console budget")
	}

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	if stored.Status != model.CertificateRequestStatusExpired {
		t.Errorf("status = %s, want %s", stored.Status, model.CertificateRequestStatusExpired)
	}
}

// The mirror of the test above: the same age on a type that uses the global
// budget is still approvable, so the narrowing is the console type's and
// not a change to everything.
func TestApprove_ShouldNotExpireAnotherTypeOnTheConsoleBudget(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(time.Hour, 2*time.Minute))
	seedUser(t, svc.db, "sub-approver")

	created, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey sudo@web01",
		Username:  "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error creating a PAM request: %v", err)
	}

	aged := time.Now().Add(-5 * time.Minute).UTC()
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", created.ID).
		Update("created_at", aged).Error; err != nil {
		t.Fatalf("failed to age the request: %v", err)
	}

	// The approval itself needs a signing pipeline this harness does not
	// wire up, so the assertion is on the row: whatever else happens, it
	// must not have been expired.
	_ = svc.Approve(context.Background(), created.ID, &Identity{Subject: "sub-approver", Username: "approver"}, DecisionContext{}, ApprovalSelection{})

	var stored model.CertificateRequest
	if err := svc.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	if stored.Status == model.CertificateRequestStatusExpired {
		t.Error("a PAM request was expired on the console type's budget")
	}
}

func TestIsUniqueConstraintViolation_ShouldRecogniseEachDialectsRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "gorm's translated error", err: gorm.ErrDuplicatedKey, want: true},
		{name: "sqlite", err: errors.New("constraint failed: UNIQUE constraint failed: certificate_requests.user_code (2067)"), want: true},
		{name: "postgres", err: errors.New(`ERROR: duplicate key value violates unique constraint "idx_certificate_requests_user_code" (SQLSTATE 23505)`), want: true},
		{name: "a wrapped duplicate", err: errors.New("failed to persist: UNIQUE constraint failed"), want: true},
		{name: "an unrelated database error", err: errors.New("sql: database is closed"), want: false},
		{name: "a foreign key rejection", err: errors.New("FOREIGN KEY constraint failed"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isUniqueConstraintViolation(tt.err); got != tt.want {
				t.Errorf("isUniqueConstraintViolation(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
