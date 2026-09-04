//go:build pam

package main

// Test methodology: check 2 (key binding) is written and tested first, per
// the PAM design — it is the check that would otherwise
// be missing entirely. Its test uses a genuinely CA-signed certificate (via
// testCA.sign, a real (*ssh.Certificate).SignCert call), not a hand-built
// struct with a fake SignatureKey: the point is that checks 1, 3, and 4 all
// pass and only check 2 catches the substitution.

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// testCA is a throwaway CA used to build genuinely-signed certificates.
type testCA struct {
	signer ssh.Signer
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate CA keypair: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(kp.Private())
	if err != nil {
		t.Fatalf("failed to build CA signer: %v", err)
	}
	return &testCA{signer: signer}
}

func (ca *testCA) publicKey() ssh.PublicKey { return ca.signer.PublicKey() }

// sign issues a certificate for subjectKey, genuinely signed by ca.
func (ca *testCA) sign(t *testing.T, subjectKey ssh.PublicKey, principals []string, validAfter, validBefore time.Time) *ssh.Certificate {
	t.Helper()

	cert := &ssh.Certificate{
		Key:             subjectKey,
		CertType:        ssh.UserCert,
		ValidPrincipals: principals,
		ValidAfter:      uint64(validAfter.Unix()),  //nolint:gosec // test fixture, always a real date
		ValidBefore:     uint64(validBefore.Unix()), //nolint:gosec // test fixture, always a real date
	}
	if err := cert.SignCert(rand.Reader, ca.signer); err != nil {
		t.Fatalf("failed to sign test certificate: %v", err)
	}
	return cert
}

// newTestKeypair generates a fresh ephemeral keypair, standing in for the
// one Authenticate generates per attempt.
func newTestKeypair(t *testing.T) *keypair.SSHKeypair {
	t.Helper()

	kp, err := keypair.NewSSHKeypair("ed25519", 0)
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	return kp
}

func TestCheckKeyBinding_ShouldRejectGenuineCertificateIssuedToADifferentKey(t *testing.T) {
	ca := newTestCA(t)
	attacker := newTestKeypair(t)    // a certificate legitimately issued to this key
	thisAttempt := newTestKeypair(t) // the key generated for the attempt under test

	now := time.Now()
	// CA-signed, right principal, valid window — checks 1, 3, and 4 would
	// all pass. Only check 2 notices the key is wrong.
	cert := ca.sign(t, attacker.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))

	if err := checkKeyBinding(stubLogger{}, cert, thisAttempt); err == nil {
		t.Fatal("expected the certificate to be rejected: signed by the CA for the right principal, but issued to a different key")
	}
}

func TestCheckKeyBinding_ShouldRejectACertificateWithNoKey(t *testing.T) {
	kp := newTestKeypair(t)
	cert := &ssh.Certificate{}

	if err := checkKeyBinding(stubLogger{}, cert, kp); err == nil {
		t.Fatal("expected the certificate to be rejected: it carries no public key")
	}
}

func TestCheckKeyBinding_ShouldAcceptCertificateIssuedToTheSameKey(t *testing.T) {
	ca := newTestCA(t)
	kp := newTestKeypair(t)

	now := time.Now()
	cert := ca.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))

	if err := checkKeyBinding(stubLogger{}, cert, kp); err != nil {
		t.Errorf("expected the certificate to be accepted, got %v", err)
	}
}

func TestCheckCASignature_ShouldAcceptACertificateSignedByAnyTrustedCA(t *testing.T) {
	trusted1 := newTestCA(t)
	trusted2 := newTestCA(t)
	kp := newTestKeypair(t)

	now := time.Now()
	cert := trusted2.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))
	kp.SetCertificate(cert)

	if err := checkCASignature(stubLogger{}, kp, []ssh.PublicKey{trusted1.publicKey(), trusted2.publicKey()}); err != nil {
		t.Errorf("expected a certificate signed by the second of several trusted CAs to be accepted, got %v", err)
	}
}

