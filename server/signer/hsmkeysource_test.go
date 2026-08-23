package signer

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// genECDSAKey generates a new ECDSA crypto.Signer with the given curve bits.
func genECDSAKey(t *testing.T, bits int) crypto.Signer {
	t.Helper()

	kp, err := keypair.NewECDSAKeyPair(bits)
	if err != nil {
		t.Fatalf("failed to generate ECDSA keypair: %v", err)
	}
	// The private key from NewECDSAKeyPair is *ecdsa.PrivateKey which implements crypto.Signer
	return kp.Private().(crypto.Signer)
}

// genRSAKey generates a new RSA crypto.Signer with the given bit length.
// For small keys (< 2048), it uses crypto/rsa directly; for >= 2048, it uses the keypair generator.
func genRSAKey(t *testing.T, bits int) crypto.Signer {
	t.Helper()

	if bits < 2048 {
		// For small keys, use crypto/rsa directly (bypassing keypair's validation)
		privKey, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Fatalf("failed to generate RSA key: %v", err)
		}
		return privKey
	}

	kp, err := keypair.NewRSAKeyPair(bits)
	if err != nil {
		t.Fatalf("failed to generate RSA keypair: %v", err)
	}
	// The private key from NewRSAKeyPair is *rsa.PrivateKey which implements crypto.Signer
	return kp.Private().(crypto.Signer)
}

// genEd25519Key generates a new Ed25519 crypto.Signer.
func genEd25519Key(t *testing.T) crypto.Signer {
	t.Helper()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate Ed25519 keypair: %v", err)
	}
	// The private key from NewEd25519KeyPair is ed25519.PrivateKey which implements crypto.Signer
	return kp.Private().(crypto.Signer)
}

func TestWrapCASigner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        crypto.Signer
		wantType   string // expected ssh public key type, "" if error
		wantErr    string // expected error substring
		checkAlgos bool   // if true, check Algorithms()[0] == KeyAlgoRSASHA512 for RSA
	}{
		{
			name:     "should wrap ecdsa p256",
			key:      genECDSAKey(t, 256),
			wantType: "ecdsa-sha2-nistp256",
			wantErr:  "",
		},
		{
			name:     "should wrap ecdsa p384",
			key:      genECDSAKey(t, 384),
			wantType: "ecdsa-sha2-nistp384",
			wantErr:  "",
		},
		{
			name:     "should wrap ecdsa p521",
			key:      genECDSAKey(t, 521),
			wantType: "ecdsa-sha2-nistp521",
			wantErr:  "",
		},
		{
			name:       "should wrap rsa 2048 restricted to rsa-sha2",
			key:        genRSAKey(t, 2048),
			wantType:   "ssh-rsa",
			wantErr:    "",
			checkAlgos: true,
		},
		{
			name:     "should reject rsa below 2048",
			key:      genRSAKey(t, 1024),
			wantType: "",
			wantErr:  "at least 2048",
		},
		{
			name:     "should reject ed25519 with hsm guidance",
			key:      genEd25519Key(t),
			wantType: "",
			wantErr:  "not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wrapped, err := wrapCASigner(tt.key)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error to contain %q, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check public key type
			pubKey := wrapped.PublicKey()
			if got := pubKey.Type(); got != tt.wantType {
				t.Errorf("expected public key type %q, got %q", tt.wantType, got)
			}

			// For RSA, check that it's restricted to rsa-sha2 algorithms
			if tt.checkAlgos {
				algoSigner, ok := wrapped.(ssh.MultiAlgorithmSigner)
				if !ok {
					t.Fatal("expected RSA-wrapped signer to implement ssh.MultiAlgorithmSigner")
				}
				algos := algoSigner.Algorithms()
				if len(algos) == 0 {
					t.Fatal("expected at least one algorithm in Algorithms()")
				}
				if algos[0] != ssh.KeyAlgoRSASHA512 {
					t.Errorf("expected Algorithms()[0] to be %q, got %q", ssh.KeyAlgoRSASHA512, algos[0])
				}
			}
		})
	}
}

// TestWrapCASigner_EndToEnd signs a minimal certificate with the wrapped P-256
// signer and verifies the signature.
func TestWrapCASigner_EndToEnd(t *testing.T) {
	t.Parallel()

	// Generate a P-256 CA key and wrap it
	caKey := genECDSAKey(t, 256)
	caSigner, err := wrapCASigner(caKey)
	if err != nil {
		t.Fatalf("failed to wrap CA key: %v", err)
	}

	// Generate a user public key
	userKP, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate user keypair: %v", err)
	}
	userPubAuthorizedKey, err := userKP.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal user public key: %v", err)
	}
	userPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(userPubAuthorizedKey))
	if err != nil {
		t.Fatalf("failed to parse user public key: %v", err)
	}

	// Build a minimal certificate
	now := uint64(time.Now().Unix()) //nolint:gosec // G115: Unix() is positive for any clock this test can run under
	cert := &ssh.Certificate{
		Nonce:           make([]byte, 16),
		Key:             userPub,
		Serial:          1,
		CertType:        ssh.UserCert,
		KeyId:           "test-key",
		ValidPrincipals: []string{"testuser"},
		ValidAfter:      now,
		ValidBefore:     now + 3600,
		Permissions: ssh.Permissions{
			CriticalOptions: make(map[string]string),
			Extensions:      make(map[string]string),
		},
	}

	// Sign the certificate
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("failed to sign certificate: %v", err)
	}

	// Verify the certificate was signed by the CA
	if got := string(cert.SignatureKey.Marshal()); got != string(caSigner.PublicKey().Marshal()) {
		t.Error("certificate was not signed by the expected CA")
	}

	// Verify the certificate validates against a checker that trusts the CA
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(caSigner.PublicKey().Marshal())
		},
	}
	if err := checker.CheckCert("testuser", cert); err != nil {
		t.Errorf("certificate validation failed: %v", err)
	}
}

// contains checks if haystack contains needle.
func contains(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 && (haystack == needle || (len(haystack) > len(needle) && (haystack[:len(needle)] == needle || haystack[len(haystack)-len(needle):] == needle || findSubstring(haystack, needle))))
}

func findSubstring(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
