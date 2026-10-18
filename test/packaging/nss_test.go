package packaging_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// nssExample is the example configuration the ssoosshd-nss package ships as
// /etc/ssoossh/nss.yaml. It lists every option at its default, commented
// out, and nssd/config holds the defaults themselves: the tests below keep
// the two from drifting apart.
const nssExample = "packaging/linux/nss.yaml"

// nssOptionLine is an option line in the example: "#name: value", with no
// space after the "#". Prose comments start "# ", so the two never mix.
var nssOptionLine = regexp.MustCompile(`^#([a-z_]+):(.*)$`)

// nssExampleOptions returns the example's option lines, uncommented, and the
// option names in the order they appear.
func nssExampleOptions(t *testing.T) (string, []string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), nssExample))
	if err != nil {
		t.Fatalf("read %s: %v", nssExample, err)
	}
	var lines, names []string
	for _, line := range strings.Split(string(body), "\n") {
		m := nssOptionLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names = append(names, m[1])
		lines = append(lines, m[1]+":"+m[2])
	}
	return strings.Join(lines, "\n") + "\n", names
}

// nssConfigKeys returns every option nss.yaml accepts: the mapstructure tag
// of each config.Config field the file can set.
func nssConfigKeys() []string {
	var keys []string
	typ := reflect.TypeFor[config.Config]()
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("mapstructure")
		if tag != "" && tag != "-" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// should list every nss.yaml option in the example exactly once, so an
// option added to nssd/config cannot ship undocumented.
func TestNSSExampleShouldListEveryOptionOnce(t *testing.T) {
	t.Parallel()

	_, names := nssExampleOptions(t)
	for _, key := range nssConfigKeys() {
		if n := countOf(names, key); n != 1 {
			t.Errorf("%s lists %q %d times, want once", nssExample, key, n)
		}
	}
	for _, name := range names {
		if !slices.Contains(nssConfigKeys(), name) {
			t.Errorf("%s lists %q, which nss.yaml does not accept", nssExample, name)
		}
	}
}

// countOf counts the occurrences of s in list.
func countOf(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// should give every option the value the daemon uses when the option is
// absent, so the example never documents a default the binary does not
// have. The example is loaded by the daemon's own loader, uncommented,
// with only the two options that have no default (the server and a TLS
// trust root) filled in.
func TestNSSExampleShouldMatchTheDaemonDefaults(t *testing.T) {
	t.Parallel()

	options, _ := nssExampleOptions(t)
	const server, pin = "https://sso.example.test", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	options = strings.Replace(options, "server: \"\"\n", "server: \""+server+"\"\n", 1)
	options = strings.Replace(options, "tls_spki_pin: []\n", "tls_spki_pin: [\""+pin+"\"]\n", 1)

	path := filepath.Join(t.TempDir(), "nss.yaml")
	if err := os.WriteFile(path, []byte(options), 0o600); err != nil {
		t.Fatalf("write the uncommented example: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("the uncommented example does not load: %v", err)
	}

	want := config.Defaults()
	want.Server, want.TLSSPKIPin, want.Domain = server, []string{pin}, got.Domain
	for _, diff := range configDiff(*got, want) {
		t.Errorf("%s: %s", nssExample, diff)
	}
}

// configDiff lists, by option name, every field where got and want differ.
// An empty slice or map equals a nil one: "allowed_shells: []" in the
// example is the same setting as the default's nil.
func configDiff(got, want config.Config) []string {
	var diffs []string
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	typ := gv.Type()
	for i := range typ.NumField() {
		g, w := gv.Field(i), wv.Field(i)
		if isEmptyCollection(g) && isEmptyCollection(w) {
			continue
		}
		if !reflect.DeepEqual(g.Interface(), w.Interface()) {
			name := typ.Field(i).Tag.Get("mapstructure")
			diffs = append(diffs, fmt.Sprintf("%s is %v in the example, the daemon's default is %v", name, g.Interface(), w.Interface()))
		}
	}
	return diffs
}

// isEmptyCollection reports whether v is a nil or empty slice or map.
func isEmptyCollection(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	default:
		return false
	}
}

// should leave every option commented out, so installing the package
// changes nothing until an administrator decides what the host trusts.
func TestNSSExampleShouldSetNothing(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), nssExample))
	if err != nil {
		t.Fatalf("read %s: %v", nssExample, err)
	}
	for i, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			t.Errorf("%s line %d is an active setting: %q", nssExample, i+1, trimmed)
		}
	}
}

