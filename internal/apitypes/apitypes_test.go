package apitypes

import (
	"slices"
	"testing"
)

// TestTerminalStatuses should return all terminal statuses.
func TestTerminalStatuses(t *testing.T) {
	statuses := TerminalStatuses()

	if len(statuses) != 5 {
		t.Fatalf("TerminalStatuses() returned %d statuses, want 5", len(statuses))
	}

	expectedStatuses := []string{
		StatusApproved,
		StatusDenied,
		StatusExpired,
		StatusEnrolled,
		StatusFailed,
	}

	for _, expected := range expectedStatuses {
		if !slices.Contains(statuses, expected) {
			t.Errorf("TerminalStatuses() missing %q", expected)
		}
	}
}

// TestTerminalStatusesConsistency verifies all terminal statuses are defined constants.
func TestTerminalStatusesConsistency(t *testing.T) {
	t.Run("StatusApproved is defined", func(t *testing.T) {
		if StatusApproved != "approved" {
			t.Errorf("StatusApproved = %q, want %q", StatusApproved, "approved")
		}
	})

	t.Run("StatusDenied is defined", func(t *testing.T) {
		if StatusDenied != "denied" {
			t.Errorf("StatusDenied = %q, want %q", StatusDenied, "denied")
		}
	})

	t.Run("StatusExpired is defined", func(t *testing.T) {
		if StatusExpired != "expired" {
			t.Errorf("StatusExpired = %q, want %q", StatusExpired, "expired")
		}
	})

	t.Run("StatusEnrolled is defined", func(t *testing.T) {
		if StatusEnrolled != "enrolled" {
			t.Errorf("StatusEnrolled = %q, want %q", StatusEnrolled, "enrolled")
		}
	})

	t.Run("StatusFailed is defined", func(t *testing.T) {
		if StatusFailed != "failed" {
			t.Errorf("StatusFailed = %q, want %q", StatusFailed, "failed")
		}
	})
}

// TestTerminalStatusesUnique verifies no duplicate statuses are returned.
func TestTerminalStatusesUnique(t *testing.T) {
	statuses := TerminalStatuses()
	seen := make(map[string]bool)

	for _, status := range statuses {
		if seen[status] {
			t.Errorf("TerminalStatuses() returned duplicate: %q", status)
		}
		seen[status] = true
	}
}

// TestErrorCodeConstants verifies all error code constants are defined.
func TestErrorCodeConstants(t *testing.T) {
	t.Run("ErrorCodeInvalidRequest", func(t *testing.T) {
		if ErrorCodeInvalidRequest != "invalid_request" {
			t.Errorf("ErrorCodeInvalidRequest = %q, want %q", ErrorCodeInvalidRequest, "invalid_request")
		}
	})

	t.Run("ErrorCodeUnauthenticated", func(t *testing.T) {
		if ErrorCodeUnauthenticated != "unauthenticated" {
			t.Errorf("ErrorCodeUnauthenticated = %q, want %q", ErrorCodeUnauthenticated, "unauthenticated")
		}
	})

	t.Run("ErrorCodeForbidden", func(t *testing.T) {
		if ErrorCodeForbidden != "forbidden" {
			t.Errorf("ErrorCodeForbidden = %q, want %q", ErrorCodeForbidden, "forbidden")
		}
	})

	t.Run("ErrorCodeNotFound", func(t *testing.T) {
		if ErrorCodeNotFound != "not_found" {
			t.Errorf("ErrorCodeNotFound = %q, want %q", ErrorCodeNotFound, "not_found")
		}
	})

	t.Run("ErrorCodeUnavailable", func(t *testing.T) {
		if ErrorCodeUnavailable != "unavailable" {
			t.Errorf("ErrorCodeUnavailable = %q, want %q", ErrorCodeUnavailable, "unavailable")
		}
	})

	t.Run("ErrorCodeRateLimited", func(t *testing.T) {
		if ErrorCodeRateLimited != "rate_limited" {
			t.Errorf("ErrorCodeRateLimited = %q, want %q", ErrorCodeRateLimited, "rate_limited")
		}
	})

	t.Run("ErrorCodeNotImplemented", func(t *testing.T) {
		if ErrorCodeNotImplemented != "not_implemented" {
			t.Errorf("ErrorCodeNotImplemented = %q, want %q", ErrorCodeNotImplemented, "not_implemented")
		}
	})

	t.Run("ErrorCodeInternalError", func(t *testing.T) {
		if ErrorCodeInternalError != "internal_error" {
			t.Errorf("ErrorCodeInternalError = %q, want %q", ErrorCodeInternalError, "internal_error")
		}
	})
}

