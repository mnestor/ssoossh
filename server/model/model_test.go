package model

import (
	"testing"
	"time"
)

// TestCertificateTableName should return the correct GORM table name.
func TestCertificateTableName(t *testing.T) {
	c := Certificate{}
	if got := c.TableName(); got != "certificates" {
		t.Errorf("Certificate.TableName() = %q, want %q", got, "certificates")
	}
}

// TestUserTableName should return the correct GORM table name.
func TestUserTableName(t *testing.T) {
	u := User{}
	if got := u.TableName(); got != "users" {
		t.Errorf("User.TableName() = %q, want %q", got, "users")
	}
}

// TestEnrollmentTableName should return the correct GORM table name.
func TestEnrollmentTableName(t *testing.T) {
	e := Enrollment{}
	if got := e.TableName(); got != "enrollments" {
		t.Errorf("Enrollment.TableName() = %q, want %q", got, "enrollments")
	}
}

// TestCertificateRequestTableName should return the correct GORM table name.
func TestCertificateRequestTableName(t *testing.T) {
	cr := CertificateRequest{}
	if got := cr.TableName(); got != "certificate_requests" {
		t.Errorf("CertificateRequest.TableName() = %q, want %q", got, "certificate_requests")
	}
}

// TestCertificateRequestDecisionTableName should return the correct GORM table name.
func TestCertificateRequestDecisionTableName(t *testing.T) {
	d := CertificateRequestDecision{}
	if got := d.TableName(); got != "certificate_request_decisions" {
		t.Errorf("CertificateRequestDecision.TableName() = %q, want %q", got, "certificate_request_decisions")
	}
}

// TestServerSecretTableName should return the correct GORM table name.
func TestServerSecretTableName(t *testing.T) {
	ss := ServerSecret{}
	if got := ss.TableName(); got != "server_secrets" {
		t.Errorf("ServerSecret.TableName() = %q, want %q", got, "server_secrets")
	}
}

// TestCertificateStructure verifies Certificate model fields.
func TestCertificateStructure(t *testing.T) {
	t.Run("should construct with all fields", func(t *testing.T) {
		now := time.Now()
		cert := Certificate{
			ID:                   "cert-123",
			Type:                 CertificateTypeUser,
			UserID:               ptrString("user-456"),
			CertificateRequestID: ptrString("req-789"),
			PublicKeyFingerprint: "SHA256:abcd...",
			SerialNumber:         123456,
			KeyID:                "laptop-key",
			Principals:           "alice@example.com",
			CriticalOptions:      `{"force-command":"/usr/bin/restricted"}`,
			Extensions:           `["permit-pty","permit-agent-forwarding"]`,
			IssuedAt:             now,
			ExpiresAt:            now.Add(30 * time.Minute),
		}

		if cert.ID != "cert-123" {
			t.Errorf("ID mismatch")
		}
		if cert.Type != CertificateTypeUser {
			t.Errorf("Type mismatch")
		}
	})

}

// TestUserStructure verifies User model fields.
func TestUserStructure(t *testing.T) {
	t.Run("should construct with required fields", func(t *testing.T) {
		now := time.Now()
		user := User{
			ID:              "user-123",
			Subject:         "google-oauth2|abc123",
			Username:        "alice",
			Email:           "alice@example.com",
			OtherAccounts:   `["alice.other@company.com"]`,
			ServiceAccounts: `["svc-alice"]`,
			CreatedAt:       now,
			UpdatedAt:       now,
		}

		if user.ID != "user-123" {
			t.Errorf("ID mismatch")
		}
		if user.Subject != "google-oauth2|abc123" {
			t.Errorf("Subject mismatch")
		}
		if user.Email != "alice@example.com" {
			t.Errorf("Email mismatch")
		}
	})
}

// TestEnrollmentStructure verifies Enrollment model fields.
func TestEnrollmentStructure(t *testing.T) {
	t.Run("should construct enrollment", func(t *testing.T) {
		now := time.Now()
		e := Enrollment{
			ID:        "enroll-123",
			UserID:    "user-456",
			PublicKey: "ssh-sk-ecdsa-sha2-nistp256@openssh.com ...",
			Code:      "enroll-code-abc",
			OptionSet: `{"extensions":["permit-pty"]}`,
			ExpiresAt: now.Add(24 * time.Hour),
			CreatedAt: now,
		}

		if e.ID != "enroll-123" {
			t.Errorf("ID mismatch")
		}
		if e.Code != "enroll-code-abc" {
			t.Error("Code mismatch")
		}
	})

	t.Run("should allow nil RedeemedAt for unredeemed enrollment", func(t *testing.T) {
		e := Enrollment{
			ID:         "enroll-unredeemed",
			Code:       "code-xyz",
			RedeemedAt: nil,
		}

		if e.RedeemedAt != nil {
			t.Error("RedeemedAt should be nil for unredeemed enrollment")
		}
	})
}

// TestCertificateRequestStructure verifies CertificateRequest model fields.
func TestCertificateRequestStructure(t *testing.T) {
	t.Run("should construct user certificate request", func(t *testing.T) {
		now := time.Now()
		cr := CertificateRequest{
			ID:               "req-123",
			Type:             CertificateTypeUser,
			UserID:           ptrString("user-456"),
			PublicKey:        "ssh-ed25519 AAAAC3...",
			RequestedOptions: `{"extensions":["permit-pty"]}`,
			SourceIP:         "192.168.1.100",
			LocalUsername:    "alice",
			LocalHostname:    "macbook-air",
			Status:           CertificateRequestStatusPending,
			CreatedAt:        now,
		}

		if cr.ID != "req-123" {
			t.Errorf("ID mismatch")
		}
		if cr.Type != CertificateTypeUser {
			t.Errorf("Type mismatch")
		}
	})

	t.Run("should construct service enrollment request", func(t *testing.T) {
		cr := CertificateRequest{
			ID:              "req-svc",
			Type:            CertificateTypeService,
			PublicKey:       "ssh-ed25519...",
			ServiceAccount:  "svc-deploy",
			Status:          CertificateRequestStatusEnrolled,
			EnrollmentToken: "token-xyz",
		}

		if cr.ServiceAccount != "svc-deploy" {
			t.Error("ServiceAccount mismatch for service request")
		}
	})
}

