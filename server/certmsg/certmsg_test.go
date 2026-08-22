package certmsg

import (
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

func TestWaitTopic(t *testing.T) {
	tests := []struct {
		name      string
		requestID string
		want      string
	}{
		{
			name:      "should return topic with request ID",
			requestID: "test-123",
			want:      "certrequest.wait.test-123",
		},
		{
			name:      "should handle empty request ID",
			requestID: "",
			want:      "certrequest.wait.",
		},
		{
			name:      "should handle special characters in request ID",
			requestID: "req-with-dashes_and_underscores",
			want:      "certrequest.wait.req-with-dashes_and_underscores",
		},
		{
			name:      "should handle UUID format",
			requestID: "550e8400-e29b-41d4-a716-446655440000",
			want:      "certrequest.wait.550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WaitTopic(tt.requestID)
			if got != tt.want {
				t.Errorf("WaitTopic(%q) = %q, want %q", tt.requestID, got, tt.want)
			}
		})
	}
}

func TestSignedReplyFailed(t *testing.T) {
	tests := []struct {
		name string
		r    SignedReply
		want bool
	}{
		{
			name: "should return true when ErrorCode is set",
			r: SignedReply{
				RequestID: "req-123",
				ErrorCode: ErrCodeBadPublicKey,
				Error:     "failed to parse public key",
			},
			want: true,
		},
		{
			name: "should return false when ErrorCode is empty",
			r: SignedReply{
				RequestID:   "req-123",
				Certificate: "ssh-cert-v01@openssh.com ...",
			},
			want: false,
		},
		{
			name: "should return true for any non-empty ErrorCode",
			r: SignedReply{
				ErrorCode: ErrCodeCAUnavailable,
			},
			want: true,
		},
		{
			name: "should return true for ErrCodeSignFailed",
			r: SignedReply{
				ErrorCode: ErrCodeSignFailed,
				Error:     "signing operation failed",
			},
			want: true,
		},
		{
			name: "should return true for ErrCodeFIPSNotApproved",
			r: SignedReply{
				ErrorCode: ErrCodeFIPSNotApproved,
				Error:     "algorithm not FIPS approved",
			},
			want: true,
		},
		{
			name: "should return true for ErrCodeUnsupportedType",
			r: SignedReply{
				ErrorCode: ErrCodeUnsupportedType,
				Error:     "certificate type not supported",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.r.Failed()
			if got != tt.want {
				t.Errorf("SignedReply.Failed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequestedOptionsStructure(t *testing.T) {
	t.Run("should construct RequestedOptions with all fields", func(t *testing.T) {
		opts := RequestedOptions{
			Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
			ForceCommand:    "/usr/bin/ssoossh-server",
			SourceAddresses: []string{"192.168.1.100", "10.0.0.50"},
			NoTouchRequired: true,
		}

		if len(opts.Extensions) != 2 {
			t.Errorf("Extensions count = %d, want 2", len(opts.Extensions))
		}
		if opts.ForceCommand != "/usr/bin/ssoossh-server" {
			t.Errorf("ForceCommand = %q, want %q", opts.ForceCommand, "/usr/bin/ssoossh-server")
		}
		if !opts.NoTouchRequired {
			t.Error("NoTouchRequired = false, want true")
		}
	})

	t.Run("should allow empty RequestedOptions", func(t *testing.T) {
		opts := RequestedOptions{}
		if opts.ForceCommand != "" {
			t.Errorf("ForceCommand = %q, want empty", opts.ForceCommand)
		}
		if opts.NoTouchRequired {
			t.Error("NoTouchRequired = true, want false")
		}
	})
}

func TestSigningJobStructure(t *testing.T) {
	t.Run("should construct complete SigningJob", func(t *testing.T) {
		now := time.Now()
		validAfter := now
		validBefore := now.Add(30 * time.Minute)

		job := SigningJob{
			RequestID: "req-456",
			Type:      model.CertificateTypeUser,
			PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5...",
			Principals: []string{"alice@example.com"},
			KeyID:     "laptop-2024-08-22",
			RequestedOptions: RequestedOptions{
				Extensions: []string{"permit-pty"},
			},
			ValidAfter:  validAfter,
			ValidBefore: validBefore,
			Serial:      12345,
		}

		if job.RequestID != "req-456" {
			t.Errorf("RequestID = %q, want %q", job.RequestID, "req-456")
		}
		if job.Type != model.CertificateTypeUser {
			t.Errorf("Type = %v, want %v", job.Type, model.CertificateTypeUser)
		}
		if len(job.Principals) != 1 {
			t.Errorf("Principals count = %d, want 1", len(job.Principals))
		}
		if job.Serial != 12345 {
			t.Errorf("Serial = %d, want 12345", job.Serial)
		}
	})
}

func TestSignedReplyStructure(t *testing.T) {
	t.Run("should construct successful SignedReply", func(t *testing.T) {
		now := time.Now()
		reply := SignedReply{
			RequestID:            "req-789",
			Type:                 model.CertificateTypeUser,
			Certificate:          "ssh-cert-v01@openssh.com ...",
			Serial:               12345,
			KeyID:                "laptop-2024-08-22",
			Principals:           []string{"alice@example.com"},
			PublicKeyFingerprint: "SHA256:...",
			ValidAfter:           now,
			ValidBefore:          now.Add(30 * time.Minute),
		}

		if reply.Certificate != "" && reply.ErrorCode == "" {
			// Just verify structure is intact
			if reply.RequestID != "req-789" {
				t.Errorf("RequestID mismatch")
			}
		}
	})

	t.Run("should construct error SignedReply with ErrorCode", func(t *testing.T) {
		reply := SignedReply{
			RequestID: "req-fail",
			ErrorCode: ErrCodeBadPublicKey,
			Error:     "malformed public key",
		}

		if reply.ErrorCode != ErrCodeBadPublicKey {
			t.Errorf("ErrorCode = %q, want %q", reply.ErrorCode, ErrCodeBadPublicKey)
		}
		if reply.Error != "malformed public key" {
			t.Errorf("Error = %q, want %q", reply.Error, "malformed public key")
		}
	})
}

func TestErrorCodeConstants(t *testing.T) {
	t.Run("should have all error code constants defined", func(t *testing.T) {
		codes := map[string]string{
			"ErrCodeUnsupportedType":   ErrCodeUnsupportedType,
			"ErrCodeBadPublicKey":      ErrCodeBadPublicKey,
			"ErrCodeCAUnavailable":     ErrCodeCAUnavailable,
			"ErrCodeSignFailed":        ErrCodeSignFailed,
			"ErrCodeFIPSNotApproved":   ErrCodeFIPSNotApproved,
		}

		for name, code := range codes {
			if code == "" {
				t.Errorf("%s is empty", name)
			}
		}
	})

	t.Run("should have unique error codes", func(t *testing.T) {
		codes := []string{
			ErrCodeUnsupportedType,
			ErrCodeBadPublicKey,
			ErrCodeCAUnavailable,
			ErrCodeSignFailed,
			ErrCodeFIPSNotApproved,
		}

		seen := make(map[string]bool)
		for _, code := range codes {
			if seen[code] {
				t.Errorf("Duplicate error code: %q", code)
			}
			seen[code] = true
		}
	})
}

func TestTopicConstants(t *testing.T) {
	t.Run("should have sign queue topic defined", func(t *testing.T) {
		if SignQueueTopic == "" {
			t.Error("SignQueueTopic is empty")
		}
		if SignQueueTopic != "certrequest.sign" {
			t.Errorf("SignQueueTopic = %q, want %q", SignQueueTopic, "certrequest.sign")
		}
	})

	t.Run("should have signed topic defined", func(t *testing.T) {
		if SignedTopic == "" {
			t.Error("SignedTopic is empty")
		}
		if SignedTopic != "certrequest.signed" {
			t.Errorf("SignedTopic = %q, want %q", SignedTopic, "certrequest.signed")
		}
	})
}
