package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// should hand back the code only for the enrolled outcome — `service
// enroll` resolves as an enrollment, never a certificate, so ssh login's
// approved-shaped check would fail its happy path.
func TestEnrollmentOutcome(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 9, 23, 14, 5, 0, 0, time.UTC)

	tests := []struct {
		name        string
		result      *api.CertificateResult
		wantCode    string
		wantAccount string
		wantExpiry  time.Time
		wantErr     string
	}{
		{name: "should return the code when enrolled", result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-1"}, wantCode: "code-1"},
		{
			name: "should carry the approved service account and code expiry",
			result: &api.CertificateResult{
				Status:         api.StatusEnrolled,
				Code:           "code-2",
				ServiceAccount: "svc-deploy",
				ExpiresAt:      &expiresAt,
			},
			wantCode:    "code-2",
			wantAccount: "svc-deploy",
			wantExpiry:  expiresAt,
		},
		{
			name:     "should leave the account and expiry unset when the server omits them",
			result:   &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-3"},
			wantCode: "code-3",
		},
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
			enrolled, err := enrollmentOutcome(tt.result)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("enrollmentOutcome() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("enrollmentOutcome() error = %v", err)
			}
			if enrolled.code != tt.wantCode {
				t.Errorf("enrollmentOutcome() code = %q, want %q", enrolled.code, tt.wantCode)
			}
			if enrolled.serviceAccount != tt.wantAccount {
				t.Errorf("enrollmentOutcome() serviceAccount = %q, want %q", enrolled.serviceAccount, tt.wantAccount)
			}
			if !enrolled.expiresAt.Equal(tt.wantExpiry) {
				t.Errorf("enrollmentOutcome() expiresAt = %v, want %v", enrolled.expiresAt, tt.wantExpiry)
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

// runServiceEnroll sat at 7.4% statement coverage: the only path anything
// executed was its `--key is required` guard. Everything after it -- resolving
// the key, creating the enrollment, printing the code, --retrieve, and the
// ssh_config guidance -- was orchestration nothing had ever run, even though
// its helpers were individually well covered. That is the shape where a
// package-level number looks survivable while the part that decides the order
// of events is untested.

// enrollFixture wires a RootCommand around a fakeAPIClient and returns the
// --key path to drive runServiceEnroll with.
func enrollFixture(t *testing.T, apiClient *fakeAPIClient) (*RootCommand, string) {
	t.Helper()
	return &RootCommand{cfg: &config.Config{Server: "https://example.test"}, api: apiClient},
		filepath.Join(t.TempDir(), "svc_key")
}

func TestRunServiceEnroll_ShouldGenerateAKeypairAndPrintTheCode(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"}}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, path := range []string{keyPath, keyPath + ".pub"} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to be generated: %v", path, err)
		}
	}
	if !strings.Contains(out.String(), "code-123") {
		t.Errorf("expected the enrollment code in the output, got:\n%s", out.String())
	}
}

// The guidance is the whole product of the command for someone setting up a
// cron job: the ssh_config recipe, and the three filenames that make it work.
func TestRunServiceEnroll_ShouldPrintTheSshConfigGuidance(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"}}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Match user", "IdentityFile", "IdentitiesOnly yes", "--grace", keyPath} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the guidance to contain %q, got:\n%s", want, got)
		}
	}
}

// The account and expiry are the two facts the operator cannot get any other
// way: the approval happened in a browser they were not looking at, and
// before this the only route to the principal was retrieving a certificate
// and inspecting it.
func TestRunServiceEnroll_ShouldNameTheApprovedAccountAndCodeExpiry(t *testing.T) {
	expiresAt := time.Now().Add(90 * 24 * time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:         api.StatusEnrolled,
		Code:           "code-123",
		ServiceAccount: "svc-deploy",
		ExpiresAt:      &expiresAt,
	}}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	for _, want := range []string{"svc-deploy", expiresAt.Local().Format("2006-01-02 15:04:05 MST"), "89 days"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the output to contain %q, got:\n%s", want, got)
		}
	}
}

// A server older than the two fields sends neither. Printing "approved for
// """ or an expiry of 0001-01-01 would be worse than saying nothing.
func TestRunServiceEnroll_ShouldStaySilentWhenTheServerOmitsTheDetail(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"}}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	for _, unwanted := range []string{"service account", "stops working", "0001-01-01"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("expected no %q in the output, got:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "The code is reusable, so that is safe to run from cron") {
		t.Errorf("expected the undated reusability line, got:\n%s", got)
	}
}

