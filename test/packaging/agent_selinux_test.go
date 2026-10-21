package packaging_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The ssoosshd-selinux package: the policy module that lets
// ssoossh-agent.service exec ssh-agent on an enforcing host.
const (
	agentSELinuxID     = "server-selinux"
	agentSELinuxName   = "ssoosshd-selinux"
	agentSELinuxModule = "ssoossh_agent"
	agentSELinuxTE     = "packaging/selinux/ssoossh_agent.te"
	agentSELinuxPP     = "build/selinux/ssoossh_agent.pp"
	agentSELinuxDest   = "/usr/share/selinux/packages/ssoosshd/ssoossh_agent.pp"
	selinuxBuildScript = "scripts/build-selinux.sh"
)

// readRepoFile returns a file from the repository, failing the test when it
// cannot be read.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

// should recommend the agent's policy package from every rpm that ships
// the agent's unit, and from no deb or apk: those have no SELinux.
func TestServerPackagesShouldRecommendTheAgentSELinuxPolicyOnRPM(t *testing.T) {
	t.Parallel()

	for _, id := range serverPackageIDs {
		t.Run("should recommend "+agentSELinuxName+" from "+id, func(t *testing.T) {
			t.Parallel()
			pkg := findNFPM(t, id)
			if got := pkg.Overrides["rpm"].Recommends; !slices.Equal(got, []string{agentSELinuxName}) {
				t.Errorf("rpm recommends %q, want %s", got, agentSELinuxName)
			}
			for _, format := range []string{"deb", "apk"} {
				if got := pkg.Overrides[format].Recommends; len(got) != 0 {
					t.Errorf("%s recommends %q; there is no SELinux policy for it", format, got)
				}
			}
		})
	}
	if got := findNFPM(t, agentSELinuxID).PackageName; got != agentSELinuxName {
		t.Errorf("the policy package is named %q, but the server packages recommend %s", got, agentSELinuxName)
	}
}

// should package the compiled module as a noarch, rpm-only meta package
// that loads it at the vendor priority and unloads it from the same one,
// and ship the source beside it.
func TestAgentSELinuxPackageShouldLoadAndUnloadTheModule(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, agentSELinuxID)
	if !pkg.Meta {
		t.Error("the policy package is not a meta package, so it would carry every binary")
	}
	if !slices.Equal(pkg.Formats, []string{"rpm"}) {
		t.Errorf("formats are %q, want rpm only", pkg.Formats)
	}
	if !slices.Contains(pkg.Dependencies, "policycoreutils") {
		t.Errorf("dependencies %q lack policycoreutils, which ships semodule", pkg.Dependencies)
	}

	installs := map[string]string{}
	for _, c := range pkg.Contents {
		installs[c.Src] = c.Dst
	}
	contents := []struct {
		name string
		src  string
		dst  string
	}{
		{name: "should install the compiled module", src: agentSELinuxPP, dst: agentSELinuxDest},
		{name: "should ship the module source", src: agentSELinuxTE, dst: "/usr/share/doc/" + agentSELinuxName + "/ssoossh_agent.te"},
	}
	for _, tt := range contents {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := installs[tt.src]; got != tt.dst {
				t.Errorf("%s is installed at %q, want %q", tt.src, got, tt.dst)
			}
		})
	}

	scripts := []struct {
		name   string
		script string
		want   string
	}{
		{name: "should load the shipped module at the vendor priority", script: pkg.Scripts.PostInstall, want: `semodule -X 200 -i "$PP"`},
		{name: "should name the shipped module's path", script: pkg.Scripts.PostInstall, want: "PP=" + agentSELinuxDest + "\n"},
		{name: "should unload it from the same priority", script: pkg.Scripts.PreRemove, want: "semodule -X 200 -r " + agentSELinuxModule},
		{name: "should unload it on removal only, not on upgrade", script: pkg.Scripts.PreRemove, want: "case \"${1:-0}\" in\n0) ;;\n*) exit 0 ;;\nesac"},
	}
	for _, tt := range scripts {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.script == "" {
				t.Fatal("no script")
			}
			if body := readRepoFile(t, tt.script); !strings.Contains(body, tt.want) {
				t.Errorf("%s does not contain %q", tt.script, tt.want)
			}
		})
	}
}

