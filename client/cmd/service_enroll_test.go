package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/client/config"
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

// should decide between generating and enrolling from what is on disk, and
// name the missing file when only half a keypair is there.
func TestResolveServiceKey(t *testing.T) {
	t.Parallel()

	const existingPub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexisting service@host\n"

	tests := []struct {
		name        string
		writePriv   bool
		writePub    bool
		wantPublic  string
		wantErrPath string
	}{
		{
			name:      "should enroll the existing public key when both halves are present",
			writePriv: true, writePub: true,
			wantPublic: existingPub,
		},
		{
			name:        "should refuse when only the public key is present",
			writePub:    true,
			wantErrPath: "service_key",
		},
		{
			name:        "should refuse when only the private key is present",
			writePriv:   true,
			wantErrPath: "service_key.pub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			keyPath := filepath.Join(t.TempDir(), "service_key")
			if tt.writePriv {
				if err := os.WriteFile(keyPath, []byte("private"), 0600); err != nil {
					t.Fatalf("write private key: %v", err)
				}
			}
			if tt.writePub {
				if err := os.WriteFile(keyPath+".pub", []byte(existingPub), 0600); err != nil {
					t.Fatalf("write public key: %v", err)
				}
			}

			got, err := resolveServiceKey(&config.Config{}, keyPath)

			if tt.wantErrPath != "" {
				if err == nil {
					t.Fatalf("expected an error naming %s, got none", tt.wantErrPath)
				}
				if !strings.Contains(err.Error(), tt.wantErrPath) {
					t.Errorf("error %q does not name the missing file %s", err, tt.wantErrPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantPublic {
				t.Errorf("resolveServiceKey() = %q, want %q", got, tt.wantPublic)
			}
		})
	}
}

// should generate a usable keypair when neither half exists, leaving both
// files behind for ssh to find. Separate from the table above because it is
// the one case that writes rather than reads, and it needs a real key
// configuration to generate against.
func TestResolveServiceKey_ShouldGenerateWhenNeitherHalfExists(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "service_key")

	got, err := resolveServiceKey(&config.Config{}, keyPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("expected the generated public key to be returned")
	}
	for _, path := range []string{keyPath, keyPath + ".pub"} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist after generating: %v", path, err)
		}
	}
}

func TestKeyPathDerivation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantPub string
		wantCrt string
	}{
		{name: "should derive from a private key path", path: "/etc/ssoossh/service_key", wantPub: "/etc/ssoossh/service_key.pub", wantCrt: "/etc/ssoossh/service_key-cert.pub"},
		{name: "should not double the suffix for a public key path", path: "/etc/ssoossh/service_key.pub", wantPub: "/etc/ssoossh/service_key.pub.pub", wantCrt: "/etc/ssoossh/service_key-cert.pub"},
		{name: "should handle a bare relative name", path: "id_ed25519", wantPub: "id_ed25519.pub", wantCrt: "id_ed25519-cert.pub"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := publicKeyPathFor(tt.path); got != tt.wantPub {
				t.Errorf("publicKeyPathFor(%q) = %q, want %q", tt.path, got, tt.wantPub)
			}
			if got := certificatePathFor(tt.path); got != tt.wantCrt {
				t.Errorf("certificatePathFor(%q) = %q, want %q", tt.path, got, tt.wantCrt)
			}
		})
	}
}

// should write the private key 0600 and the public key beside it, and hand
// back the same public key that gets enrolled.
func TestGenerateServiceKeypair(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "service_key")
	cfg := &config.Config{}

	publicKey, err := generateServiceKeypair(cfg, keyPath)
	if err != nil {
		t.Fatalf("generateServiceKeypair() error = %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("private key mode = %o, want 0600", got)
	}

	pubData, err := os.ReadFile(publicKeyPathFor(keyPath))
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	if string(pubData) != publicKey {
		t.Errorf("public key file = %q, want the enrolled key %q", pubData, publicKey)
	}
}

// should refuse to clobber an existing key — overwriting a private key
// destroys every certificate that depends on it.
func TestGenerateServiceKeypair_ShouldRefuseToOverwrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing func(dir string) string
	}{
		{name: "should refuse when the private key exists", existing: func(dir string) string { return filepath.Join(dir, "service_key") }},
		{name: "should refuse when only the public key exists", existing: func(dir string) string { return filepath.Join(dir, "service_key.pub") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(tt.existing(dir), []byte("existing"), 0600); err != nil {
				t.Fatalf("seed existing file: %v", err)
			}

			_, err := generateServiceKeypair(&config.Config{}, filepath.Join(dir, "service_key"))
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("generateServiceKeypair() error = %v, want an already-exists refusal", err)
			}
		})
	}
}

// should format the file paths with absolute paths when possible.
func TestPrintEnrollmentCodeAndPaths(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printEnrollmentCodeAndPaths(&out, "id_ed25519")

	got := out.String()
	wantContains := []string{
		"ssh key files are:",
		"Private key:",
		"Public key:",
		"Certificate:",
		"id_ed25519",
		"id_ed25519.pub",
		"id_ed25519-cert.pub",
	}

	for _, want := range wantContains {
		if !strings.Contains(got, want) {
			t.Errorf("printEnrollmentCodeAndPaths() output missing %q, got:\n%s", want, got)
		}
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
