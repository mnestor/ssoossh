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
	"regexp"
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
	NFPMs    []nfpmPackage `yaml:"nfpms"`
	Archives []struct {
		ID      string        `yaml:"id"`
		Formats []string      `yaml:"formats"`
		Files   []archiveFile `yaml:"files"`
	} `yaml:"archives"`
	Builds []goreleaserBuild `yaml:"builds"`
}

// nfpmPackage is one entry under nfpms:. Bindir and Scripts are read as
// well as Contents because where the binary lands and what runs at install
// time are both package behaviour these tests pin.
type nfpmPackage struct {
	ID          string        `yaml:"id"`
	PackageName string        `yaml:"package_name"`
	Meta        bool          `yaml:"meta"`
	Bindir      string        `yaml:"bindir"`
	Formats     []string      `yaml:"formats"`
	APK         nfpmAPK       `yaml:"apk"`
	Scripts     nfpmScripts   `yaml:"scripts"`
	Contents    []nfpmContent `yaml:"contents"`
}

// nfpmAPK carries the apk-only signing block. apk cannot use the OpenPGP
// key the deb and rpm are signed with, so this is a separate key entirely.
type nfpmAPK struct {
	Signature struct {
		KeyFile string `yaml:"key_file"`
		KeyName string `yaml:"key_name"`
	} `yaml:"signature"`
}

// nfpmScripts are the install-time hooks. Every one named here has to exist
// on disk and be executable, which is what TestNFPMScripts checks.
type nfpmScripts struct {
	PreInstall  string `yaml:"preinstall"`
	PostInstall string `yaml:"postinstall"`
	PreRemove   string `yaml:"preremove"`
	PostRemove  string `yaml:"postremove"`
}

// nfpmContent is one shipped file. Type distinguishes a real file from a
// symlink or a config, and FileInfo carries the ownership that makes the
// mapping file readable by the lookup account.
type nfpmContent struct {
	Src      string `yaml:"src"`
	Dst      string `yaml:"dst"`
	Type     string `yaml:"type"`
	FileInfo struct {
		Owner string `yaml:"owner"`
		Group string `yaml:"group"`
		Mode  any    `yaml:"mode"`
	} `yaml:"file_info"`
}

