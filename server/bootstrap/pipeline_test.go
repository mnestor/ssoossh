package bootstrap

// Test methodology: unit test for initPipeline's CA-key FIPS check. Uses
// the same minimal *app fixture as services_test.go's
// TestInitServices_ShouldErrorOnInvalidSSHKey: the FIPS check runs right
// after the CA key parses, before initPipeline ever touches a.db or
// a.svc, so those can stay nil here.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/pubsub"
)

// testECDSAKeyPEM returns a throwaway P-384 ECDSA SSH private key in PEM
// format: FIPS-approved, unlike services_test.go's testSSHKeyPEM (ed25519).
func testECDSAKeyPEM(t *testing.T) string {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ecdsa key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test-key")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestInitPipeline_ShouldRejectANonFIPSApprovedCAKeyUnderFIPS(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: testSSHKeyPEM(t)}, FIPS: boolPtr(true)}

	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() {
		if err := ps.Close(t.Context()); err != nil {
			t.Errorf("unexpected error closing pub/sub: %v", err)
		}
	})
	a := &app{config: c, pubSub: ps}

	if err := a.initPipeline(ServerModeFull); err == nil {
		t.Fatal("expected startup to fail for a non-FIPS-approved (ed25519) CA key under FIPS")
	}
}

func TestInitPipeline_ShouldSucceedWithAFIPSApprovedCAKeyUnderFIPS(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: testECDSAKeyPEM(t)}, FIPS: boolPtr(true)}

	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	// Unlike the reject-path test above, initPipeline succeeds here and
	// registers handlers on the router — closing a router that was
	// registered on but never Run (this test doesn't exercise Bootstrap's
	// run loop) blocks until Watermill's own close timeout, so the cleanup
	// here doesn't assert on it the way TestInitServices_* does.
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	a := &app{config: c, pubSub: ps, svc: &services{}}

	if err := a.initPipeline(ServerModeFull); err != nil {
		t.Errorf("unexpected error for a FIPS-approved (ecdsa) CA key: %v", err)
	}
}

// TestNewCAKeySource tests the CA key source selection and memoization logic.
func TestNewCAKeySource_ShouldBuildConfigKeySourceWhenSSHKeyConfigured(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: testSSHKeyPEM(t)}}
	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	a := &app{config: c, pubSub: ps}

	ks, err := a.newCAKeySource()
	if err != nil {
		t.Fatalf("expected no error for valid ssh_key, got %v", err)
	}
	if ks == nil {
		t.Fatal("expected non-nil key source")
	}
	if a.closeCAKeySource != nil {
		t.Error("expected closeCAKeySource to be nil for config-backed source")
	}
}

func TestNewCAKeySource_ShouldMemoizeKeySource(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: testSSHKeyPEM(t)}}
	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	a := &app{config: c, pubSub: ps}

	// First call
	ks1, err := a.newCAKeySource()
	if err != nil {
		t.Fatalf("first call: unexpected error %v", err)
	}

	// Second call — should return the same instance
	ks2, err := a.newCAKeySource()
	if err != nil {
		t.Fatalf("second call: unexpected error %v", err)
	}

	if ks1 != ks2 {
		t.Error("expected second call to return the same cached instance")
	}
}

func TestNewCAKeySource_ShouldFailWhenSSHKeyEmpty(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: ""}}
	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	a := &app{config: c, pubSub: ps}

	_, err = a.newCAKeySource()
	if err == nil {
		t.Fatal("expected error when ssh_key is empty")
	}
	if !strings.Contains(err.Error(), "no CA private key configured") {
		t.Errorf("expected error mentioning 'no CA private key configured', got: %v", err)
	}
}

func TestNewCAKeySource_ShouldFailWhenHSMModuleNonexistent(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{
		HSM: config.HSMConfig{
			Module:     "/nonexistent.so",
			TokenLabel: "test-token",
			PIN:        "1234",
			KeyLabel:   "test-key",
		},
	}}
	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	a := &app{config: c, pubSub: ps}

	_, err = a.newCAKeySource()
	if err == nil {
		t.Fatal("expected error when HSM module does not exist")
	}
	if !strings.Contains(err.Error(), "PKCS#11") {
		t.Errorf("expected error mentioning 'PKCS#11', got: %v", err)
	}
}

func TestNewCAKeySource_ShouldFailWhenHSMPINFileUnreadable(t *testing.T) {
	t.Parallel()

	// Point to a nonexistent file in an empty temp directory
	pinFilePath := filepath.Join(t.TempDir(), "nonexistent.txt")

	c := &config.Config{Signer: config.SignerConfig{
		HSM: config.HSMConfig{
			Module:     "/usr/lib/softhsm/libsofthsm2.so",
			TokenLabel: "test-token",
			PINFile:    pinFilePath,
			KeyLabel:   "test-key",
		},
	}}
	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	a := &app{config: c, pubSub: ps}

	_, err = a.newCAKeySource()
	if err == nil {
		t.Fatal("expected error when HSM pin_file is unreadable")
	}
	if !strings.Contains(err.Error(), "pin_file") {
		t.Errorf("expected error mentioning 'pin_file', got: %v", err)
	}
}
