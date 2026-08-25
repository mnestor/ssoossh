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
	"slices"
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
	Builds []struct {
		ID  string   `yaml:"id"`
		Env []string `yaml:"env"`
	} `yaml:"builds"`
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

// mailTemplateDir is the directory server/mail embeds through
// server/resources. The packaged copies come from here so an operator
// writing an override starts from the exact template their binary is
// rendering.
const mailTemplateDir = "server/resources/mail"

// serverPackageIDs and serverArchiveIDs are the ssoosshd artifacts. Mail
// templates and the server man pages belong to these and not to the client.
var (
	serverPackageIDs = []string{"server", "server-musl"}
	serverArchiveIDs = []string{"linux-server-archives", "linux-server-musl-archives"}
)

// should ship the mail templates the server binary embeds, so an operator
// who installed a package — and has no source tree — can copy one out as
// the starting point for a mail.template_dir override.
//
// They are reference copies under /usr/share, deliberately not installed
// into an active template_dir: a shipped file in an override directory
// becomes an override, and then an upgrade either destroys the operator's
// edits or (with config|noreplace) pins them to a stale template forever.
// A stale file is not a cosmetic problem here — mail.Renderer rejects an
// override directory holding a template for a notification kind it does not
// recognize, so a kind removed in a later release would stop the server.
func TestServerPackagesShouldShipTheMailTemplates(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	seen := map[string]bool{}
	for _, pkg := range cfg.NFPMs {
		for _, content := range pkg.Contents {
			if !strings.HasPrefix(content.Src, mailTemplateDir) {
				continue
			}
			seen[pkg.ID] = true
			if want := "/usr/share/ssoossh/mail-templates/"; content.Dst != want {
				t.Errorf("nfpm %q installs the mail templates to %q, want %q", pkg.ID, content.Dst, want)
			}
		}
	}

	for _, id := range serverPackageIDs {
		if !seen[id] {
			t.Errorf("nfpm %q ships no mail templates; every packaged ssoosshd needs them for override authoring", id)
		}
	}

	// The client has no use for them and installing them there would put
	// two packages in the same directory.
	for _, pkg := range cfg.NFPMs {
		if !slices.Contains(serverPackageIDs, pkg.ID) && seen[pkg.ID] {
			t.Errorf("nfpm %q ships mail templates but is not a server package", pkg.ID)
		}
	}
}

// should put the same templates in the server release archives.
func TestServerArchivesShouldShipTheMailTemplates(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	seen := map[string]bool{}
	for _, archive := range cfg.Archives {
		for _, file := range archive.Files {
			if strings.HasPrefix(file.Src, mailTemplateDir) {
				seen[archive.ID] = true
			}
		}
	}

	for _, id := range serverArchiveIDs {
		if !seen[id] {
			t.Errorf("archive %q ships no mail templates", id)
		}
	}
}

// should keep every packaged template a real file, so a release does not
// fail on a glob that matches nothing.
func TestMailTemplatesShouldExist(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(filepath.Join(repoRoot(t), mailTemplateDir))
	if err != nil {
		t.Fatalf("failed to read %s: %v", mailTemplateDir, err)
	}

	var count int
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmpl") {
			count++
		}
	}
	if count == 0 {
		t.Errorf("%s holds no .tmpl files, so the packaged glob would match nothing", mailTemplateDir)
	}
}

// manPageOwner maps a man page's filename prefix to the packages that must
// install it. Packages are split by which binary a page documents, because
// deb and rpm refuse to co-install two packages owning the same path — a
// host running ssoosshd with the client beside it is an ordinary setup, and
// `apt install ssoossh ssoosshd` has to keep working. Verified: dpkg answers
// "trying to overwrite '/usr/share/man/man8/ssoosshd.8', which is also in
// package ...".
//
// Archives are not split. A tarball owns no filesystem paths, so the
// constraint does not apply, and an archive that documents the whole tool
// is more useful than one documenting half of it — see
// TestArchivesShouldShipEveryManPage.
type manPageOwner struct {
	// packages are the goreleaser nfpm IDs that must install pages
	// matching this owner.
	packages []string
}