// goreleaserBuild is one entry under builds:. Flags carries the -tags
// argument, which is what decides whether crypto11 is compiled in at all.
type goreleaserBuild struct {
	ID    string   `yaml:"id"`
	Env   []string `yaml:"env"`
	Flags []string `yaml:"flags"`
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

	for _, name := range []string{".goreleaser.yml"} {
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
	serverPackageIDs = []string{"server", "server-pkcs11"}
	serverArchiveIDs = []string{"linux-server-archives", "linux-server-pkcs11-archives"}
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
}

// ownerFor returns the owner key for a man page filename.
func ownerFor(name string) string {
	switch {
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

// should put every man page in every real release archive. Unlike the
// packages, archives are not split by owner: nothing owns a path inside a
// tarball, so there is no co-installation hazard. "Real" excludes formats:
// binary entries -- goreleaser's raw binary format packages only the one
// binary, with no files: list at all, so it structurally cannot carry
// docs/man alongside it.
func TestArchivesShouldShipEveryManPage(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)
	if len(cfg.Archives) == 0 {
		t.Fatal(".goreleaser.yml declares no archives")
	}

	for _, archive := range cfg.Archives {
		if slices.Contains(archive.Formats, "binary") {
			continue
		}
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

// The two server builds and the one invariant that separates them: the
// default is cgo-free and the pkcs11 build is not. crypto11 is the only
// cgo dependency in ssoosshd, so that single difference is what makes the
// default binary static, gives it no libc floor, and lets its image sit on
// distroless/static.
const (
	defaultBuildID = "server-linux-build"
	pkcs11BuildID  = "server-linux-pkcs11-build"
	pkcs11Tag      = "hsm"
)

// buildByID returns the named goreleaser build, failing when it is absent.
func buildByID(t *testing.T, cfg goreleaserConfig, id string) goreleaserBuild {
	t.Helper()
	for _, build := range cfg.Builds {
		if build.ID == id {
			return build
		}
	}
	t.Fatalf("no goreleaser build %q", id)
	return goreleaserBuild{}
}

// should keep the default server build free of cgo. It is what makes the
// binary static, and a static binary is what removed the glibc/musl split
// from the packages and images: one artifact per architecture, no libc
// floor, and distroless/static as the image base. cgo creeping back in
// would reintroduce a libc requirement that nothing declares and nothing
// tests, and the binary would keep working on the build host either way.
//
// build.yaml checks the linkage of what goreleaser actually produced, but
// that job does not run on a pull request; this checks the config the pull
// request is changing.
func TestDefaultServerBuildShouldNotUseCgo(t *testing.T) {
	t.Parallel()

	build := buildByID(t, loadGoreleaser(t), defaultBuildID)

	joined := strings.Join(build.Env, "\n")
	if strings.Contains(joined, "CGO_ENABLED=1") {
		t.Errorf("build %q enables cgo, which makes it dynamically linked and gives it an undeclared libc floor: %q", defaultBuildID, joined)
	}
	if strings.Contains(strings.Join(build.Flags, " "), pkcs11Tag) {
		t.Errorf("build %q is compiled with the %q tag, which pulls in crypto11 and requires cgo: %q", defaultBuildID, pkcs11Tag, build.Flags)
	}
}

// should keep the pkcs11 server build cgo-enabled and tagged. Without cgo
// the crypto11 binding does not compile in; without the tag the file is not
// built at all, and NewHSMKeySource becomes the refusal stub. Either way the
// package would install a binary whose whole reason for existing is missing,
// and nothing about it would say so until an operator pointed it at a module.
func TestPKCS11ServerBuildShouldEnableCgoAndTheHSMTag(t *testing.T) {
	t.Parallel()

	build := buildByID(t, loadGoreleaser(t), pkcs11BuildID)

	joined := strings.Join(build.Env, "\n")
	if !strings.Contains(joined, "CGO_ENABLED=1") {
		t.Errorf("build %q does not set CGO_ENABLED=1, so it has no PKCS#11 support to link: %q", pkcs11BuildID, joined)
	}
	if !strings.Contains(strings.Join(build.Flags, " "), pkcs11Tag) {
		t.Errorf("build %q is not compiled with the %q tag, so hsmkeysource.go is excluded and hsm: config is refused at startup: %q", pkcs11BuildID, pkcs11Tag, build.Flags)
	}
}

// The packaging changes below are the kind that rot silently: nothing fails
// to build when a binary moves back under /usr/local, when a drop-in gains
// an active directive, or when a unit's ExecStart stops matching the path
// the package installs. These pin each one.

// should install binaries outside /usr/local, which the FHS reserves for
// software the package manager does not manage. The concrete failure is
// sshd's: it refuses an AuthorizedPrincipalsCommand whose path is writable
// by group or other, and /usr/local/bin is root:staff 2775 on Debian and
// Ubuntu, so every certificate login failed there.
func TestNFPMShouldInstallBinariesOutsideUsrLocal(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"client":        "/usr/bin",
		"server":        "/usr/sbin",
		"server-pkcs11": "/usr/sbin",
	}

	for _, pkg := range loadGoreleaser(t).NFPMs {
		// A meta package ships no binary, so it has no bindir to check.
		if pkg.Meta {
			continue
		}
		wantDir, ok := want[pkg.ID]
		if !ok {
			t.Errorf("nfpm %q is not covered by this test; add it", pkg.ID)
			continue
		}
		if pkg.Bindir != wantDir {
			t.Errorf("nfpm %q: bindir is %q, want %q", pkg.ID, pkg.Bindir, wantDir)
		}
	}
}

// should keep a compatibility symlink at each binary's former path, so an
// sshd_config line, cron entry or unit naming the old location survives the
// move. Scheduled for removal two releases after it was introduced; when it
// goes, this test goes with it.
func TestNFPMShouldShipCompatibilitySymlinks(t *testing.T) {
	t.Parallel()

	want := map[string]struct{ dst, src string }{
		"client":        {dst: "/usr/local/bin/ssoossh", src: "/usr/bin/ssoossh"},
		"server":        {dst: "/usr/local/sbin/ssoosshd", src: "/usr/sbin/ssoosshd"},
		"server-pkcs11": {dst: "/usr/local/sbin/ssoosshd", src: "/usr/sbin/ssoosshd"},
	}

	for _, pkg := range loadGoreleaser(t).NFPMs {
		link, ok := want[pkg.ID]
		if !ok {
			continue
		}
		found := false
		for _, c := range pkg.Contents {
			if c.Dst != link.dst {
				continue
			}
			found = true
			if c.Type != "symlink" {
				t.Errorf("nfpm %q: %s has type %q, want symlink", pkg.ID, c.Dst, c.Type)
			}
			if c.Src != link.src {
				t.Errorf("nfpm %q: %s points at %q, want %q", pkg.ID, c.Dst, c.Src, link.src)
			}
		}
		if !found {
			t.Errorf("nfpm %q: no compatibility symlink at %s", pkg.ID, link.dst)
		}
	}
}

// should name install hooks that actually exist and can be run. A scriptlet
// path that does not resolve is not a build failure — it is a package that
// ships without the hook it was supposed to carry.
func TestNFPMScriptsShouldExistAndBeExecutable(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, pkg := range loadGoreleaser(t).NFPMs {
		for hook, path := range map[string]string{
			"preinstall":  pkg.Scripts.PreInstall,
			"postinstall": pkg.Scripts.PostInstall,
			"preremove":   pkg.Scripts.PreRemove,
			"postremove":  pkg.Scripts.PostRemove,
		} {
			if path == "" {
				continue
			}
			info, err := os.Stat(filepath.Join(root, path))
			if err != nil {
				t.Errorf("nfpm %q %s: %v", pkg.ID, hook, err)
				continue
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Errorf("nfpm %q %s: %s is not executable (mode %v)", pkg.ID, hook, path, info.Mode().Perm())
			}
		}
	}
}

// should create the principals lookup account before any file naming it is
// unpacked. Both rpm and dpkg apply ownership as they unpack, so a group
// created in postinstall is too late and the mapping file lands owned by
// root — which fails silently, since the lookup then answers with the
// account name alone and exits 0.
func TestNFPMClientShouldCreateTheLookupAccountInPreinstall(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, "client")
	if pkg.Scripts.PreInstall == "" {
		t.Fatal("the client package has no preinstall hook")
	}

	body, err := os.ReadFile(filepath.Join(repoRoot(t), pkg.Scripts.PreInstall))
	if err != nil {
		t.Fatalf("read preinstall: %v", err)
	}
	if !strings.Contains(string(body), "ssoossh-principals") {
		t.Error("the client preinstall hook does not create ssoossh-principals")
	}
}

// should give the mapping file to the lookup account's group, read-only,
// with no access for anyone else. This is the pairing the deployer used to
// have to get right by hand, and getting it wrong is invisible.
func TestNFPMClientShouldOwnTheMappingFileByTheLookupGroup(t *testing.T) {
	t.Parallel()

	for _, c := range findNFPM(t, "client").Contents {
		if c.Dst != "/etc/ssoossh/principals.yaml" {
			continue
		}
		if c.FileInfo.Owner != "root" {
			t.Errorf("owner is %q, want root", c.FileInfo.Owner)
		}
		if c.FileInfo.Group != "ssoossh-principals" {
			t.Errorf("group is %q, want ssoossh-principals", c.FileInfo.Group)
		}
		if !strings.HasPrefix(c.Type, "config") {
			t.Errorf("type is %q, want a config type so operator edits survive upgrades", c.Type)
		}
		return
	}
	t.Error("the client package does not ship /etc/ssoossh/principals.yaml")
}

// should ship both SSH drop-ins completely inert. Installing the client
// package must not change how a machine accepts or makes SSH connections,
// so every directive in them is commented and the operator uncomments two
// lines to opt in.
func TestShippedDropInsShouldBeFullyCommented(t *testing.T) {
	t.Parallel()

	dropIns := []string{
		"packaging/linux/sshd_config.d/50-ssoossh.conf",
		"packaging/linux/ssh_config.d/50-ssoossh.conf",
	}
	for _, rel := range dropIns {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
			if err != nil {
				t.Fatalf("read drop-in: %v", err)
			}
			for i, line := range strings.Split(string(body), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				t.Errorf("line %d is an active directive: %q", i+1, trimmed)
			}
		})
	}
}