// The recipe is meant to be pasted, not edited. The service account is the
// certificate's sole principal, so it is also the only user the Match block
// can usefully key on.
func TestRunServiceEnroll_ShouldMatchOnTheApprovedServiceAccount(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:         api.StatusEnrolled,
		Code:           "code-123",
		ServiceAccount: "svc-deploy",
	}}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Match user svc-deploy exec") {
		t.Errorf("expected the recipe to match on svc-deploy, got:\n%s", got)
	}
	if strings.Contains(got, "Match user USERNAME") {
		t.Errorf("expected the placeholder to be gone, got:\n%s", got)
	}
}

// A server older than the service_account field leaves nothing to
// substitute, and there is no second source for the principal here. The
// placeholder is what tells the operator to fill it in themselves.
func TestRunServiceEnroll_ShouldKeepThePlaceholderWhenNoAccountIsReported(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"}}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "Match user USERNAME exec") {
		t.Errorf("expected the USERNAME placeholder, got:\n%s", out.String())
	}
}

func TestApproximateDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "should say so when the code is already dead", d: -time.Second, want: "already expired"},
		{name: "should report seconds under two minutes", d: 90 * time.Second, want: "90 seconds"},
		{name: "should report minutes under two hours", d: 45 * time.Minute, want: "45 minutes"},
		{name: "should report hours under two days", d: 30 * time.Hour, want: "30 hours"},
		{name: "should report days beyond that", d: 90 * 24 * time.Hour, want: "90 days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := approximateDuration(tt.d); got != tt.want {
				t.Errorf("approximateDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// --retrieve writes the certificate immediately, so the operator learns the
// enrollment works now rather than when cron first runs.
func TestRunServiceEnroll_ShouldWriteTheCertificateWhenRetrieveIsAsked(t *testing.T) {
	certText := signedCertText(t, time.Now().Add(time.Hour))
	client := &fakeAPIClient{
		result:       &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"},
		retrieveCert: certText,
	}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	if err := runServiceEnroll(context.Background(), root, &out, keyPath, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !client.retrieveCalled {
		t.Error("expected --retrieve to redeem the code")
	}
	if _, err := os.Stat(keyPath + "-cert.pub"); err != nil {
		t.Errorf("expected the certificate to be written: %v", err)
	}
	if !strings.Contains(out.String(), "retrieved right away") {
		t.Errorf("expected the immediate retrieval to be reported, got:\n%s", out.String())
	}
}

// The code is printed before retrieval is attempted, and a retrieval failure
// must not cost the operator the one copy of it they will ever see. The
// ordering is the property; the comment on retrieveRightAway says so and
// nothing checked it.
func TestRunServiceEnroll_ShouldKeepTheCodeVisibleWhenRetrievalFails(t *testing.T) {
	client := &fakeAPIClient{
		result:      &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"},
		retrieveErr: errors.New("server said no"),
	}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	err := runServiceEnroll(context.Background(), root, &out, keyPath, true)
	if err == nil {
		t.Fatal("expected the retrieval failure to be reported")
	}
	if !strings.Contains(out.String(), "code-123") {
		t.Errorf("the enrollment code was lost on a retrieval failure, output:\n%s", out.String())
	}
}

// A server that answers with a public key instead of a certificate is a
// backend fault the operator has to be told about, not something to write to
// disk as though it were a certificate.
func TestRunServiceEnroll_ShouldRefuseANonCertificateFromTheServer(t *testing.T) {
	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	client := &fakeAPIClient{
		result:       &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"},
		retrieveCert: string(ssh.MarshalAuthorizedKey(kp.Public())),
	}
	root, keyPath := enrollFixture(t, client)
	var out bytes.Buffer

	err = runServiceEnroll(context.Background(), root, &out, keyPath, true)
	if err == nil {
		t.Fatal("expected a public key to be refused")
	}
	if !strings.Contains(err.Error(), "not a certificate") {
		t.Errorf("got %q, want it to say the server returned a public key", err.Error())
	}
}

// An existing keypair is enrolled as-is rather than regenerated, which is
// what lets an operator enroll a key they already trust.
func TestRunServiceEnroll_ShouldEnrollAnExistingKeypair(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusEnrolled, Code: "code-123"}}
	root, keyPath := enrollFixture(t, client)

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	pub := string(ssh.MarshalAuthorizedKey(kp.Public()))
	if err := os.WriteFile(keyPath, []byte("existing private key\n"), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	// 0600 rather than a public key's usual 0644: the test only needs the
	// file to exist and be readable by this process, and gosec rightly
	// objects to a wider mode in a fixture.
	if err := os.WriteFile(keyPath+".pub", []byte(pub), 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}

	var out bytes.Buffer
	if err := runServiceEnroll(context.Background(), root, &out, keyPath, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 1 || client.createdWith[0] != pub {
		t.Errorf("expected the existing public key to be enrolled, got %v", client.createdWith)
	}
	// The private half must be left exactly as it was.
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	if string(data) != "existing private key\n" {
		t.Error("the existing private key was overwritten")
	}
}
