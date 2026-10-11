//go:build unix

package cmd

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes an executable shell script standing in for sshd, so
// SSHDConfig can be exercised without one installed.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sshd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil { //nolint:gosec // an executable test script in the test's own temp dir
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestOSHostSystemSSHDConfig(t *testing.T) {
	t.Parallel()

	t.Run("should return what sshd -T prints", func(t *testing.T) {
		t.Parallel()
		sshd := writeScript(t, `echo "pubkeyauthentication yes"`+"\n")
		out, err := osHostSystem{}.SSHDConfig(t.Context(), sshd, "")
		if err != nil || strings.TrimSpace(string(out)) != "pubkeyauthentication yes" {
			t.Errorf("SSHDConfig() = %q, %v; want sshd's output", out, err)
		}
	})

	t.Run("should pass the connection spec with -C", func(t *testing.T) {
		t.Parallel()
		sshd := writeScript(t, `echo "$@"`+"\n")
		out, _ := osHostSystem{}.SSHDConfig(t.Context(), sshd, "user=deploy")
		if got := strings.TrimSpace(string(out)); got != "-T -C user=deploy" {
			t.Errorf("sshd was run with %q, want %q", got, "-T -C user=deploy")
		}
	})

	t.Run("should report sshd's stderr when it fails", func(t *testing.T) {
		t.Parallel()
		sshd := writeScript(t, "echo 'sshd: no hostkeys available -- exiting.' >&2\nexit 1\n")
		_, err := osHostSystem{}.SSHDConfig(t.Context(), sshd, "")
		if err == nil || !strings.Contains(err.Error(), "no hostkeys available") {
			t.Errorf("SSHDConfig() error = %v, want sshd's own message", err)
		}
	})

	t.Run("should report the exit when sshd fails silently", func(t *testing.T) {
		t.Parallel()
		sshd := writeScript(t, "exit 3\n")
		if _, err := (osHostSystem{}).SSHDConfig(t.Context(), sshd, ""); err == nil {
			t.Error("SSHDConfig() error = nil, want the exit status")
		}
	})
}

// Not parallel: it changes PATH, which is process-wide.
func TestResolveSSHDPathShouldPreferSSHDOnPath(t *testing.T) {
	sshd := writeScript(t, "exit 0\n")
	t.Setenv("PATH", filepath.Dir(sshd))
	if got := resolveSSHDPath(""); got != sshd {
		t.Errorf("resolveSSHDPath() = %q, want %q", got, sshd)
	}
}

func TestOSHostSystemStat(t *testing.T) {
	t.Parallel()

	t.Run("should report the file's numeric owner", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "ca.pub")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		st, err := osHostSystem{}.Stat(path)
		if err != nil || !st.ownerKnown || st.uid != uint32(os.Getuid()) { //nolint:gosec // a uid fits in uint32 on every Unix
			t.Errorf("Stat() = %+v, %v; want owner uid %d", st, err, os.Getuid())
		}
	})

	t.Run("should fail for a missing file", func(t *testing.T) {
		t.Parallel()
		if _, err := (osHostSystem{}).Stat(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Error("Stat() error = nil, want an error")
		}
	})
}

func TestOSHostSystemLookupUser(t *testing.T) {
	t.Parallel()

	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user to look up: %v", err)
	}

	t.Run("should resolve an existing account with its groups", func(t *testing.T) {
		t.Parallel()
		u, err := osHostSystem{}.LookupUser(me.Username)
		if err != nil || u.uid != uint32(os.Getuid()) || len(u.gids) == 0 { //nolint:gosec // a uid fits in uint32 on every Unix
			t.Errorf("LookupUser(%q) = %+v, %v; want uid %d with groups", me.Username, u, err, os.Getuid())
		}
	})

	t.Run("should fail for an account that does not exist", func(t *testing.T) {
		t.Parallel()
		if _, err := (osHostSystem{}).LookupUser("ssoossh-no-such-account"); err == nil {
			t.Error("LookupUser() error = nil, want an error")
		}
	})
}

func TestOSHostSystemPrivileged(t *testing.T) {
	t.Parallel()
	if got, want := (osHostSystem{}).Privileged(), os.Geteuid() == 0; got != want {
		t.Errorf("Privileged() = %v, want %v", got, want)
	}
}

func TestParseNTPSynchronized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		out               string
		err               error
		wantSync, wantKno bool
	}{
		{name: "should report a synchronized clock", out: "yes\n", wantSync: true, wantKno: true},
		{name: "should report an unsynchronized clock", out: "no\n", wantKno: true},
		{name: "should report unknown when timedatectl fails", err: errors.New("exec: \"timedatectl\": executable file not found in $PATH")},
		{name: "should report unknown for an answer it does not recognize", out: "maybe\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synced, known := parseNTPSynchronized([]byte(tt.out), tt.err)
			if synced != tt.wantSync || known != tt.wantKno {
				t.Errorf("parseNTPSynchronized() = %v, %v; want %v, %v", synced, known, tt.wantSync, tt.wantKno)
			}
		})
	}
}

// should run end to end through the real command tree and exit non-zero on
// a failed check. sshd is a script, so what the host itself has installed
// does not matter: PubkeyAuthentication no is a failure on any host.
func TestExecuteHostCheckShouldFailWhenACheckFails(t *testing.T) {
	sshd := writeScript(t, "echo 'pubkeyauthentication no'\necho 'trustedusercakeys none'\n")
	fx := newTestExec(t)
	_, err := fx.exec.Execute(t.Context(), []string{"host", "check", "--sshd", sshd})
	if err == nil || !strings.Contains(err.Error(), "check(s) failed") {
		t.Errorf("Execute(host check) error = %v, want a failed-checks error", err)
	}
}

func TestOSHostSystemClockSynchronized(t *testing.T) {
	t.Parallel()

	// Whether this machine runs timedatectl, and what it says, is not
	// the test's to decide; only that an answer of "unknown" is never
	// also "synchronized".
	synced, known := osHostSystem{}.ClockSynchronized(t.Context())
	if synced && !known {
		t.Error("ClockSynchronized() = synced but unknown")
	}
}

func TestOSHostSystemRealpath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "ssoossh")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	want, _ := filepath.EvalSymlinks(target)
	if got, err := (osHostSystem{}).Realpath(link); err != nil || got != want {
		t.Errorf("Realpath() = %q, %v; want %q", got, err, want)
	}
}

func TestOSHostSystemReadFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ca.pub")
	if err := os.WriteFile(path, []byte("key\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if got, err := (osHostSystem{}).ReadFile(path); err != nil || string(got) != "key\n" {
		t.Errorf("ReadFile() = %q, %v; want the file's contents", got, err)
	}
}

func TestOSHostSystemLoadMapping(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("deploy:\n  - alice\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	m, err := osHostSystem{}.LoadMapping(path)
	if err != nil || len(m["deploy"]) != 1 {
		t.Errorf("LoadMapping() = %v, %v; want deploy mapped to alice", m, err)
	}
}