func TestCheckCASignature_ShouldRejectACertificateSignedByAnUntrustedCA(t *testing.T) {
	untrusted := newTestCA(t)
	trusted := newTestCA(t)
	kp := newTestKeypair(t)

	now := time.Now()
	cert := untrusted.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))
	kp.SetCertificate(cert)

	if err := checkCASignature(stubLogger{}, kp, []ssh.PublicKey{trusted.publicKey()}); err == nil {
		t.Fatal("expected a certificate signed by an untrusted CA to be rejected")
	}
}

func TestCheckPrincipal_ShouldRejectWhenPrincipalsOmitTheAuthenticatingUser(t *testing.T) {
	cert := &ssh.Certificate{ValidPrincipals: []string{"bob", "carol"}}

	if err := checkPrincipal(stubLogger{}, cert, "alice", ""); err == nil {
		t.Fatal("expected rejection: alice is not among the certificate's principals")
	}
}

func TestCheckPrincipal_ShouldAcceptWhenPrincipalsIncludeTheAuthenticatingUser(t *testing.T) {
	cert := &ssh.Certificate{ValidPrincipals: []string{"bob", "alice"}}

	if err := checkPrincipal(stubLogger{}, cert, "alice", ""); err != nil {
		t.Errorf("expected acceptance, got %v", err)
	}
}

func TestCheckPrincipal_WithMapConfigured_ShouldAcceptAMappedPrincipal(t *testing.T) {
	path := writeMapFile(t, "alice:\n  - admin\n")
	cert := &ssh.Certificate{ValidPrincipals: []string{"admin"}}

	if err := checkPrincipal(stubLogger{}, cert, "alice", path); err != nil {
		t.Errorf("expected acceptance: admin is mapped to alice, got %v", err)
	}
}

func TestCheckPrincipal_WithMapConfigured_ShouldRejectAnUnmappedPrincipal(t *testing.T) {
	path := writeMapFile(t, "alice:\n  - admin\n")
	cert := &ssh.Certificate{ValidPrincipals: []string{"bob"}}

	if err := checkPrincipal(stubLogger{}, cert, "alice", path); err == nil {
		t.Fatal("expected rejection: bob is not mapped to alice")
	}
}

func TestCheckPrincipal_WithMapConfigured_ShouldRejectAnAccountAbsentFromTheMap(t *testing.T) {
	path := writeMapFile(t, "alice:\n  - admin\n")
	cert := &ssh.Certificate{ValidPrincipals: []string{"admin"}}

	if err := checkPrincipal(stubLogger{}, cert, "carol", path); err == nil {
		t.Fatal("expected rejection: carol has no entry in the map")
	}
}

func TestCheckPrincipal_WithMapMissingOrMalformed_ShouldFallBackToExactMatch(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	malformed := writeMapFile(t, "alice: [not: valid\n")

	for _, path := range []string{missing, malformed} {
		cert := &ssh.Certificate{ValidPrincipals: []string{"alice"}}
		if err := checkPrincipal(stubLogger{}, cert, "alice", path); err != nil {
			t.Errorf("expected exact-match fallback to accept alice for %q, got %v", path, err)
		}

		cert = &ssh.Certificate{ValidPrincipals: []string{"bob"}}
		if err := checkPrincipal(stubLogger{}, cert, "alice", path); err == nil {
			t.Errorf("expected exact-match fallback to reject bob for %q", path)
		}
	}
}

// writeMapFile writes a principals-map YAML file to a temp file and returns
// its path.
func writeMapFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return path
}