// manOwners assigns every page in docs/man to its artifacts.
// TestEveryManPageShouldBeAssigned fails when a page matches none of these,
// so a new cobra subcommand cannot add a page that silently ships nowhere.
var manOwners = map[string]manPageOwner{
	// The client's own pages: the root, one per subcommand, and the config
	// page. Windows and macOS archives share the client file list.
	"ssoossh": {packages: []string{"client"}},
	// The server's root and per-subcommand pages, plus its config page.
	"ssoosshd": {packages: serverPackageIDs},
	// Shipped by the PAM packages, configured in .goreleaser-pam-*.yml and
	// asserted by TestPAMPackagesShouldShipEveryPAMManPage.
	"pam_ssoossh": {},
}

// ownerFor returns the owner key for a man page filename.
func ownerFor(name string) string {
	switch {
	case strings.HasPrefix(name, "pam_ssoossh"):
		return "pam_ssoossh"
	// Checked before "ssoossh": every server page name starts with the
	// client's prefix too, so the order here is the whole discrimination.
	case strings.HasPrefix(name, "ssoosshd"):
		return "ssoosshd"
	case strings.HasPrefix(name, "ssoossh"):
		return "ssoossh"
	default:
		return ""
	}
}

// manPages lists every page in docs/man.
func manPages(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot(t), "docs", "man"))
	if err != nil {
		t.Fatalf("failed to read docs/man: %v", err)
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("docs/man is empty")
	}
	return names
}

// should assign every generated page to an artifact. gendocs writes one
// page per cobra subcommand, so adding a subcommand adds a page; without
// this, that page would simply never ship and nobody would notice until
// someone ran `man ssoossh-the-new-thing` on an installed host.
func TestEveryManPageShouldBeAssigned(t *testing.T) {
	t.Parallel()

	for _, name := range manPages(t) {
		if ownerFor(name) == "" {
			t.Errorf("docs/man/%s matches no owner in manOwners, so nothing ships it", name)
		}
	}
}

// should install every man page from every package that owns it. The
// packages used to ship the two root pages and the two config pages only,
// so `man ssoossh-ssh-login` — a page this repo generates and commits —
// failed on an installed host.
func TestPackagesShouldShipEveryManPageTheyOwn(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	// Collect the man sources each package installs, expanding globs the
	// way goreleaser will.
	installed := map[string]map[string]bool{}
	for _, pkg := range cfg.NFPMs {
		installed[pkg.ID] = map[string]bool{}
		for _, content := range pkg.Contents {
			if !strings.HasPrefix(content.Src, "docs/man/") {
				continue
			}
			for _, name := range expandManGlob(t, content.Src) {
				installed[pkg.ID][name] = true
				// The section comes from the page's own extension, not from
				// which binary owns it: the config pages are section 5 while
				// their command pages are 1 and 8, and a page filed under the
				// wrong section is one `man` cannot find.
				wantSection := "man" + name[strings.LastIndex(name, ".")+1:]
				if !strings.Contains(content.Dst, wantSection) {
					t.Errorf("nfpm %q installs %s into %q, want section %s",
						pkg.ID, name, content.Dst, wantSection)
				}
			}
		}
	}

	for _, name := range manPages(t) {
		owner := manOwners[ownerFor(name)]
		for _, id := range owner.packages {
			if !installed[id][name] {
				t.Errorf("nfpm %q does not install docs/man/%s", id, name)
			}
		}
	}
}

// should put every man page in every release archive. Unlike the packages,
// archives are not split by owner: nothing owns a path inside a tarball, so
// there is no co-installation hazard, and the pam page belongs there too
// even though its packages are built from a different config.
func TestArchivesShouldShipEveryManPage(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)
	if len(cfg.Archives) == 0 {
		t.Fatal(".goreleaser.yml declares no archives")
	}

	for _, archive := range cfg.Archives {
		included := map[string]bool{}
		for _, file := range archive.Files {
			if !strings.HasPrefix(file.Src, "docs/man/") {
				continue
			}
			for _, name := range expandManGlob(t, file.Src) {
				included[name] = true
			}
		}

		for _, name := range manPages(t) {
			if !included[name] {
				t.Errorf("archive %q does not include docs/man/%s", archive.ID, name)
			}
		}
	}
}

