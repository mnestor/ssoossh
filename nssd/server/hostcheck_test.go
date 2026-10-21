package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckNSSwitch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		wantOK      bool
		wantProblem string
	}{
		{name: "should pass when every line lists systemd", wantOK: true,
			content: "passwd: files systemd\ngroup:  files [SUCCESS=merge] systemd\nshadow: files systemd # sp_expire for locked users\n"},
		{name: "should fail compat when the token systemd is absent as on Leap", wantProblem: `the passwd: line "compat" in`,
			content: "passwd: compat\ngroup: compat systemd\nshadow: compat systemd\n"},
		{name: "should fail shadow when Alma 9 ships files only", wantProblem: `the shadow: line "files" in`,
			content: "passwd: files systemd\ngroup: files systemd\nshadow: files\n"},
		{name: "should fail a missing line when glibc's default applies", wantProblem: "has no group: line",
			content: "passwd: files systemd\nshadow: files systemd\n"},
		{name: "should ignore a commented systemd when it is not in force", wantProblem: "passwd:",
			content: "passwd: files # systemd\ngroup: files systemd\nshadow: files systemd\n"},
		{name: "should not mistake a longer token when it only contains systemd", wantProblem: "passwd:",
			content: "passwd: files systemd-foo\ngroup: files systemd\nshadow: files systemd\n"},
		{name: "should pass when every line lists ssoossh, the RHEL 8 module", wantOK: true,
			content: "passwd: files ssoossh\ngroup:  files ssoossh\nshadow: files ssoossh\n"},
		{name: "should pass a mix of systemd and ssoossh across the lines", wantOK: true,
			content: "passwd: files ssoossh\ngroup:  files systemd\nshadow: files ssoossh\n"},
		{name: "should not mistake a longer token for ssoossh", wantProblem: "passwd:",
			content: "passwd: files ssoosshd\ngroup: files systemd\nshadow: files systemd\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "nsswitch.conf")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			r := CheckNSSwitch(path)
			if r.OK() != tt.wantOK {
				t.Fatalf("got OK %v, problems %v", r.OK(), r.Problems())
			}
			if tt.wantProblem != "" && !slices.ContainsFunc(r.Problems(), func(p string) bool { return strings.Contains(p, tt.wantProblem) }) {
				t.Fatalf("got problems %v, want one containing %q", r.Problems(), tt.wantProblem)
			}
			if len(r.Lines) != 3 || r.Lines[0].Database != "passwd" || r.Lines[2].Database != "shadow" {
				t.Fatalf("got lines %+v", r.Lines)
			}
		})
	}
}

func TestCheckNSSwitchShouldFailWhenTheFileIsUnreadable(t *testing.T) {
	t.Parallel()
	r := CheckNSSwitch(filepath.Join(t.TempDir(), "absent"))
	if r.OK() || len(r.Problems()) != 1 || !strings.Contains(r.Problems()[0], "cannot read") {
		t.Fatalf("got %+v", r)
	}
}

func TestRunningCaches(t *testing.T) {
	t.Parallel()
	proc := t.TempDir()
	for pid, comm := range map[string]string{"1": "systemd\n", "200": "nscd\n", "201": "nscd\n", "300": "unscd\n", "self": "nscd\n"} {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, pid, "comm"), []byte(comm), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A process that exited between the glob and the read.
	if err := os.MkdirAll(filepath.Join(proc, "400", "comm"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := RunningCaches(proc)
	slices.Sort(got)
	if !slices.Equal(got, []string{"nscd", "unscd"}) {
		t.Fatalf("got %v", got)
	}
	if got := RunningCaches(t.TempDir()); len(got) != 0 {
		t.Fatalf("should find none on a host without them, got %v", got)
	}
}
