package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/internal/api"
)

// should hand back the code only for the enrolled outcome — `service
// enroll` resolves as an enrollment, never a certificate, so ssh login's
// approved-shaped check would fail its happy path.
func TestEnrollmentCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		result   *api.CertificateResult
		wantCode string
		wantErr  string
	}{
		{name: "should return the code when enrolled", result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-1"}, wantCode: "code-1"},
		{name: "should reject an enrolled outcome without a code", result: &api.CertificateResult{Status: api.StatusEnrolled}, wantErr: "no code was delivered"},
		{name: "should reject a denial", result: &api.CertificateResult{Status: api.StatusDenied}, wantErr: "denied"},
		{name: "should reject an expiry", result: &api.CertificateResult{Status: api.StatusExpired}, wantErr: "expired"},
		{name: "should reject a failure", result: &api.CertificateResult{Status: api.StatusFailed}, wantErr: "could not create"},
		{name: "should reject a missing outcome", result: nil, wantErr: "no outcome"},
		{name: "should reject an unrecognized outcome", result: &api.CertificateResult{Status: "mystery"}, wantErr: "unrecognized"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, err := enrollmentCode(tt.result)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("enrollmentCode() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("enrollmentCode() error = %v", err)
			}
			if code != tt.wantCode {
				t.Errorf("enrollmentCode() = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

// should round-trip the enrollment file with credential-tight permissions.
func TestSaveEnrollment_RoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "enrollment.json")
	saved := ServiceEnrollment{Code: "code-1", PublicKey: "ssh-ed25519 AAAA", PrivateKeyMaterial: "PEM"}

	if err := saveEnrollment(path, saved); err != nil {
		t.Fatalf("saveEnrollment() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat enrollment file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("enrollment file mode = %o, want 0600", got)
	}

	loaded, err := loadEnrollment(path)
	if err != nil {
		t.Fatalf("loadEnrollment() error = %v", err)
	}
	if *loaded != saved {
		t.Errorf("round-trip mismatch: got %+v, want %+v", *loaded, saved)
	}
}

func TestSaveEnrollment_ShouldRequireAConfiguredPath(t *testing.T) {
	t.Parallel()

	if err := saveEnrollment("", ServiceEnrollment{Code: "c"}); err == nil {
		t.Fatal("saveEnrollment() error = nil, want error for empty path")
	}
}

// should replace the destination atomically without a temp file left
// behind, staying inside the destination's own directory — rename cannot
// cross filesystems, and the system temp dir routinely lives on another
// one than /etc or $HOME.
func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("mode = %o, want 0644", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the target in %s, found %d entries", dir, len(entries))
	}
}

func TestWriteFileAtomic_ShouldFailWhenTheDirectoryDoesNotExist(t *testing.T) {
	t.Parallel()

	err := writeFileAtomic(filepath.Join(t.TempDir(), "missing", "target"), []byte("x"), 0644)
	if err == nil {
		t.Fatal("writeFileAtomic() error = nil, want error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("writeFileAtomic() error = %v, want wrapping os.ErrNotExist", err)
	}
}
