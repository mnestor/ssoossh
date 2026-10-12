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

// should write every commented-out key in defaults.yaml as "#key:", so it
// stands apart from the prose and turns on by deleting one character into
// YAML that loads the key where its indentation says. The file is generated
// by `make confdocs`; this is what keeps the generator honest about it.
func TestEmbeddedDefaults_ShouldUncommentEverySettingIntoValidYAML(t *testing.T) {
	t.Parallel()

	configgolden.AssertCommentedSettings(t, defaultconfig, Config{})
}