// TestRequestedOptionsStructure verifies RequestedOptions construction.
func TestRequestedOptionsStructure(t *testing.T) {
	t.Run("should construct with all fields", func(t *testing.T) {
		opts := RequestedOptions{
			Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
			ForceCommand:    "/usr/bin/restricted",
			SourceAddresses: []string{"192.168.1.100", "10.0.0.50"},
			NoTouchRequired: true,
		}

		if len(opts.Extensions) != 2 {
			t.Errorf("Extensions count = %d, want 2", len(opts.Extensions))
		}
		if opts.ForceCommand != "/usr/bin/restricted" {
			t.Errorf("ForceCommand mismatch")
		}
		if !opts.NoTouchRequired {
			t.Error("NoTouchRequired should be true")
		}
	})

	t.Run("should allow empty RequestedOptions", func(t *testing.T) {
		opts := RequestedOptions{}

		if opts.ForceCommand != "" {
			t.Errorf("ForceCommand should be empty")
		}
		if opts.NoTouchRequired {
			t.Error("NoTouchRequired should be false")
		}
	})
}

// TestCertificateResultStructure verifies CertificateResult construction.
func TestCertificateResultStructure(t *testing.T) {
	t.Run("should construct approved result with certificate", func(t *testing.T) {
		result := CertificateResult{
			Status:      StatusApproved,
			Certificate: "ssh-cert-v01@openssh.com AAAAgnc...",
		}

		if result.Status != StatusApproved {
			t.Errorf("Status mismatch")
		}
		if result.Certificate == "" {
			t.Error("Certificate should not be empty")
		}
	})

	t.Run("should construct enrolled result with code", func(t *testing.T) {
		result := CertificateResult{
			Status: StatusEnrolled,
			Code:   "enrollment-token-123",
		}

		if result.Status != StatusEnrolled {
			t.Errorf("Status mismatch")
		}
		if result.Code != "enrollment-token-123" {
			t.Errorf("Code mismatch")
		}
	})

	t.Run("should construct denied result", func(t *testing.T) {
		result := CertificateResult{
			Status: StatusDenied,
		}

		if result.Status != StatusDenied {
			t.Errorf("Status mismatch")
		}
		if result.Certificate != "" || result.Code != "" {
			t.Error("Certificate and Code should be empty on denial")
		}
	})

	t.Run("should construct expired result", func(t *testing.T) {
		result := CertificateResult{
			Status: StatusExpired,
		}

		if result.Status != StatusExpired {
			t.Errorf("Status mismatch")
		}
	})

	t.Run("should construct failed result", func(t *testing.T) {
		result := CertificateResult{
			Status: StatusFailed,
		}

		if result.Status != StatusFailed {
			t.Errorf("Status mismatch")
		}
	})
}

// TestCAResponseStructure verifies CAResponse construction.
func TestCAResponseStructure(t *testing.T) {
	t.Run("should construct CA response", func(t *testing.T) {
		caKey := "ssh-ed25519-cert-v01@openssh.com AAAAC3..."
		resp := CAResponse{
			CA: caKey,
		}

		if resp.CA != caKey {
			t.Errorf("CA mismatch")
		}
	})
}

// TestEnvelopeStructure verifies Envelope construction with generics.
func TestEnvelopeStructure(t *testing.T) {
	t.Run("should construct success envelope with data", func(t *testing.T) {
		type TestData struct {
			Message string
		}

		data := TestData{Message: "success"}
		env := Envelope[TestData]{
			Data: data,
		}

		if env.Data.Message != "success" {
			t.Errorf("Data mismatch")
		}
		if env.Error != "" {
			t.Error("Error should be empty on success")
		}
		if env.ErrorCode != "" {
			t.Error("ErrorCode should be empty on success")
		}
	})

	t.Run("should construct error envelope", func(t *testing.T) {
		type TestData struct {
			Message string
		}

		env := Envelope[TestData]{
			Error:     "request validation failed",
			ErrorCode: ErrorCodeInvalidRequest,
		}

		if env.Error != "request validation failed" {
			t.Errorf("Error mismatch")
		}
		if env.ErrorCode != ErrorCodeInvalidRequest {
			t.Errorf("ErrorCode mismatch")
		}
	})

	t.Run("should construct CA envelope", func(t *testing.T) {
		caKey := "ssh-ed25519-cert-v01@openssh.com AAAAC3..."
		env := Envelope[CAResponse]{
			Data: CAResponse{CA: caKey},
		}

		if env.Data.CA != caKey {
			t.Errorf("CA mismatch")
		}
	})

	t.Run("should construct certificate result envelope", func(t *testing.T) {
		env := Envelope[CertificateResult]{
			Data: CertificateResult{
				Status:      StatusApproved,
				Certificate: "ssh-cert-v01@openssh.com...",
			},
		}

		if env.Data.Status != StatusApproved {
			t.Errorf("Status mismatch")
		}
	})
}
