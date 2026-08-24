// Package packaging_test checks that what ships in a package is the same
// file the binary embeds as its defaults.
//
// The two used to be separate: docs/ssoossh.yaml.default and
// docs/ssoosshd.yaml.default were shipped to /etc/ssoossh, while
// client/config/defaults.yaml and server/config/defaults.yaml were embedded.
// Nothing tied them together, and they drifted — the shipped client sample
// documented an ecdsa P-384 default and a hard FIPS error while the embedded
// file's comments still described ed25519 and advisory warnings. These tests
// keep there being one file per side to update.
package packaging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// shippedConfigs maps each package destination to the single repository file
// that must supply it — the same file its binary embeds as defaults.
var shippedConfigs = map[string]string{
	"/etc/ssoossh/ssoossh.yaml":  "client/config/defaults.yaml",
	"/etc/ssoossh/ssoosshd.yaml": "server/config/defaults.yaml",
}

// retiredConfigs are the duplicates that shipped before the merge. Naming
// them keeps a revert from quietly reintroducing the drift.
var retiredConfigs = []string{
	"docs/ssoossh.yaml.default",
	"docs/ssoosshd.yaml.default",
	"ssoossh.default.yaml",
}

// goreleaserConfig is the subset of .goreleaser.yml these tests read.
type goreleaserConfig struct {
	NFPMs []struct {
		ID       string `yaml:"id"`
		Contents []struct {
			Src string `yaml:"src"`
			Dst string `yaml:"dst"`
		} `yaml:"contents"`
	} `yaml:"nfpms"`
	Archives []struct {
		ID    string        `yaml:"id"`
		Files []archiveFile `yaml:"files"`
	} `yaml:"archives"`
}

// archiveFile is one entry in an archive's files list. GoReleaser accepts
// either a bare path or a {src, dst, strip_parent} mapping, and the config
// uses both, so this reads whichever form is written.
type archiveFile struct {
	Src string `yaml:"src"`
}

func (f *archiveFile) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&f.Src)
	}
	type plain archiveFile
	return node.Decode((*plain)(f))
}

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve the repository root: %v", err)
	}
	return root
}

func loadGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()

	path := filepath.Join(repoRoot(t), ".goreleaser.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}

	var cfg goreleaserConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	return cfg
}

// should install each /etc config from the file its binary already embeds,
// so there is exactly one file per side to keep current.
func TestNFPMShouldShipTheEmbeddedDefaults(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	seen := map[string]bool{}
	for _, pkg := range cfg.NFPMs {
		for _, content := range pkg.Contents {
			want, ok := shippedConfigs[content.Dst]
			if !ok {
				continue
			}
			seen[content.Dst] = true
			if content.Src != want {
				t.Errorf("nfpm %q installs %s from %q, want %q — the shipped config must be the file the binary embeds",
					pkg.ID, content.Dst, content.Src, want)
			}
		}
	}

	for dst := range shippedConfigs {
		if !seen[dst] {
			t.Errorf("no nfpm package installs %s; every packaged binary needs its configuration", dst)
		}
	}
}

// should put the same file in the release archives as in the packages.
func TestArchivesShouldShipTheEmbeddedDefaults(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	wanted := map[string]bool{}
	for _, src := range shippedConfigs {
		wanted[src] = true
	}

	for _, archive := range cfg.Archives {
		for _, file := range archive.Files {
			if !strings.HasSuffix(file.Src, ".yaml") && !strings.HasSuffix(file.Src, ".yaml.default") {
				continue
			}
			if !wanted[file.Src] {
				t.Errorf("archive %q ships %q, want one of %v", archive.ID, file.Src, sortedKeys(wanted))
			}
		}
	}
}

// should leave no trace of the pre-merge duplicates: not on disk, and not
// referenced from the release configuration.
func TestRetiredConfigsShouldBeGone(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, rel := range retiredConfigs {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Errorf("%s still exists; its content belongs in the file the binary embeds", rel)
		}
	}

	for _, name := range []string{".goreleaser.yml", ".goreleaser-pam-amd64.yml", ".goreleaser-pam-arm64.yml"} {
		path := filepath.Join(root, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, rel := range retiredConfigs {
			if strings.Contains(string(raw), rel) {
				t.Errorf("%s still references the retired %s", name, rel)
			}
		}
	}
}

// should keep every shipped config readable from the repository root, so a
// release does not fail on a path that only the test knew about.
func TestShippedConfigsShouldExist(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for dst, src := range shippedConfigs {
		if _, err := os.Stat(filepath.Join(root, src)); err != nil {
			t.Errorf("%s is installed from %s, which does not exist: %v", dst, src, err)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
