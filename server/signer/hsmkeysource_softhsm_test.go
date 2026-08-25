//go:build softhsm

package signer

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Global SoftHSM manager and temp directory, initialized in TestMain.
var (
	softhsmMgr    *softhsmManager
	softhsmTmpDir string
)

// findSofthsmModule returns the SoftHSM2 PKCS#11 module path or skips.
// Checks SSOOSSH_TEST_PKCS11_MODULE, then Debian/Ubuntu locations, then
// non-standard paths like /usr/local/lib/softhsm/libsofthsm2.so.
func findSofthsmModule() string {
	if p := os.Getenv("SSOOSSH_TEST_PKCS11_MODULE"); p != "" {
		return p
	}
	for _, p := range []string{
		"/usr/lib/softhsm/libsofthsm2.so",
		"/usr/lib/x86_64-linux-gnu/softhsm/libsofthsm2.so",
		"/usr/lib/aarch64-linux-gnu/softhsm/libsofthsm2.so",
		"/usr/local/lib/softhsm/libsofthsm2.so",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// softhsmManager manages a single shared SoftHSM2 token directory and
// configuration for all tests in the process. SoftHSM2 caches the config
// from the first module load, so reusing one conf+tokendir with distinct
// token labels per test ensures isolation despite the caching behavior.
type softhsmManager struct {
	confPath  string
	tokensDir string
	module    string
}

// TestMain initializes the shared SoftHSM2 environment once and ensures
// cleanup after all tests finish. This approach works around SoftHSM2's
// config caching by creating one conf+tokendir per test binary run, with
// distinct token labels per test for isolation.
func TestMain(m *testing.M) {
	// Create the shared temp directory.
	tmpDir, err := os.MkdirTemp("", "softhsm2-test-*")
	if err != nil {
		// If we can't create the temp dir, skip the tests gracefully
		// (they will see softhsmMgr == nil and skip).
		softhsmTmpDir = ""
	} else {
		softhsmTmpDir = tmpDir

		tokensDir := filepath.Join(tmpDir, "tokens")
		if err := os.MkdirAll(tokensDir, 0o755); err == nil {
			confPath := filepath.Join(tmpDir, "softhsm2.conf")
			confContent := "directories.tokendir = " + tokensDir + "\nobjectstore.backend = file\n"
			if err := os.WriteFile(confPath, []byte(confContent), 0o600); err == nil {
				// Set the environment variable for this process.
				if err := os.Setenv("SOFTHSM2_CONF", confPath); err == nil {
					module := findSofthsmModule()
					if module != "" {
						softhsmMgr = &softhsmManager{
							confPath:  confPath,
							tokensDir: tokensDir,
							module:    module,
						}
					}
				}
			}
		}
	}

	// Run all tests.
	code := m.Run()

	// Clean up the temp directory after all tests finish.
	if softhsmTmpDir != "" {
		os.RemoveAll(softhsmTmpDir)
	}

	os.Exit(code)
}

// provisionToken creates a new SoftHSM2 token with the given label,
// generates one key of the given type, and returns the module path.
// keyType is "EC:prime256v1" or "RSA:2048".
func provisionToken(t *testing.T, tokenLabel, keyType, keyLabel, keyID string) string {
	t.Helper()

	if softhsmMgr == nil {
		t.Skip("softhsm2 not initialized")
	}

	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "SOFTHSM2_CONF="+softhsmMgr.confPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Initialize a new token for this test.
	run("softhsm2-util", "--init-token", "--free", "--label", tokenLabel,
		"--pin", "1234", "--so-pin", "123456")

	// Generate the key in this token. --token-label is what pins it here:
	// without it pkcs11-tool targets the first slot that holds a token, and
	// SoftHSM2 assigns slot IDs at random on --init-token. From the second
	// provisioned token onwards that is a coin flip, and losing it puts the
	// key in the previous test's token, leaving this one empty.
	run("pkcs11-tool", "--module", softhsmMgr.module,
		"--token-label", tokenLabel, "--login", "--pin", "1234",
		"--keypairgen", "--key-type", keyType,
		"--label", keyLabel, "--id", keyID)

	return softhsmMgr.module
}

// buildTestCert constructs a minimal ssh.Certificate for testing,
// using the given public key string (authorized_keys format).
// This mirrors the certificate construction from sign_test.go.
func buildTestCert(t *testing.T, publicKeyStr string) *ssh.Certificate {
	t.Helper()

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKeyStr))
	if err != nil {
		t.Fatalf("failed to parse public key: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	cert := &ssh.Certificate{
		Nonce:           []byte("test-nonce"),
		Key:             pub,
		Serial:          1,
		CertType:        ssh.UserCert,
		KeyId:           "test-key",
		ValidPrincipals: []string{"testuser"},
		ValidAfter:      uint64(now.Unix()),                //nolint:gosec // a Unix timestamp is positive for any real date
		ValidBefore:     uint64(now.Add(time.Hour).Unix()), //nolint:gosec // as above
		Permissions:     ssh.Permissions{Extensions: map[string]string{}},
	}
	return cert
}

// TestHSMKeySource_ShouldSignWithECDSAKeyFromSoftHSM tests that an
// ECDSA P-256 key in SoftHSM2 can be used to sign and verify a certificate.
func TestHSMKeySource_ShouldSignWithECDSAKeyFromSoftHSM(t *testing.T) {
	tokenLabel := "hsm-ec-sign"
	keyLabel := "ec-key"
	module := provisionToken(t, tokenLabel, "EC:prime256v1", keyLabel, "01")

	// Configure the HSM key source.
	hs, err := NewHSMKeySource(HSMParams{
		Module:     module,
		TokenLabel: tokenLabel,
		PIN:        "1234",
		KeyID:      []byte{0x01},
		KeyLabel:   keyLabel,
	})
	if err != nil {
		t.Fatalf("failed to create HSM key source: %v", err)
	}
	defer hs.Close()

	signer, err := hs.Signer(context.Background())
	if err != nil {
		t.Fatalf("failed to get signer: %v", err)
	}

	// Create a test certificate and sign it.
	publicKeyStr, err := newTestPublicKeyString(t)
	if err != nil {
		t.Fatalf("failed to generate test public key: %v", err)
	}
	cert := buildTestCert(t, publicKeyStr)

	// Sign the certificate.
	err = cert.SignCert(rand.Reader, signer)
	if err != nil {
		t.Fatalf("failed to sign certificate: %v", err)
	}

	// Verify the signature against the signer's public key.
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(signer.PublicKey().Marshal())
		},
	}
	if err := checker.CheckCert("testuser", cert); err != nil {
		t.Errorf("certificate verification failed: %v", err)
	}
}