func TestCheckValidityWindow(t *testing.T) {
	now := time.Now()
	tolerance := 2 * time.Second

	tests := []struct {
		name        string
		validAfter  time.Time
		validBefore time.Time
		wantErr     bool
	}{
		{
			name:        "should accept a certificate inside its validity window",
			validAfter:  now.Add(-time.Minute),
			validBefore: now.Add(time.Minute),
			wantErr:     false,
		},
		{
			name:        "should reject a certificate not yet valid, outside tolerance",
			validAfter:  now.Add(5 * time.Second),
			validBefore: now.Add(time.Minute),
			wantErr:     true,
		},
		{
			name:        "should accept a certificate not yet valid but within tolerance",
			validAfter:  now.Add(time.Second),
			validBefore: now.Add(time.Minute),
			wantErr:     false,
		},
		{
			name:        "should reject an expired certificate, outside tolerance",
			validAfter:  now.Add(-time.Minute),
			validBefore: now.Add(-5 * time.Second),
			wantErr:     true,
		},
		{
			name:        "should accept an expired certificate within tolerance",
			validAfter:  now.Add(-time.Minute),
			validBefore: now.Add(-time.Second),
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert := &ssh.Certificate{
				ValidAfter:  uint64(tt.validAfter.Unix()),  //nolint:gosec // test fixture, always a real date
				ValidBefore: uint64(tt.validBefore.Unix()), //nolint:gosec // test fixture, always a real date
			}
			err := checkValidityWindow(stubLogger{}, cert, now, tolerance)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkValidityWindow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckValidityWindow_ShouldDescribeObservedSkewOnFailure(t *testing.T) {
	now := time.Now()
	cert := &ssh.Certificate{
		ValidAfter:  uint64(now.Add(10 * time.Second).Unix()), //nolint:gosec // test fixture, always a real date
		ValidBefore: uint64(now.Add(time.Minute).Unix()),      //nolint:gosec // test fixture, always a real date
	}

	err := checkValidityWindow(stubLogger{}, cert, now, 2*time.Second)
	if err == nil {
		t.Fatal("expected an error for a not-yet-valid certificate")
	}
	if !strings.Contains(err.Error(), "not yet valid") || !strings.Contains(err.Error(), "tolerance 2s") {
		t.Errorf("expected the error to describe the observed skew and configured tolerance, got %q", err.Error())
	}
}

func TestParseTrustedCAs(t *testing.T) {
	t.Run("should parse a single CA", func(t *testing.T) {
		ca := newTestCA(t)
		path := writeAuthorizedKeysFile(t, ca.publicKey())

		got, err := parseTrustedCAs(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d CAs, want 1", len(got))
		}
	})

	t.Run("should parse several CAs, one per line, for rotation", func(t *testing.T) {
		ca1 := newTestCA(t)
		ca2 := newTestCA(t)
		path := writeAuthorizedKeysFile(t, ca1.publicKey(), ca2.publicKey())

		got, err := parseTrustedCAs(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d CAs, want 2", len(got))
		}
	})

	t.Run("should error when the file does not exist", func(t *testing.T) {
		if _, err := parseTrustedCAs(filepath.Join(t.TempDir(), "missing.pub")); err == nil {
			t.Fatal("expected an error for a missing file")
		}
	})

	t.Run("should error when the file is empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.pub")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		if _, err := parseTrustedCAs(path); err == nil {
			t.Fatal("expected an error for an empty file")
		}
	})

	t.Run("should error when the file is not authorized_keys format", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "garbage.pub")
		if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		if _, err := parseTrustedCAs(path); err == nil {
			t.Fatal("expected an error for unparseable content")
		}
	})
}

// writeAuthorizedKeysFile writes keys, one per line in authorized_keys
// format, to a temp file and returns its path.
func writeAuthorizedKeysFile(t *testing.T, keys ...ssh.PublicKey) string {
	t.Helper()

	var buf []byte
	for _, k := range keys {
		buf = append(buf, ssh.MarshalAuthorizedKey(k)...)
	}
	path := filepath.Join(t.TempDir(), "cas.pub")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return path
}

// The logging tests below use recordingLogger (pam_ssoossh_test.go), which
// never gates on SetDebug: the gating is the real loggers' job
// (logger_test.go); these tests are about the content of the lines.

// joined flattens lines for a substring assertion on the whole log.
func joined(lines []string) string { return strings.Join(lines, "\n") }