// should never restart or start the agent from the package scripts: a
// restarted agent comes back without its key, and ssoosshd then fails
// every signature while looking healthy.
func TestAgentSELinuxScriptsShouldNotTouchTheAgentUnit(t *testing.T) {
	t.Parallel()

	pkg := findNFPM(t, agentSELinuxID)
	for _, script := range []string{pkg.Scripts.PostInstall, pkg.Scripts.PreRemove} {
		t.Run("should leave the unit alone in "+script, func(t *testing.T) {
			t.Parallel()
			for i, line := range strings.Split(readRepoFile(t, script), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				if strings.Contains(trimmed, "systemctl") {
					t.Errorf("%s line %d runs systemctl: %q", script, i+1, trimmed)
				}
			}
		})
	}
}

// agentAllowRule is the module's one allow rule, captured up to its
// permission set.
var agentAllowRule = regexp.MustCompile(`(?m)^allow init_t ssh_agent_exec_t:file \{([^}]*)\};$`)

// should grant init_t every permission the exec needs. Dropping execute,
// open or read brings back status=203/EXEC; dropping map brings back the
// SIGSEGV that leaves no trace but the AVC.
func TestAgentSELinuxSourceShouldGrantTheExec(t *testing.T) {
	t.Parallel()

	te := readRepoFile(t, agentSELinuxTE)
	m := agentAllowRule.FindStringSubmatch(te)
	if m == nil {
		t.Fatalf("%s has no allow init_t ssh_agent_exec_t:file rule", agentSELinuxTE)
	}
	granted := strings.Fields(m[1])
	for _, perm := range []string{"execute", "execute_no_trans", "open", "read", "getattr", "map"} {
		t.Run("should grant "+perm, func(t *testing.T) {
			t.Parallel()
			if !slices.Contains(granted, perm) {
				t.Errorf("the rule grants %q, not %s", granted, perm)
			}
		})
	}
}

// should keep the module name, the source file and the file the build
// writes in agreement: the refpolicy Makefile names the module after the
// .te file, checkmodule refuses an output named otherwise, and the package
// scripts name it as ssoossh_agent.
func TestAgentSELinuxSourceShouldDeclareTheModuleItIsNamedFor(t *testing.T) {
	t.Parallel()

	te := readRepoFile(t, agentSELinuxTE)
	if !strings.HasPrefix(te, "module "+agentSELinuxModule+" ") {
		t.Errorf("%s does not start with module %s", agentSELinuxTE, agentSELinuxModule)
	}
	if got := filepath.Base(agentSELinuxPP); got != agentSELinuxModule+".pp" {
		t.Errorf("the package takes %s, not %s.pp", got, agentSELinuxModule)
	}
}

// selinuxModulesLine is the build script's list of the modules it builds.
var selinuxModulesLine = regexp.MustCompile(`(?m)^modules="([^"]*)"$`)

// should build every policy source in packaging/selinux, so a module
// cannot ship as source with no package carrying it compiled.
func TestSELinuxBuildShouldBuildEveryModule(t *testing.T) {
	t.Parallel()

	m := selinuxModulesLine.FindStringSubmatch(readRepoFile(t, selinuxBuildScript))
	if m == nil {
		t.Fatalf("%s sets no modules=\"...\" list", selinuxBuildScript)
	}
	built := strings.Fields(m[1])

	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "packaging/selinux/*.te"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no policy sources in packaging/selinux: %v", err)
	}
	var want []string
	for _, src := range sources {
		want = append(want, strings.TrimSuffix(filepath.Base(src), ".te"))
	}
	slices.Sort(built)
	slices.Sort(want)
	if !slices.Equal(built, want) {
		t.Errorf("%s builds %q, but packaging/selinux holds %q", selinuxBuildScript, built, want)
	}
}
