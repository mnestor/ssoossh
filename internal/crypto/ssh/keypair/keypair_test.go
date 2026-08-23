package keypair

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// should satisfy the generic Keypair interface
func TestSSHKeypair_ImplementsKeypair(t *testing.T) {
	t.Parallel()

	var _ Keypair = (*SSHKeypair)(nil)
}

// should generate a keypair and round-trip its private key through PEM for every supported algorithm
func TestNewSSHKeypair_GenerateAndMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		keyType string
		keySize int
		load    func(pem []byte) (*SSHKeypair, error)
	}{
		{name: "should round-trip an RSA keypair", keyType: "rsa", keySize: 2048, load: LoadSSHKeypair},
		{name: "should round-trip an ECDSA P-256 keypair", keyType: "ecdsa", keySize: 256, load: LoadSSHKeypair},
		{name: "should round-trip an ECDSA P-384 keypair", keyType: "ecdsa", keySize: 384, load: LoadSSHKeypair},
		{name: "should round-trip an ECDSA P-521 keypair", keyType: "ecdsa", keySize: 521, load: LoadSSHKeypair},
		{name: "should round-trip an Ed25519 keypair", keyType: "ed25519", keySize: 0, load: LoadSSHKeypair},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kp, err := NewSSHKeypair(tt.keyType, tt.keySize)
			if err != nil {
				t.Fatalf("NewSSHKeypair(%q, %d) error = %v", tt.keyType, tt.keySize, err)
			}

			authKey, err := kp.MarshalAuthorizedKey()
			if err != nil {
				t.Fatalf("MarshalAuthorizedKey() error = %v", err)
			}
			if authKey == "" {
				t.Error("MarshalAuthorizedKey() returned empty string")
			}

			privPEM, err := kp.MarshalPrivateKey()
			if err != nil {
				t.Fatalf("MarshalPrivateKey() error = %v", err)
			}

			loaded, err := tt.load(privPEM)
			if err != nil {
				t.Fatalf("load(privPEM) error = %v", err)
			}

			gotKey, err := loaded.MarshalAuthorizedKey()
			if err != nil {
				t.Fatalf("loaded.MarshalAuthorizedKey() error = %v", err)
			}
			if gotKey != authKey {
				t.Errorf("round-tripped public key = %q, want %q", gotKey, authKey)
			}
		})
	}
}

// should reject unsupported ECDSA curve sizes
func TestNewECDSAKeyPair_RejectsUnsupportedBits(t *testing.T) {
	t.Parallel()

	if _, err := NewECDSAKeyPair(224); err == nil {
		t.Error("NewECDSAKeyPair(224) error = nil, want error for unsupported curve size")
	}
}

// should reject an unknown key type
func TestNewSSHKeypair_RejectsUnknownType(t *testing.T) {
	t.Parallel()

	if _, err := NewSSHKeypair("dsa", 1024); err == nil {
		t.Error(`NewSSHKeypair("dsa", 1024) error = nil, want error for unsupported key type`)
	}
}