// should keep the units' ExecStart on the path the package installs. The
// unit and the bindir are two files that have to agree, and nothing else
// notices when they stop: the package installs cleanly and the service
// fails at start with a path that is no longer there.
func TestUnitsShouldExecTheInstalledBinaryPath(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ssoosshd.service"))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}

	bindir := findNFPM(t, "server").Bindir
	want := "ExecStart=" + bindir + "/ssoosshd"
	if !strings.Contains(string(body), want) {
		t.Errorf("deploy/ssoosshd.service does not ExecStart %s/ssoosshd (bindir is %q)", bindir, bindir)
	}
}

// should ship every unit that exists in deploy/, since a unit referenced by
// the install docs but absent from the package is one the operator has to
// write out by hand.
func TestNFPMServerShouldShipTheSystemdUnits(t *testing.T) {
	t.Parallel()

	want := []string{"deploy/ssoosshd.service", "deploy/ssoossh-agent.service"}
	for _, id := range []string{"server", "server-pkcs11"} {
		pkg := findNFPM(t, id)
		for _, unit := range want {
			found := false
			for _, c := range pkg.Contents {
				if c.Src == unit {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("nfpm %q does not ship %s", id, unit)
			}
		}
	}
}

// findNFPM returns the named package, failing the test when it is missing.
func findNFPM(t *testing.T, id string) nfpmPackage {
	t.Helper()

	for _, pkg := range loadGoreleaser(t).NFPMs {
		if pkg.ID == id {
			return pkg
		}
	}
	t.Fatalf("no nfpm package with id %q", id)
	return nfpmPackage{}
}

// repoHost is the one hostname the repository definitions may name. It is
// baked into every installed /etc/yum.repos.d/ssoossh.repo and
// /etc/apt/sources.list.d/ssoossh.sources and cannot be changed for hosts
// that already have it, so it is pinned here rather than left to a typo.
const repoHost = "packages.mikenestor.org"

// should carry no binary. ssoossh-release exists only to place a repository
// definition and a key; a build id leaking into it would ship a second copy
// of the client at a path nothing expects.
func TestNFPMReleasePackagesShouldBeMetaPackages(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"release-rpm", "release-deb"} {
		if pkg := findNFPM(t, id); !pkg.Meta {
			t.Errorf("nfpm %q is not a meta package", id)
		}
	}
}

