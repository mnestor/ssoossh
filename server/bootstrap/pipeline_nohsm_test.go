//go:build !hsm

package bootstrap

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/pubsub"
)

// The default build has no PKCS#11 support. What matters is that a config
// asking for an HSM is refused outright, with an error that names the way
// forward -- never that it quietly falls back to some other key.
func TestNewCAKeySource_ShouldRefuseHSMConfigInACgoFreeBuild(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{
		HSM: config.HSMConfig{
			Module:     "/usr/lib/softhsm/libsofthsm2.so",
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

	ks, err := a.newCAKeySource()
	if err == nil {
		t.Fatal("expected a refusal when hsm is configured in a build without PKCS#11")
	}
	if ks != nil {
		t.Error("expected no key source alongside the refusal")
	}
	for _, want := range []string{"no PKCS#11 support", "ssh_key_agent", "hsm-tagged build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