// should load every PEM block type LoadSSHKeypair supports: PKCS#1 RSA, SEC1
// EC, PKCS#8 (any algorithm), and OpenSSH format (any algorithm).
func TestLoadSSHKeypair_AllBlockTypes(t *testing.T) {
	t.Parallel()

	rsaKP, err := NewRSAKeyPair(2048)
	if err != nil {
		t.Fatalf("NewRSAKeyPair() error = %v", err)
	}
	ecdsaKP, err := NewECDSAKeyPair(256)
	if err != nil {
		t.Fatalf("NewECDSAKeyPair() error = %v", err)
	}
	ed25519KP, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	pkcs8Block := func(priv any) []byte {
		bytes, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: bytes})
	}
	opensshBlock := func(priv any) []byte {
		block, err := ssh.MarshalPrivateKey(priv, "test")
		if err != nil {
			t.Fatalf("ssh.MarshalPrivateKey() error = %v", err)
		}
		return pem.EncodeToMemory(block)
	}

	rsaPriv, ok := rsaKP.Private().(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("rsaKP.Private() = %T, want *rsa.PrivateKey", rsaKP.Private())
	}
	ecdsaPriv := ecdsaKP.Private()
	ed25519Priv, ok := ed25519KP.Private().(ed25519.PrivateKey)
	if !ok {
		t.Fatalf("ed25519KP.Private() = %T, want ed25519.PrivateKey", ed25519KP.Private())
	}

	tests := []struct {
		name string
		pem  []byte
	}{
		{name: "should load a PKCS#1 RSA PEM block", pem: mustMarshalPrivateKey(t, rsaKP)},
		{name: "should load a SEC1 EC PEM block", pem: mustMarshalPrivateKey(t, ecdsaKP)},
		{name: "should load an OpenSSH-format PEM block", pem: mustMarshalPrivateKey(t, ed25519KP)},
		{name: "should load a PKCS#8 PEM block wrapping an RSA key", pem: pkcs8Block(rsaPriv)},
		{name: "should load a PKCS#8 PEM block wrapping an Ed25519 key", pem: pkcs8Block(ed25519Priv)},
		{name: "should load a PKCS#8 PEM block wrapping an ECDSA key", pem: pkcs8Block(ecdsaPriv)},
		{name: "should load an OpenSSH-format PEM block wrapping an RSA key", pem: opensshBlock(rsaPriv)},
		{name: "should load an OpenSSH-format PEM block wrapping an ECDSA key", pem: opensshBlock(ecdsaPriv)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kp, err := LoadSSHKeypair(tt.pem)
			if err != nil {
				t.Fatalf("LoadSSHKeypair() error = %v", err)
			}
			if kp.Public() == nil {
				t.Error("LoadSSHKeypair() returned a keypair with no public key")
			}
		})
	}
}

// mustMarshalPrivateKey marshals kp's private key to PEM, failing the test on error.
func mustMarshalPrivateKey(t *testing.T, kp *SSHKeypair) []byte {
	t.Helper()
	data, err := kp.MarshalPrivateKey()
	if err != nil {
		t.Fatalf("MarshalPrivateKey() error = %v", err)
	}
	return data
}

// should reject malformed input at every stage: undecodable PEM, an unsupported block type, and malformed key bytes within a recognized block type
func TestLoadSSHKeypair_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	pkcs8Garbage := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not a key")})

	tests := []struct {
		name string
		in   []byte
	}{
		{name: "should reject data that is not PEM-encoded", in: []byte("not pem data")},
		{name: "should reject an unsupported PEM block type", in: pem.EncodeToMemory(&pem.Block{Type: "DSA PRIVATE KEY", Bytes: []byte("x")})},
		{name: "should reject malformed RSA PKCS#1 bytes", in: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("bad")})},
		{name: "should reject malformed EC SEC1 bytes", in: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("bad")})},
		{name: "should reject malformed PKCS#8 bytes", in: pkcs8Garbage},
		{name: "should reject malformed OpenSSH private key bytes", in: pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: []byte("bad")})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadSSHKeypair(tt.in); err == nil {
				t.Error("LoadSSHKeypair() error = nil, want error")
			}
		})
	}
}

// should reject a PKCS8 key of an algorithm type it does not recognize
func TestKeypairFromPKCS8_RejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	if _, err := keypairFromPKCS8("not a key"); err == nil {
		t.Error("keypairFromPKCS8() error = nil, want error for unsupported type")
	}
}

// should reject an OpenSSH-parsed key of an algorithm type it does not recognize
func TestKeypairFromOpenSSHRaw_RejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	if _, err := keypairFromOpenSSHRaw("not a key"); err == nil {
		t.Error("keypairFromOpenSSHRaw() error = nil, want error for unsupported type")
	}
}

// should expose the raw private key passed to the constructor
func TestSSHKeypair_Private(t *testing.T) {
	t.Parallel()

	kp, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	if kp.Private() == nil {
		t.Error("Private() = nil, want the generated private key")
	}
}

// should derive the SSH public key from the stored public key
func TestSSHKeypair_Public(t *testing.T) {
	t.Parallel()

	kp, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	if kp.Public() == nil {
		t.Error("Public() = nil, want a derived ssh.PublicKey")
	}
}

// should store and return the certificate, defaulting to nil when unset
func TestSSHKeypair_CertificateAndSetCertificate(t *testing.T) {
	t.Parallel()

	kp, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	if got := kp.Certificate(); got != nil {
		t.Errorf("Certificate() = %v, want nil before SetCertificate", got)
	}

	cert := &ssh.Certificate{Key: kp.Public(), CertType: ssh.UserCert}
	kp.SetCertificate(cert)
	if got := kp.Certificate(); got != cert {
		t.Errorf("Certificate() = %v, want %v", got, cert)
	}
}