// should present one package name on both distributions, so the documented
// install line does not have to branch even though the shipped files do.
func TestNFPMReleasePackagesShouldShareOneName(t *testing.T) {
	t.Parallel()

	rpm := findNFPM(t, "release-rpm").PackageName
	if deb := findNFPM(t, "release-deb").PackageName; deb != rpm {
		t.Errorf("release packages are named %q and %q; they must match", rpm, deb)
	}
}

// should point apt's Signed-By at the exact path the same package installs
// the keyring to. These are two strings in two files that must agree, and
// when they do not apt rejects the whole repository -- with a message about
// a missing keyring, not about this package.
func TestReleaseDebSignedByShouldMatchTheShippedKeyringPath(t *testing.T) {
	t.Parallel()

	var keyringDst string
	for _, c := range findNFPM(t, "release-deb").Contents {
		if strings.HasSuffix(c.Dst, ".gpg") {
			keyringDst = c.Dst
		}
	}
	if keyringDst == "" {
		t.Fatal("the release deb ships no keyring")
	}

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging/linux/repo/ssoossh.sources"))
	if err != nil {
		t.Fatalf("read sources: %v", err)
	}

	want := "Signed-By: " + keyringDst
	if !strings.Contains(string(body), want) {
		t.Errorf("ssoossh.sources does not carry %q", want)
	}
}