// The NSS daemon's goreleaser build and nfpm packages.
const (
	nssBuildID      = "nss-linux-build"
	nssPackageID    = "nss"
	nssSELinuxID    = "nss-selinux"
	nssSELinuxPP    = "build/selinux/ssoossh_nss.pp"
	nssSELinuxDest  = "/usr/share/selinux/packages/ssoossh-nss/ssoossh_nss.pp"
	nssSELinuxPrior = "-X 200"
)

// should build the daemon for both Linux architectures with cgo off and
// with the server's FIPS 140-3 module. Neither is set on the build itself:
// both come from the global env, which the default server build inherits
// too, so a build-level override is exactly the drift this catches.
func TestNSSBuildShouldMatchTheServerBuildEnvironment(t *testing.T) {
	t.Parallel()

	cfg := loadGoreleaser(t)
	build := buildByID(t, cfg, nssBuildID)
	server := buildByID(t, cfg, defaultBuildID)

	tests := []struct {
		name string
		ok   bool
		msg  string
	}{
		{name: "should build cmd/ssoosshd-nss", ok: build.Dir == "./cmd/ssoosshd-nss", msg: "dir is " + build.Dir},
		{name: "should build for linux only", ok: slices.Equal(build.Goos, []string{"linux"}), msg: fmt.Sprint("goos ", build.Goos)},
		{name: "should build amd64 and arm64", ok: slices.Equal(build.Goarch, []string{"amd64", "arm64"}), msg: fmt.Sprint("goarch ", build.Goarch)},
		{name: "should not override the environment", ok: len(build.Env) == 0, msg: fmt.Sprint("env ", build.Env)},
		{name: "should share the server build's environment", ok: len(server.Env) == 0, msg: fmt.Sprint("server env ", server.Env)},
		{name: "should inherit CGO_ENABLED=0", ok: slices.Contains(cfg.Env, "CGO_ENABLED=0"), msg: fmt.Sprint("global env ", cfg.Env)},
		{name: "should inherit a pinned GOFIPS140", ok: slices.ContainsFunc(cfg.Env, func(e string) bool {
			return strings.HasPrefix(e, "GOFIPS140=v")
		}), msg: fmt.Sprint("global env ", cfg.Env)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !tt.ok {
				t.Errorf("build %q: %s", nssBuildID, tt.msg)
			}
		})
	}
}

// should declare the platform floor in each format's own spelling: the
// systemd that brought userdb, and whatever ships nss-systemd. The rpm side
// names the soname capability, which resolves on every rpm family, and
// never a file path, which EL10's dnf cannot resolve without filelists.
func TestNSSPackageShouldDependOnSystemdUserdb(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, nssPackageID)
	tests := []struct {
		name   string
		format string
		want   []string
	}{
		{name: "should require libnss-systemd and systemd 245 on deb", format: "deb", want: []string{"libnss-systemd", "systemd (>= 245)"}},
		{name: "should require the nss-systemd soname and systemd 245 on rpm", format: "rpm", want: []string{"libnss_systemd.so.2()(64bit)", "systemd >= 245"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pkg.Overrides[tt.format].Dependencies
			if !slices.Equal(got, tt.want) {
				t.Errorf("%s dependencies are %q, want %q", tt.format, got, tt.want)
			}
			for _, dep := range got {
				if strings.HasPrefix(dep, "/") {
					t.Errorf("%s depends on the file %q; name a package or capability", tt.format, dep)
				}
			}
		})
	}
	if len(pkg.Dependencies) != 0 {
		t.Errorf("format-independent dependencies %q: the deb and rpm names differ, so each belongs under overrides", pkg.Dependencies)
	}
}

// should recommend the policy package on rpm and nowhere else.
func TestNSSPackageShouldRecommendTheSELinuxPolicyOnRPM(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, nssPackageID)
	if got := pkg.Overrides["rpm"].Recommends; !slices.Equal(got, []string{"ssoossh-nss-selinux"}) {
		t.Errorf("rpm recommends %q, want ssoossh-nss-selinux", got)
	}
	if got := pkg.Overrides["deb"].Recommends; len(got) != 0 {
		t.Errorf("deb recommends %q; there is no SELinux policy for Debian", got)
	}
	if want := findNFPM(t, nssSELinuxID).PackageName; want != "ssoossh-nss-selinux" {
		t.Errorf("the policy package is named %q, but the daemon recommends ssoossh-nss-selinux", want)
	}
}

// should ship deb and rpm only: musl cannot load NSS modules and Alpine
// has no systemd, so an apk would install a daemon nothing can use.
func TestNSSPackageShouldNotBuildAnAPK(t *testing.T) {
	t.Parallel()

	if formats := findNFPM(t, nssPackageID).Formats; !slices.Equal(formats, []string{"rpm", "deb"}) {
		t.Errorf("formats are %q, want rpm and deb", formats)
	}
}

