package config

import (
	"testing"

	"github.com/mnestor/ssoossh/test/configgolden"
)

// should keep the values the embedded defaults resolve to stable, whatever
// happens to the prose around them. defaults.yaml is also the file shipped
// to /etc/ssoossh/ssoosshd.yaml, so it is edited for its comments far more
// often than for its values — see package configgolden.
func TestEmbeddedDefaults_ShouldMatchGolden(t *testing.T) {
	t.Parallel()

	configgolden.Assert(t, "./server/config/", "defaults.golden", configgolden.Flatten(t, defaultconfig))
}
