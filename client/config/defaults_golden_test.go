package config

import (
	"testing"

	"github.com/mnestor/ssoossh/test/configgolden"
)

// should keep the values the embedded defaults resolve to stable, whatever
// happens to the prose around them. defaults.yaml is also the file shipped
// to /etc/ssoossh/ssoossh.yaml, so it is edited for its comments far more
// often than for its values — see package configgolden.
//
// Keys deliberately left commented out here (sshkey, fips) resolve at
// runtime instead, so this golden is also what catches one being activated
// by accident: an active `fips: false` is not the same as an absent one.
func TestEmbeddedDefaults_ShouldMatchGolden(t *testing.T) {
	t.Parallel()

	configgolden.Assert(t, "./client/config/", "defaults.golden", configgolden.Flatten(t, defaultconfig))
}