// should ship the units, the example configuration root-only and
// noreplace, and the man pages, and never the fleet key.
func TestNSSPackageShouldShipItsFiles(t *testing.T) {
	t.Parallel()

	byDst := map[string]nfpmContent{}
	for _, c := range findNFPM(t, nssPackageID).Contents {
		byDst[c.Dst] = c
	}
	tests := []struct {
		name string
		dst  string
		src  string
		typ  string
		mode int
	}{
		{name: "should ship the socket unit", dst: "/usr/lib/systemd/system/ssoosshd-nss.socket", src: "deploy/ssoosshd-nss.socket", mode: 0o644},
		{name: "should ship the service unit", dst: "/usr/lib/systemd/system/ssoosshd-nss.service", src: "deploy/ssoosshd-nss.service", mode: 0o644},
		{name: "should ship the example configuration root-only", dst: "/etc/ssoossh/nss.yaml", src: nssExample, typ: "config|noreplace", mode: 0o600},
		{name: "should ship the man pages", dst: "/usr/share/man/man8/", src: "docs/man/ssoosshd-nss*.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, ok := byDst[tt.dst]
			if !ok {
				t.Fatalf("nothing is installed at %s", tt.dst)
			}
			if c.Src != tt.src {
				t.Errorf("%s comes from %q, want %q", tt.dst, c.Src, tt.src)
			}
			if c.Type != tt.typ {
				t.Errorf("%s has type %q, want %q", tt.dst, c.Type, tt.typ)
			}
			if tt.mode != 0 && c.FileInfo.Mode != tt.mode {
				t.Errorf("%s has mode %v, want %#o", tt.dst, c.FileInfo.Mode, tt.mode)
			}
		})
	}
	if _, ok := byDst["/etc/ssoossh/nss.key"]; ok {
		t.Error("the package ships /etc/ssoossh/nss.key; the fleet key is an administrator's to write")
	}
}

// should keep the units' ExecStart on the path the package installs the
// daemon to, and the socket on the path the daemon's code names.
func TestNSSUnitsShouldNameTheInstalledPaths(t *testing.T) {
	t.Parallel()

	service, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ssoosshd-nss.service"))
	if err != nil {
		t.Fatalf("read the service unit: %v", err)
	}
	socket, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ssoosshd-nss.socket"))
	if err != nil {
		t.Fatalf("read the socket unit: %v", err)
	}
	bindir := findNFPM(t, nssPackageID).Bindir
	tests := []struct {
		name string
		unit []byte
		want string
	}{
		{name: "should exec the installed binary", unit: service, want: "ExecStart=" + bindir + "/ssoosshd-nss run\n"},
		{name: "should listen on the userdb socket", unit: socket, want: "ListenStream=" + config.UserdbSocket + "\n"},
		{name: "should keep the daemon out of its own lookups", unit: service, want: "Environment=SYSTEMD_BYPASS_USERDB=com.github.mnestor.ssoossh\n"},
		{name: "should own the admin socket's directory", unit: service, want: "RuntimeDirectory=" + filepath.Base(filepath.Dir(config.DefaultAdminSocket)) + "\n"},
		{name: "should own the cache's directory", unit: service, want: "StateDirectory=" + filepath.Base(filepath.Dir(config.DefaultCachePath)) + "\n"},
		{name: "should hold no capability", unit: service, want: "CapabilityBoundingSet=\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(string(tt.unit), tt.want) {
				t.Errorf("unit lacks %q", strings.TrimSpace(tt.want))
			}
		})
	}
}

// should only reload systemd on install: never enable a unit and never
// touch nsswitch.conf, both of which are the administrator's decisions.
func TestNSSPostinstallShouldOnlyReloadSystemd(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, nssPackageID)
	body, err := os.ReadFile(filepath.Join(repoRoot(t), pkg.Scripts.PostInstall))
	if err != nil {
		t.Fatalf("read postinstall: %v", err)
	}
	var commands []string
	for _, line := range strings.Split(string(body), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			commands = append(commands, trimmed)
		}
	}
	for _, line := range commands {
		for _, banned := range []string{"enable", "start", "nsswitch", "sed ", "authselect"} {
			if strings.Contains(line, banned) {
				t.Errorf("postinstall runs %q, which contains %q", line, banned)
			}
		}
	}
	if !strings.Contains(strings.Join(commands, "\n"), "systemctl daemon-reload") {
		t.Error("postinstall does not run systemctl daemon-reload")
	}
}