// should report whether the loaded certificate was signed by the given CA
func TestSSHKeypair_SignedBy(t *testing.T) {
	t.Parallel()

	ca, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	other, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should report false when no certificate is set", func(t *testing.T) {
		t.Parallel()
		if leaf.SignedBy(ca.Public()) {
			t.Error("SignedBy() = true, want false with no certificate set")
		}
	})

	t.Run("should report false when the certificate has no SignatureKey", func(t *testing.T) {
		t.Parallel()
		unsigned := &SSHKeypair{privateKey: leaf.Private(), publicKey: leaf.publicKey}
		unsigned.SetCertificate(&ssh.Certificate{Key: leaf.Public(), CertType: ssh.UserCert})
		if unsigned.SignedBy(ca.Public()) {
			t.Error("SignedBy() = true, want false for a certificate with no SignatureKey")
		}
	})

	t.Run("should report true when signed by the given CA", func(t *testing.T) {
		t.Parallel()
		signed := &SSHKeypair{privateKey: leaf.Private(), publicKey: leaf.publicKey}
		cert := &ssh.Certificate{
			Key:         leaf.Public(),
			CertType:    ssh.UserCert,
			ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),
			ValidBefore: uint64(time.Now().Add(time.Hour).Unix()),
		}
		if err := cert.SignCert(rand.Reader, caSigner); err != nil {
			t.Fatalf("SignCert() error = %v", err)
		}
		signed.SetCertificate(cert)

		if !signed.SignedBy(ca.Public()) {
			t.Error("SignedBy() = false, want true for the signing CA")
		}
		if signed.SignedBy(other.Public()) {
			t.Error("SignedBy() = true, want false for an unrelated CA")
		}
	})

	t.Run("should reject a forged certificate whose SignatureKey claims the CA but whose Signature was not produced by it", func(t *testing.T) {
		t.Parallel()
		forged := &SSHKeypair{privateKey: leaf.Private(), publicKey: leaf.publicKey}
		// Sign with an unrelated key, then swap in the real CA's public key
		// as SignatureKey without re-signing — the Signature bytes were
		// never produced by ca's private key.
		cert := &ssh.Certificate{
			Key:         leaf.Public(),
			CertType:    ssh.UserCert,
			ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),
			ValidBefore: uint64(time.Now().Add(time.Hour).Unix()),
		}
		otherSigner, err := ssh.NewSignerFromKey(other.Private())
		if err != nil {
			t.Fatalf("NewSignerFromKey() error = %v", err)
		}
		if err := cert.SignCert(rand.Reader, otherSigner); err != nil {
			t.Fatalf("SignCert() error = %v", err)
		}
		cert.SignatureKey = ca.Public()
		forged.SetCertificate(cert)

		if forged.SignedBy(ca.Public()) {
			t.Error("SignedBy() = true, want false: SignatureKey matches ca but Signature was not produced by ca's private key")
		}
	})
}

// should reject a certificate whose SignatureKey matches but whose Signature bytes were tampered with after signing
func TestVerifyCertSignature_RejectsTamperedSignature(t *testing.T) {
	t.Parallel()

	ca, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	cert := &ssh.Certificate{
		Key:         leaf.Public(),
		CertType:    ssh.UserCert,
		ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),
		ValidBefore: uint64(time.Now().Add(time.Hour).Unix()),
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("SignCert() error = %v", err)
	}

	if !VerifyCertSignature(cert, ca.Public()) {
		t.Fatal("VerifyCertSignature() = false, want true for a genuinely CA-signed certificate")
	}

	tampered := *cert.Signature
	tampered.Blob = append([]byte(nil), tampered.Blob...)
	tampered.Blob[0] ^= 0xFF
	cert.Signature = &tampered

	if VerifyCertSignature(cert, ca.Public()) {
		t.Error("VerifyCertSignature() = true, want false for a certificate with a tampered signature blob")
	}
}