// should keep the client and server packages off each other's paths, so
// installing both on one host does not fail on a file both packages own.
func TestPackagesShouldNotShipAnotherPackagesManPages(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	for _, pkg := range cfg.NFPMs {
		for _, content := range pkg.Contents {
			if !strings.HasPrefix(content.Src, "docs/man/") {
				continue
			}
			for _, name := range expandManGlob(t, content.Src) {
				owner := manOwners[ownerFor(name)]
				if len(owner.packages) > 0 && !slices.Contains(owner.packages, pkg.ID) {
					t.Errorf("nfpm %q ships docs/man/%s, which belongs to %v; two packages owning one path breaks co-installation",
						pkg.ID, name, owner.packages)
				}
			}
		}
	}
}

// expandManGlob resolves a docs/man source — a literal path or a glob —
// to the page names it matches, so the tests check what will actually be
// packaged rather than the pattern that was written.
func expandManGlob(t *testing.T, src string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(repoRoot(t), src))
	if err != nil {
		t.Fatalf("bad man page pattern %q: %v", src, err)
	}
	if len(matches) == 0 {
		t.Errorf("man page source %q matches no file", src)
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, filepath.Base(match))
	}
	return names
}

// pamGoreleaserConfigs are the separate release configs the PAM module is
// built from — it is cgo and per-arch, so it does not live in the main one.
var pamGoreleaserConfigs = []string{".goreleaser-pam-amd64.yml", ".goreleaser-pam-arm64.yml"}

// should ship every pam_* man page from every PAM package. The page is
// owned by a config the main goreleaser file knows nothing about, so
// without this the pam entry in manOwners would assert nothing and a second
// PAM page could ship nowhere.
func TestPAMPackagesShouldShipEveryPAMManPage(t *testing.T) {
	t.Parallel()

	var wanted []string
	for _, name := range manPages(t) {
		if ownerFor(name) == "pam_ssoossh" {
			wanted = append(wanted, name)
		}
	}
	if len(wanted) == 0 {
		t.Fatal("no pam man pages found, but manOwners claims the PAM packages ship some")
	}

	for _, configName := range pamGoreleaserConfigs {
		path := filepath.Join(repoRoot(t), configName)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", configName, err)
		}

		var cfg goreleaserConfig
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("failed to parse %s: %v", configName, err)
		}
		if len(cfg.NFPMs) == 0 {
			t.Fatalf("%s declares no packages", configName)
		}

		for _, pkg := range cfg.NFPMs {
			installed := map[string]bool{}
			for _, content := range pkg.Contents {
				if !strings.HasPrefix(content.Src, "docs/man/") {
					continue
				}
				for _, name := range expandManGlob(t, content.Src) {
					installed[name] = true
				}
			}
			for _, name := range wanted {
				if !installed[name] {
					t.Errorf("%s package %q does not install docs/man/%s", configName, pkg.ID, name)
				}
			}
		}
	}
}

// muslBuildID is the goreleaser build the Alpine packages and the musl
// archives are cut from, and muslTarget is the zig target triple suffix it
// compiles against.
const (
	muslBuildID = "server-linux-musl-build"
	muslTarget  = "-linux-musl"
)

// should keep the musl server build cgo-enabled and dynamically linked.
// musl's static libc answers every dlopen with "Dynamic loading not
// supported", so a statically linked Alpine binary cannot load a PKCS#11
// module at all: the HSM signer would be dead weight in the package, and
// nothing about the binary would say so until an operator pointed it at a
// module. build.yaml checks the linkage of what goreleaser actually
// produced, but that job does not run on a pull request — this checks the
// config the pull request is changing.
func TestMuslServerBuildShouldLinkDynamically(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)

	var env []string
	found := false
	for _, build := range cfg.Builds {
		if build.ID == muslBuildID {
			found, env = true, build.Env
		}
	}
	if !found {
		t.Fatalf("no goreleaser build %q; the Alpine packages are built from it", muslBuildID)
	}

	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CGO_ENABLED=1") {
		t.Errorf("build %q does not set CGO_ENABLED=1, so it has no PKCS#11 support to link: %q", muslBuildID, joined)
	}

	targets := 0
	for rest := joined; ; {
		i := strings.Index(rest, muslTarget)
		if i < 0 {
			break
		}
		targets++
		rest = rest[i+len(muslTarget):]
		if !strings.HasPrefix(rest, " -dynamic") {
			t.Errorf("build %q compiles a musl target without -dynamic, which links libc statically: %q", muslBuildID, joined)
		}
	}
	if targets == 0 {
		t.Errorf("build %q compiles for no musl target at all: %q", muslBuildID, joined)
	}
}