// should verify both the packages and the repository metadata. gpgcheck
// alone leaves signed packages advertised by metadata nobody signed, which
// is the half of the guarantee people forget.
func TestReleaseRepoShouldEnableBothGPGChecks(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging/linux/repo/ssoossh.repo"))
	if err != nil {
		t.Fatalf("read repo file: %v", err)
	}
	for _, want := range []string{"gpgcheck=1", "repo_gpgcheck=1", "enabled=1"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("ssoossh.repo does not set %s", want)
		}
	}
}

// should name only the pinned hostname, in both definitions.
func TestRepoDefinitionsShouldNameThePinnedHost(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{
		"packaging/linux/repo/ssoossh.repo",
		"packaging/linux/repo/ssoossh.sources",
	} {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !strings.Contains(string(body), repoHost) {
				t.Errorf("does not name %s", repoHost)
			}
			// r2.dev is rate limited and explicitly not for production, and
			// a baseurl is not temporary once it is installed.
			if strings.Contains(string(body), "r2.dev") {
				t.Error("names an r2.dev URL; the repository must be reached through its own hostname")
			}
		})
	}
}

// should keep the yum baseurl per EL major. The variants of pam-ssoossh are
// one package name at distinct NEVRAs, so a single flat tree would offer an
// EL8 host a package built against an OpenSSL it does not have.
func TestReleaseRepoShouldUseReleaseverInTheBaseurl(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging/linux/repo/ssoossh.repo"))
	if err != nil {
		t.Fatalf("read repo file: %v", err)
	}
	if !strings.Contains(string(body), "$releasever") {
		t.Error("the yum baseurl does not vary by $releasever")
	}
}

// should sign every apk it builds, with an explicit key name.
//
// Two separate traps. The apk format cannot use the OpenPGP key the deb and
// rpm are signed with -- it takes a bare RSA key -- so an apk is silently
// unsigned unless its own signature block is present, which is how ssoossh
// shipped an unsigned .apk until this was wired. And nfpm only requires
// key_name implicitly: left unset it falls back to parsing `maintainer` as
// a mail address, which for a bare name fails the build outright.
func TestNFPMShouldSignEveryAPKItBuilds(t *testing.T) {
	t.Parallel()

	for _, pkg := range loadGoreleaser(t).NFPMs {
		if !slices.Contains(pkg.Formats, "apk") {
			continue
		}
		if pkg.APK.Signature.KeyFile == "" {
			t.Errorf("nfpm %q builds an apk with no apk.signature.key_file; it would ship unsigned", pkg.ID)
		}
		if pkg.APK.Signature.KeyName == "" {
			t.Errorf("nfpm %q builds an apk with no apk.signature.key_name; nfpm would fall back to parsing the maintainer as a mail address", pkg.ID)
		}
	}
}