// should reject nil inputs and a certificate with no signature or SignatureKey
func TestVerifyCertSignature_RejectsIncompleteInput(t *testing.T) {
	t.Parallel()

	ca, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	tests := []struct {
		name string
		cert *ssh.Certificate
		ca   ssh.PublicKey
	}{
		{name: "should reject a nil certificate", cert: nil, ca: ca.Public()},
		{name: "should reject a nil ca", cert: &ssh.Certificate{Key: leaf.Public()}, ca: nil},
		{name: "should reject a certificate with no SignatureKey", cert: &ssh.Certificate{Key: leaf.Public()}, ca: ca.Public()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if VerifyCertSignature(tt.cert, tt.ca) {
				t.Error("VerifyCertSignature() = true, want false")
			}
		})
	}
}

// should marshal a set certificate to authorized_keys format and error when none is set
func TestSSHKeypair_CertificateString(t *testing.T) {
	t.Parallel()

	kp, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should error when no certificate is set", func(t *testing.T) {
		t.Parallel()
		if _, err := kp.CertificateString(); err == nil {
			t.Error("CertificateString() error = nil, want error")
		}
	})

	t.Run("should return the certificate in authorized_keys format", func(t *testing.T) {
		t.Parallel()
		withCert := &SSHKeypair{privateKey: kp.Private(), publicKey: kp.publicKey}
		withCert.SetCertificate(mustSignCert(t, kp.Public(), kp))
		got, err := withCert.CertificateString()
		if err != nil {
			t.Fatalf("CertificateString() error = %v", err)
		}
		if got == "" {
			t.Error("CertificateString() returned an empty string")
		}
	})
}

// mustSignCert builds and signs a minimal ssh.Certificate over pub using
// signerKP as the CA. ssh.Certificate.Marshal (used by MarshalAuthorizedKey)
// panics on an unsigned certificate, so every fixture that gets marshaled
// needs a real signature, not just a populated Key field.
func mustSignCert(t *testing.T, pub ssh.PublicKey, signerKP *SSHKeypair) *ssh.Certificate {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(signerKP.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	cert := &ssh.Certificate{Key: pub, CertType: ssh.UserCert}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		t.Fatalf("SignCert() error = %v", err)
	}
	return cert
}

// should marshal a set certificate to bytes, and return nil when none is set
func TestSSHKeypair_MarshalCertificate(t *testing.T) {
	t.Parallel()

	kp, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	if got := kp.MarshalCertificate(); got != nil {
		t.Errorf("MarshalCertificate() = %v, want nil with no certificate set", got)
	}

	kp.SetCertificate(mustSignCert(t, kp.Public(), kp))
	if got := kp.MarshalCertificate(); len(got) == 0 {
		t.Error("MarshalCertificate() returned empty bytes with a certificate set")
	}
}

// should parse and set a certificate from an authorized_keys-format string, rejecting anything else
func TestSSHKeypair_ParseCertificateFromString(t *testing.T) {
	t.Parallel()

	kp, err := NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	cert := mustSignCert(t, kp.Public(), kp)
	certStr := string(ssh.MarshalAuthorizedKey(cert))

	t.Run("should parse and set a valid certificate string", func(t *testing.T) {
		t.Parallel()
		target := &SSHKeypair{privateKey: kp.Private(), publicKey: kp.publicKey}
		if err := target.ParseCertificateFromString(certStr); err != nil {
			t.Fatalf("ParseCertificateFromString() error = %v", err)
		}
		if target.Certificate() == nil {
			t.Error("expected a certificate to be set")
		}
	})

	t.Run("should reject a malformed string", func(t *testing.T) {
		t.Parallel()
		target := &SSHKeypair{}
		if err := target.ParseCertificateFromString("not a certificate"); err == nil {
			t.Error("ParseCertificateFromString() error = nil, want error")
		}
	})

	t.Run("should reject a plain public key that is not a certificate", func(t *testing.T) {
		t.Parallel()
		authKey, err := kp.MarshalAuthorizedKey()
		if err != nil {
			t.Fatalf("MarshalAuthorizedKey() error = %v", err)
		}
		target := &SSHKeypair{}
		if err := target.ParseCertificateFromString(authKey); err == nil {
			t.Error("ParseCertificateFromString() error = nil, want error")
		}
	})
}

// should reject a private key of a type it does not know how to PEM-encode
func TestSSHKeypair_MarshalPrivateKey_RejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	kp := &SSHKeypair{privateKey: "not a supported key type"}
	if _, err := kp.MarshalPrivateKey(); err == nil {
		t.Error("MarshalPrivateKey() error = nil, want error for unsupported key type")
	}
}