// TestHSMKeySource_ShouldSignRSAWithSHA2FromSoftHSM tests that an RSA 2048-bit
// key in SoftHSM2 signs with rsa-sha2-512 algorithm.
func TestHSMKeySource_ShouldSignRSAWithSHA2FromSoftHSM(t *testing.T) {
	tokenLabel := "hsm-rsa-sha2"
	keyLabel := "rsa-key"
	module := provisionToken(t, tokenLabel, "RSA:2048", keyLabel, "02")

	// Configure the HSM key source.
	hs, err := NewHSMKeySource(HSMParams{
		Module:     module,
		TokenLabel: tokenLabel,
		PIN:        "1234",
		KeyID:      []byte{0x02},
		KeyLabel:   keyLabel,
	})
	if err != nil {
		t.Fatalf("failed to create HSM key source: %v", err)
	}
	defer hs.Close()

	signer, err := hs.Signer(context.Background())
	if err != nil {
		t.Fatalf("failed to get signer: %v", err)
	}

	// Create a test certificate and sign it.
	publicKeyStr, err := newTestPublicKeyString(t)
	if err != nil {
		t.Fatalf("failed to generate test public key: %v", err)
	}
	cert := buildTestCert(t, publicKeyStr)

	// Sign the certificate.
	err = cert.SignCert(rand.Reader, signer)
	if err != nil {
		t.Fatalf("failed to sign certificate: %v", err)
	}

	// Verify that the signature format is rsa-sha2-512.
	if cert.Signature.Format != ssh.KeyAlgoRSASHA512 {
		t.Errorf("expected signature format %q, got %q", ssh.KeyAlgoRSASHA512, cert.Signature.Format)
	}

	// Verify the signature against the signer's public key.
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(signer.PublicKey().Marshal())
		},
	}
	if err := checker.CheckCert("testuser", cert); err != nil {
		t.Errorf("certificate verification failed: %v", err)
	}
}