// TestCertificateRequest_ReportedIdentity pins which column pair means "who
// and where the request came from" for each type, so no consumer picks a
// pair by hand and reads empty strings for the other family.
func TestCertificateRequest_ReportedIdentity(t *testing.T) {
	t.Parallel()

	req := CertificateRequest{
		Username:      "root",
		Hostname:      "web01",
		LocalUsername: "alice",
		LocalHostname: "alice-laptop",
	}
	tests := []struct {
		name         string
		typ          CertificateType
		wantUsername string
		wantHostname string
	}{
		{name: "should read the PAM columns for a pam request", typ: CertificateTypePAM, wantUsername: "root", wantHostname: "web01"},
		{name: "should read the PAM columns for a console request", typ: CertificateTypeConsole, wantUsername: "root", wantHostname: "web01"},
		{name: "should read the local columns for a user request", typ: CertificateTypeUser, wantUsername: "alice", wantHostname: "alice-laptop"},
		{name: "should read the local columns for a service request", typ: CertificateTypeService, wantUsername: "alice", wantHostname: "alice-laptop"},
		{name: "should read the local columns for an empty type", typ: "", wantUsername: "alice", wantHostname: "alice-laptop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := req
			r.Type = tt.typ
			username, hostname := r.ReportedIdentity()
			if username != tt.wantUsername || hostname != tt.wantHostname {
				t.Errorf("got (%q, %q), want (%q, %q)", username, hostname, tt.wantUsername, tt.wantHostname)
			}
		})
	}
}

// TestCertificateRequestDecisionStructure verifies CertificateRequestDecision model fields.
func TestCertificateRequestDecisionStructure(t *testing.T) {
	t.Run("should construct decision with approval", func(t *testing.T) {
		now := time.Now()
		d := CertificateRequestDecision{
			ID:                   "decision-123",
			CertificateRequestID: "req-456",
			Outcome:              CertificateRequestDecisionApproved,
			Subject:              "google-oauth2|user123",
			Username:             "alice",
			Email:                "alice@example.com",
			Groups:               `["developers"]`,
			SourceIP:             "192.168.1.100",
			UserAgent:            "Mozilla/5.0...",
			DecidedAt:            now,
		}

		if d.ID != "decision-123" {
			t.Errorf("ID mismatch")
		}
		if d.Outcome != CertificateRequestDecisionApproved {
			t.Errorf("Outcome mismatch")
		}
	})

	t.Run("should construct decision with denial", func(t *testing.T) {
		now := time.Now()
		d := CertificateRequestDecision{
			ID:                   "decision-deny",
			CertificateRequestID: "req-789",
			Outcome:              CertificateRequestDecisionDenied,
			Subject:              "google-oauth2|denied-user",
			Username:             "bob",
			Email:                "bob@example.com",
			DecidedAt:            now,
		}

		if d.Outcome != CertificateRequestDecisionDenied {
			t.Errorf("Outcome mismatch")
		}
	})
}

// TestServerSecretStructure verifies ServerSecret model fields.
func TestServerSecretStructure(t *testing.T) {
	t.Run("should construct server secret with byte value", func(t *testing.T) {
		now := time.Now()
		secretValue := []byte("secret-key-material")
		ss := ServerSecret{
			Name:      "encryption_key_v1",
			Value:     secretValue,
			CreatedAt: now,
		}

		if ss.Name != "encryption_key_v1" {
			t.Errorf("Name mismatch")
		}
		if string(ss.Value) != "secret-key-material" {
			t.Errorf("Value mismatch")
		}
	})

	t.Run("should allow session cookie key name constant", func(t *testing.T) {
		ss := ServerSecret{
			Name:  ServerSecretSessionKey,
			Value: []byte("cookie-signing-key"),
		}

		if ss.Name != "session_cookie_key" {
			t.Errorf("Name mismatch: got %q", ss.Name)
		}
	})
}

// Helper function for creating string pointers in tests.
func ptrString(s string) *string {
	return &s
}

// TestNotificationPreferenceTableName should return the correct GORM table
// name.
func TestNotificationPreferenceTableName(t *testing.T) {
	p := NotificationPreference{}
	if got := p.TableName(); got != "notification_preferences" {
		t.Errorf("NotificationPreference.TableName() = %q, want %q", got, "notification_preferences")
	}
}

// The models that arrived with the CA-key and enrollment-audit work. Grouped
// into one table rather than three more near-identical functions: the whole
// contract is "this name matches the migration", and a table makes the next
// model an added line rather than an added function -- which is the shape
// that actually gets remembered.
func TestTableName_ShouldMatchTheMigrationForAuditAndKeyModels(t *testing.T) {
	tests := []struct {
		name  string
		model interface{ TableName() string }
		want  string
	}{
		{name: "CASignerKey", model: CASignerKey{}, want: "ca_signer_keys"},
		{name: "EnrollmentRetrieval", model: EnrollmentRetrieval{}, want: "enrollment_retrievals"},
		{name: "EnrollmentReassignment", model: EnrollmentReassignment{}, want: "enrollment_reassignments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.TableName(); got != tt.want {
				t.Errorf("%s.TableName() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
