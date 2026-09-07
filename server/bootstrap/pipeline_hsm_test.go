//go:build hsm

package bootstrap

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/pubsub"
)

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