// should publish the public half of the apk key with the release. A host
// installing the .apk by direct download verifies against
// /etc/apk/keys/<key_name>.rsa.pub and has nowhere else to obtain it.
func TestReleaseShouldPublishTheAPKPublicKey(t *testing.T) {
	t.Parallel()

	var keyName string
	for _, pkg := range loadGoreleaser(t).NFPMs {
		if slices.Contains(pkg.Formats, "apk") && pkg.APK.Signature.KeyName != "" {
			keyName = pkg.APK.Signature.KeyName
		}
	}
	if keyName == "" {
		t.Skip("no signed apk is built")
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	want := "build/" + keyName + ".rsa.pub"
	if !strings.Contains(string(raw), want) {
		t.Errorf("the release does not publish %s, so an apk installed by direct download cannot be verified", want)
	}
}

// Signing keys are written into the checkout so nfpm and gpg can reach them
// by a workspace-relative path. That makes the tree dirty, which goreleaser
// refuses to release from, and it puts a private key one `git add -A` away
// from being committed. Both are avoided by the same line in .gitignore, so
// the rule is derived from the workflow rather than listed here: whatever
// the build writes into the workspace root must be ignored.
func TestWorkflowSecretsWrittenIntoTheCheckoutShouldBeGitignored(t *testing.T) {
	root := repoRoot(t)

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "build.yaml"))
	if err != nil {
		t.Fatalf("read build workflow: %v", err)
	}
	ignores, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	// Matches the redirection the "Save ... key" steps use, e.g.
	//   op read "op://..." > "${GITHUB_WORKSPACE}/apk.pem"
	written := regexp.MustCompile(`>\s*"\$\{GITHUB_WORKSPACE\}/([^"/]+)"`)
	matches := written.FindAllStringSubmatch(string(workflow), -1)
	if len(matches) == 0 {
		t.Fatal("no workspace-root writes found in the workflow; this test has stopped watching anything")
	}

	ignored := make(map[string]bool)
	for _, line := range strings.Split(string(ignores), "\n") {
		ignored[strings.TrimSpace(line)] = true
	}

	for _, m := range matches {
		name := m[1]
		t.Run(name, func(t *testing.T) {
			if !ignored["/"+name] {
				t.Errorf("the build writes %s into the checkout but .gitignore has no /%s entry: "+
					"goreleaser will refuse to release from the dirty tree, and the key can be committed by accident", name, name)
			}
		})
	}
}

// Uploading to R2 is the only irreversible step in the release:
// publish-repo.sh never deletes, so anything that reaches the bucket is
// there for good. It therefore has to wait for every reversible verdict --
// the Mac and Windows checks, and the flip of the draft to a published
// release -- or the repository can end up serving signed metadata for a
// version whose release page was deleted when a notary rejected it.
//
// The rule is structural and easy to lose in a refactor: whichever job runs
// the upload must depend on the job that publishes the release.
func TestRepositoryUploadShouldWaitForThePublishedRelease(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "build.yaml"))
	if err != nil {
		t.Fatalf("read build workflow: %v", err)
	}

	var wf struct {
		Jobs map[string]struct {
			Needs jobNeeds `yaml:"needs"`
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse build workflow: %v", err)
	}

	const uploader = "publish-repo.sh"
	found := false
	for name, job := range wf.Jobs {
		runsUpload := false
		for _, step := range job.Steps {
			if strings.Contains(step.Run, uploader) {
				runsUpload = true
				break
			}
		}
		if !runsUpload {
			continue
		}
		found = true

		if !slices.Contains(job.Needs, "publish") {
			t.Errorf("job %q uploads the repository but does not need the publish job (needs: %v): "+
				"packages would reach the bucket before the release is published, and nothing deletes them again",
				name, job.Needs)
		}
	}

	if !found {
		t.Fatalf("no job runs %s; this test has stopped watching anything", uploader)
	}
}

// jobNeeds reads a workflow job's `needs`, which GitHub accepts as either a
// single job name or a list of them.
type jobNeeds []string

func (n *jobNeeds) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var one string
		if err := node.Decode(&one); err != nil {
			return err
		}
		*n = jobNeeds{one}
		return nil
	}
	var many []string
	if err := node.Decode(&many); err != nil {
		return err
	}
	*n = many
	return nil
}