// TestHSMKeySource_ShouldFailWhenKeyLabelMissing tests that NewHSMKeySource
// returns an error containing "no key pair found" when the requested key
// label does not exist.
func TestHSMKeySource_ShouldFailWhenKeyLabelMissing(t *testing.T) {
	tokenLabel := "hsm-missing-label"
	keyLabel := "ec-key"
	module := provisionToken(t, tokenLabel, "EC:prime256v1", keyLabel, "01")

	// Try to open with the wrong label.
	_, err := NewHSMKeySource(HSMParams{
		Module:     module,
		TokenLabel: tokenLabel,
		PIN:        "1234",
		KeyID:      nil,    // not using ID
		KeyLabel:   "nope", // wrong label
	})

	if err == nil {
		t.Fatal("expected an error when key label is missing, got nil")
	}

	// The error should indicate that the key pair was not found.
	// Either from crypto11 directly or from our wrapper.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "not found") && !strings.Contains(errMsg, "no key pair found") {
		t.Errorf("expected error to indicate key not found, got: %v", err)
	}
}

// TestHSMKeySource_ShouldFailWhenPINWrong tests that NewHSMKeySource
// returns an error from the crypto11 Configure call when the PIN is wrong.
func TestHSMKeySource_ShouldFailWhenPINWrong(t *testing.T) {
	tokenLabel := "hsm-wrong-pin"
	keyLabel := "ec-key"
	module := provisionToken(t, tokenLabel, "EC:prime256v1", keyLabel, "01")

	// Try to open with the wrong PIN.
	_, err := NewHSMKeySource(HSMParams{
		Module:     module,
		TokenLabel: tokenLabel,
		PIN:        "9999", // wrong PIN
		KeyID:      nil,
		KeyLabel:   keyLabel,
	})

	if err == nil {
		t.Fatal("expected an error when PIN is wrong, got nil")
	}

	// The error should come from the crypto11 Configure call.
	// We don't mandate a specific error message, just that one occurs.
}

// newTestPublicKeyString returns a fresh Ed25519 public key in
// authorized_keys format, as a string. This is used for certificate
// signing tests to have a distinct key to sign, separate from the CA key.
func newTestPublicKeyString(t *testing.T) (string, error) {
	t.Helper()

	// Generate a temporary Ed25519 key just for the user part of the cert.
	// We use a simple approach: exec ssh-keygen or use the keypair package.
	// For simplicity, we'll use os.Exec to generate a key.
	keyFile := filepath.Join(t.TempDir(), "test_key")

	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyFile, "-C", "test")
	if err := cmd.Run(); err != nil {
		// Fallback: use a hardcoded test key if ssh-keygen is not available.
		// This is the public key part only, suitable for certificate construction.
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBLa4VPBz1XHEgmhWpRVIGLW7DF3CeLmPBfJvK1xLNQL test@localhost", nil
	}

	pubFile := keyFile + ".pub"
	pubBytes, err := os.ReadFile(pubFile)
	if err != nil {
		return "", err
	}

	return string(pubBytes), nil
}