func TestCheckCASignature_ShouldLogWhichTrustedCASignedWhenItPasses(t *testing.T) {
	trusted1 := newTestCA(t)
	trusted2 := newTestCA(t)
	kp := newTestKeypair(t)
	now := time.Now()
	kp.SetCertificate(trusted2.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour)))
	log := &recordingLogger{}

	if err := checkCASignature(log, kp, []ssh.PublicKey{trusted1.publicKey(), trusted2.publicKey()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := joined(log.debugs)
	for _, want := range []string{"check 1/4", "trusted CA 2 of 2", ssh.FingerprintSHA256(trusted2.publicKey())} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log %q lacks %q", got, want)
		}
	}
}

func TestCheckCASignature_ShouldNameTheSignatureKeyAndTrustedCAsWhenItFails(t *testing.T) {
	untrusted := newTestCA(t)
	trusted := newTestCA(t)
	kp := newTestKeypair(t)
	now := time.Now()
	kp.SetCertificate(untrusted.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour)))
	log := &recordingLogger{}

	err := checkCASignature(log, kp, []ssh.PublicKey{trusted.publicKey()})
	if err == nil {
		t.Fatal("expected rejection")
	}
	for _, want := range []string{ssh.FingerprintSHA256(untrusted.publicKey()), ssh.FingerprintSHA256(trusted.publicKey())} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks fingerprint %q", err.Error(), want)
		}
	}
	if len(log.debugs) != 0 {
		t.Errorf("expected no debug line on failure, got %v", log.debugs)
	}
}

func TestCheckCASignature_ShouldReportNoSignatureKeyWhenTheCertificateHasNone(t *testing.T) {
	kp := newTestKeypair(t)
	kp.SetCertificate(&ssh.Certificate{Key: kp.Public()})

	err := checkCASignature(&recordingLogger{}, kp, []ssh.PublicKey{newTestCA(t).publicKey()})
	if err == nil {
		t.Fatal("expected rejection: the certificate is unsigned")
	}
	if !strings.Contains(err.Error(), "signature key <none>") {
		t.Errorf("expected the error to say there is no signature key, got %q", err.Error())
	}
}

func TestCheckKeyBinding_ShouldLogTheBoundKeyWhenItPasses(t *testing.T) {
	ca := newTestCA(t)
	kp := newTestKeypair(t)
	now := time.Now()
	cert := ca.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))
	log := &recordingLogger{}

	if err := checkKeyBinding(log, cert, kp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := joined(log.debugs)
	for _, want := range []string{"check 2/4", ssh.FingerprintSHA256(kp.Public())} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log %q lacks %q", got, want)
		}
	}
}

func TestCheckKeyBinding_ShouldNameBothKeysWhenItFails(t *testing.T) {
	ca := newTestCA(t)
	attacker := newTestKeypair(t)
	thisAttempt := newTestKeypair(t)
	now := time.Now()
	cert := ca.sign(t, attacker.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))

	err := checkKeyBinding(&recordingLogger{}, cert, thisAttempt)
	if err == nil {
		t.Fatal("expected rejection")
	}
	for _, want := range []string{ssh.FingerprintSHA256(attacker.Public()), ssh.FingerprintSHA256(thisAttempt.Public())} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks fingerprint %q", err.Error(), want)
		}
	}
}