// should package the compiled policy module as a noarch, rpm-only meta
// package that loads it at the vendor priority and unloads it from the
// same one.
func TestNSSSELinuxPackageShouldLoadAndUnloadTheModule(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, nssSELinuxID)
	if !pkg.Meta {
		t.Error("the policy package is not a meta package, so it would carry every binary")
	}
	if !slices.Equal(pkg.Formats, []string{"rpm"}) {
		t.Errorf("formats are %q, want rpm only", pkg.Formats)
	}
	found := false
	for _, c := range pkg.Contents {
		if c.Src == nssSELinuxPP && c.Dst == nssSELinuxDest {
			found = true
		}
	}
	if !found {
		t.Errorf("the policy package does not install %s at %s", nssSELinuxPP, nssSELinuxDest)
	}

	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{name: "should load the shipped module at the vendor priority", script: pkg.Scripts.PostInstall, want: []string{"semodule " + nssSELinuxPrior + " -i", nssSELinuxDest}},
		{name: "should unload it from the same priority", script: pkg.Scripts.PreRemove, want: []string{"semodule " + nssSELinuxPrior + " -r ssoossh_nss"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.script == "" {
				t.Fatal("no script")
			}
			body, err := os.ReadFile(filepath.Join(repoRoot(t), tt.script))
			if err != nil {
				t.Fatalf("read %s: %v", tt.script, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(body), want) {
					t.Errorf("%s does not contain %q", tt.script, want)
				}
			}
		})
	}
}

// should keep the module name, the policy source and the file the build
// writes in agreement: the refpolicy Makefile names the module after the
// .te file, and the package scripts name it as ssoossh_nss.
func TestNSSSELinuxSourceShouldDeclareTheModuleItIsNamedFor(t *testing.T) {
	t.Parallel()

	te, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging/selinux/ssoossh_nss.te"))
	if err != nil {
		t.Fatalf("read the policy source: %v", err)
	}
	if !strings.HasPrefix(string(te), "policy_module(ssoossh_nss, ") {
		t.Error("ssoossh_nss.te does not start with policy_module(ssoossh_nss, ...)")
	}
	if filepath.Base(nssSELinuxPP) != "ssoossh_nss.pp" {
		t.Errorf("the package takes %s, not ssoossh_nss.pp", nssSELinuxPP)
	}
}

// should keep the client's copy of the RefreshUser contract equal to the
// daemon's. The client may not import nssd, so it spells the socket and
// the method out, and this is what stops the two drifting apart.
func TestNSSClientRefreshShouldNameTheDaemonsSocketAndMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "should dial the daemon's userdb socket", file: "client/cmd/host_refresh.go", want: `nssUserdbSocket = "` + config.UserdbSocket + `"`},
		{name: "should call the daemon's RefreshUser method", file: "client/cmd/host_refresh_linux.go", want: `nssRefreshMethod = "` + userdb.MethodRefreshUser + `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(filepath.Join(repoRoot(t), tt.file))
			if err != nil {
				t.Fatalf("read %s: %v", tt.file, err)
			}
			if !strings.Contains(string(src), tt.want) {
				t.Errorf("%s lacks %s", tt.file, tt.want)
			}
		})
	}
}

// should give up on a daemon that cannot start within the start limit's
// window: while the service fails and restarts, every lookup on the host
// waits behind the socket, local users included, and only the start limit
// failing the socket turns those waits into a quick not-found.
func TestNSSServiceShouldReachItsStartLimitWhenTheDaemonCannotStart(t *testing.T) {
	t.Parallel()

	service, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/ssoosshd-nss.service"))
	if err != nil {
		t.Fatalf("read the service unit: %v", err)
	}
	value := func(key string) string {
		m := regexp.MustCompile(`(?m)^` + key + `=(\S+)$`).FindSubmatch(service)
		if m == nil {
			t.Fatalf("the service unit sets no %s", key)
		}
		return string(m[1])
	}
	restart, err := time.ParseDuration(value("RestartSec"))
	if err != nil {
		t.Fatalf("RestartSec: %v", err)
	}
	window, err := time.ParseDuration(value("StartLimitIntervalSec"))
	if err != nil {
		t.Fatalf("StartLimitIntervalSec: %v", err)
	}
	burst, err := strconv.Atoi(value("StartLimitBurst"))
	if err != nil {
		t.Fatalf("StartLimitBurst: %v", err)
	}
	if took := time.Duration(burst) * restart; took >= window {
		t.Errorf("%d starts %v apart take %v, not under the %v window: a failing daemon never reaches the limit", burst, restart, took, window)
	}
}