func TestCheckPrincipal_Logging(t *testing.T) {
	const missing = "\x00missing" // sentinel: configure a path that does not exist

	tests := []struct {
		name         string
		mapContents  string // "" means no map configured; missing means a nonexistent path
		principals   []string
		wantErr      bool
		wantDebug    []string
		wantWarnings int
	}{
		{
			name:       "should log an exact match when no map is configured",
			principals: []string{"bob", "alice"},
			wantDebug:  []string{"check 3/4", "exact match", `"alice"`, "[bob alice]"},
		},
		{
			name:        "should log the mapping it matched on when a map allows the principal",
			mapContents: "alice:\n  - admin\n  - ops\n",
			principals:  []string{"ops"},
			wantDebug:   []string{"check 3/4", `"ops" authorized for account "alice"`, "via principals-map", "allowed [admin ops]"},
		},
		{
			name:         "should warn and fall back to exact match when the map file is missing",
			mapContents:  missing,
			principals:   []string{"alice"},
			wantDebug:    []string{"check 3/4", "exact match"},
			wantWarnings: 1,
		},
		{
			name:         "should warn and fall back to exact match when the map file is malformed",
			mapContents:  "alice: [not: valid\n",
			principals:   []string{"alice"},
			wantDebug:    []string{"check 3/4", "exact match"},
			wantWarnings: 1,
		},
		{
			name:        "should log nothing at debug and name the allowed list when the map rejects",
			mapContents: "alice:\n  - admin\n",
			principals:  []string{"bob"},
			wantErr:     true,
		},
		{
			name:       "should log nothing at debug when the exact match fails",
			principals: []string{"bob"},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mapPath string
			switch tt.mapContents {
			case "":
			case missing:
				mapPath = filepath.Join(t.TempDir(), "missing.yaml")
			default:
				mapPath = writeMapFile(t, tt.mapContents)
			}
			cert := &ssh.Certificate{ValidPrincipals: tt.principals}
			log := &recordingLogger{}

			err := checkPrincipal(log, cert, "alice", mapPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkPrincipal() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if len(log.debugs) != 0 {
					t.Errorf("expected no debug line on failure, got %v", log.debugs)
				}
				if mapPath != "" && !strings.Contains(err.Error(), "allowed: [admin]") {
					t.Errorf("expected the map rejection to name the allowed list, got %q", err.Error())
				}
				return
			}
			got := joined(log.debugs)
			for _, want := range tt.wantDebug {
				if !strings.Contains(got, want) {
					t.Errorf("debug log %q lacks %q", got, want)
				}
			}
			if len(log.warnings) != tt.wantWarnings {
				t.Errorf("got %d warnings %v, want %d", len(log.warnings), log.warnings, tt.wantWarnings)
			}
			if tt.wantWarnings > 0 && !strings.Contains(joined(log.warnings), mapPath) {
				t.Errorf("expected the warning to name the map path %q, got %v", mapPath, log.warnings)
			}
		})
	}
}

func TestCheckValidityWindow_ShouldLogTheWindowAndToleranceWhenItPasses(t *testing.T) {
	now := time.Date(2026, 9, 4, 18, 42, 9, 0, time.UTC)
	cert := &ssh.Certificate{
		ValidAfter:  uint64(now.Add(-time.Minute).Unix()),    //nolint:gosec // test fixture, always a real date
		ValidBefore: uint64(now.Add(5 * time.Minute).Unix()), //nolint:gosec // test fixture, always a real date
	}
	log := &recordingLogger{}

	if err := checkValidityWindow(log, cert, now, 2*time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := joined(log.debugs)
	for _, want := range []string{"check 4/4", "2026-09-04T18:42:09Z", "2026-09-04T18:41:09Z", "2026-09-04T18:47:09Z", "tolerance 2s", "5m0s remaining"} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log %q lacks %q", got, want)
		}
	}
}

func TestCheckValidityWindow_ShouldLogNothingAtDebugWhenItFails(t *testing.T) {
	now := time.Now()
	cert := &ssh.Certificate{
		ValidAfter:  uint64(now.Add(-time.Hour).Unix()),   //nolint:gosec // test fixture, always a real date
		ValidBefore: uint64(now.Add(-time.Minute).Unix()), //nolint:gosec // test fixture, always a real date
	}
	log := &recordingLogger{}

	if err := checkValidityWindow(log, cert, now, 2*time.Second); err == nil {
		t.Fatal("expected rejection: the certificate has expired")
	}
	if len(log.debugs) != 0 {
		t.Errorf("expected no debug line on failure, got %v", log.debugs)
	}
}
