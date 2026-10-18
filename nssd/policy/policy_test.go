package policy

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/nssd/config"
)

// Fixture host files.
const (
	testPasswd = "root:x:0:0:root:/root:/bin/bash\n# comment\n\nbob:x:1001:1001::/home/bob:/bin/sh\n+nisuser\nbroken:x:notanumber\n"
	testGroup  = "root:x:0:\nsudo:x:27:bob\nlocalgrp:x:1500:\nalias:x:1500:\nusers:x:100:\nops:x:1600:\n+nisgrp\n"
	testShells = "# /etc/shells\n/bin/sh\n/bin/bash\n/usr/bin/zsh\n\n/usr/bin/fish\n"
)

// fixture is a policy engine over files in a temporary directory.
type fixture struct {
	engine *Engine
	cfg    *config.Config
	paths  Paths
	logs   *bytes.Buffer
}

// newFixture writes the host files and builds an Engine; mutate adjusts
// the configuration first.
func newFixture(t *testing.T, mutate func(*config.Config)) *fixture {
	t.Helper()
	dir := t.TempDir()
	paths := Paths{
		Passwd:    filepath.Join(dir, "passwd"),
		Group:     filepath.Join(dir, "group"),
		Shells:    filepath.Join(dir, "shells"),
		Overrides: filepath.Join(dir, "nss-overrides.yaml"),
	}
	for p, content := range map[string]string{paths.Passwd: testPasswd, paths.Group: testGroup, paths.Shells: testShells} {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Domain = "corp.example"
	if mutate != nil {
		mutate(&cfg)
	}
	logs := &bytes.Buffer{}
	return &fixture{engine: New(&cfg, paths, slog.New(slog.NewTextHandler(logs, nil))), cfg: &cfg, paths: paths, logs: logs}
}

// writeOverrides writes the override file.
func (f *fixture) writeOverrides(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(f.paths.Overrides, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f.engine.Reload()
}

// str returns a pointer to s.
func str(s string) *string { return &s }

// activeUser is a valid server answer for alice.
func activeUser() *apitypes.NSSUserResponse {
	return &apitypes.NSSUserResponse{
		Name:   "alice",
		UID:    5001,
		GID:    5001,
		Groups: []apitypes.NSSMembership{{GID: 5001, Name: "alice"}, {GID: 1500, Name: "localgrp"}, {GID: 27, Name: "sudo"}, {GID: 5100, Name: "devs"}, {GID: 5101, Name: "bad:name"}, {GID: 5001, Name: "alice"}},
		Home:   str("/home/alice"),
		Shell:  str("/bin/bash"),
		Gecos:  "Alice: Smith\n",
		Status: apitypes.NSSStatusActive,
		KeyID:  "nss_k",
	}
}

func TestShortCircuit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.MaxID = 60000; c.FilterUsers = []string{"root", "admin"} })

	tests := []struct {
		name  string
		check func() error
		ok    bool
	}{
		{name: "should pass a directory user name when nothing refuses it", check: func() error { return f.engine.CheckUserName("alice") }, ok: true},
		{name: "should refuse an invalid user name when it breaks the name rules", check: func() error { return f.engine.CheckUserName("al:ice") }},
		{name: "should refuse a filtered user name when filter_users lists it", check: func() error { return f.engine.CheckUserName("admin") }},
		{name: "should refuse a local user name when /etc/passwd lists it", check: func() error { return f.engine.CheckUserName("bob") }},
		{name: "should refuse a compat entry's name when it is listed with +", check: func() error { return f.engine.CheckUserName("nisuser") }},
		{name: "should refuse a broken line's name when its id is unreadable", check: func() error { return f.engine.CheckUserName("broken") }},
		{name: "should pass a directory uid when nothing refuses it", check: func() error { return f.engine.CheckUID(5001) }, ok: true},
		{name: "should refuse a uid below min_id when it is a system id", check: func() error { return f.engine.CheckUID(999) }},
		{name: "should refuse a uid above max_id when one is set", check: func() error { return f.engine.CheckUID(60001) }},
		{name: "should refuse a local uid when /etc/passwd uses it", check: func() error { return f.engine.CheckUID(1001) }},
		{name: "should pass a directory group name when nothing refuses it", check: func() error { return f.engine.CheckGroupName("devs") }, ok: true},
		{name: "should refuse an invalid group name when it breaks the name rules", check: func() error { return f.engine.CheckGroupName("1234") }},
		{name: "should refuse a filtered group name when filter_groups lists it", check: func() error { return f.engine.CheckGroupName("root") }},
		{name: "should refuse a local group name when /etc/group lists it", check: func() error { return f.engine.CheckGroupName("sudo") }},
		{name: "should pass a directory gid when nothing refuses it", check: func() error { return f.engine.CheckGID(5001) }, ok: true},
		{name: "should refuse a gid below min_id when it is a system id", check: func() error { return f.engine.CheckGID(27) }},
		{name: "should refuse a local gid when /etc/group uses it", check: func() error { return f.engine.CheckGID(1500) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.check()
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, ErrRejected)) {
				t.Fatalf("got %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestShortCircuitShouldFailClosedWhenTheLocalFilesCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	// A directory where the file should be: stat works, reading fails.
	for _, p := range []string{f.paths.Passwd, f.paths.Group} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.engine.Reload()
	for _, err := range []error{
		f.engine.CheckUserName("alice"), f.engine.CheckUID(5001),
		f.engine.CheckGroupName("devs"), f.engine.CheckGID(5001),
	} {
		if !errors.Is(err, ErrRejected) {
			t.Fatalf("got %v, want a refusal", err)
		}
	}
	if _, err := f.engine.User(activeUser()); !errors.Is(err, ErrRejected) {
		t.Fatalf("user: got %v, want a refusal", err)
	}
	if _, err := f.engine.Group(&apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice"}}); !errors.Is(err, ErrRejected) {
		t.Fatalf("group: got %v, want a refusal", err)
	}
}

func TestShortCircuitShouldTreatMissingFilesAsEmptyWhenAContainerHasNone(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	for _, p := range []string{f.paths.Passwd, f.paths.Group, f.paths.Shells} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.engine.CheckUserName("bob"); err != nil {
		t.Fatalf("got %v", err)
	}
	if err := f.engine.CheckGID(1500); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestShortCircuitShouldSeeAnEditWhenTheFileChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	if err := f.engine.CheckUserName("carol"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.Passwd, []byte(testPasswd+"carol:x:1002:1002::/home/carol:/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.CheckUserName("carol"); err == nil {
		t.Fatal("should refuse a name added to /etc/passwd without a restart")
	}
}

func TestUserSanityRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*config.Config)
		resp    func(*apitypes.NSSUserResponse)
		wantErr string
	}{
		{name: "should accept an active user when every rule passes"},
		{name: "should refuse a nil answer when the server sent none", resp: nil, wantErr: "no user record"},
		{name: "should refuse an invalid server name when it breaks the name rules", resp: func(r *apitypes.NSSUserResponse) { r.Name = "a/b" }, wantErr: "server name"},
		{name: "should refuse a filtered name when filter_users lists it", mutate: func(c *config.Config) { c.FilterUsers = []string{"alice"} }, wantErr: "filter_users"},
		{name: "should refuse a uid below min_id when it is a system id", resp: func(r *apitypes.NSSUserResponse) { r.UID = 999 }, wantErr: "uid 999"},
		{name: "should refuse a uid above max_id when one is set", mutate: func(c *config.Config) { c.MaxID = 5000 }, wantErr: "uid 5001"},
		{name: "should refuse a gid below min_id when it is a system id", resp: func(r *apitypes.NSSUserResponse) { r.GID = 100 }, wantErr: "gid 100"},
		{name: "should refuse a local name when /etc/passwd lists it", resp: func(r *apitypes.NSSUserResponse) { r.Name = "bob"; r.UID = 7000 }, wantErr: "local account"},
		{name: "should refuse a local uid when a local account uses it", resp: func(r *apitypes.NSSUserResponse) { r.UID = 1001 }, wantErr: "belongs to a local account"},
		{name: "should refuse a user outside require_groups when it is set", mutate: func(c *config.Config) { c.RequireGroups = []string{"staff"} }, wantErr: "require_groups"},
		{name: "should accept a member of require_groups when one matches", mutate: func(c *config.Config) { c.RequireGroups = []string{"staff", "devs"} }},
		{name: "should accept a member of require_groups when the match's gid is a local group's", mutate: func(c *config.Config) { c.RequireGroups = []string{"localgrp"} }},
		{name: "should refuse a user for require_groups when the only match is outside min_id..max_id", mutate: func(c *config.Config) { c.RequireGroups = []string{"sudo"} }, wantErr: "require_groups"},
		{name: "should refuse a user for require_groups when the only match's name collides with a local group", mutate: func(c *config.Config) { c.RequireGroups = []string{"users"} },
			resp: func(r *apitypes.NSSUserResponse) {
				r.Groups = append(r.Groups, apitypes.NSSMembership{GID: 2005, Name: "users"})
			}, wantErr: "require_groups"},
		{name: "should refuse a user for require_groups when the only match is in filter_groups", mutate: func(c *config.Config) { c.RequireGroups = []string{"devs"}; c.FilterGroups = []string{"devs"} }, wantErr: "require_groups"},
		{name: "should refuse a user with no home when no candidate is absolute", mutate: func(c *config.Config) { c.FallbackHomedir = "%h" }, resp: func(r *apitypes.NSSUserResponse) { r.Home = nil }, wantErr: "home"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tt.mutate)
			var resp *apitypes.NSSUserResponse
			if tt.resp != nil || tt.wantErr != "no user record" {
				resp = activeUser()
				if tt.resp != nil {
					tt.resp(resp)
				}
			}
			_, err := f.engine.User(resp)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want a refusal containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestUserMapping(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	u, err := f.engine.User(activeUser())
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "alice" || u.UID != 5001 || u.GID != 5001 || u.Home != "/home/alice" || u.Shell != "/bin/bash" || u.Locked {
		t.Fatalf("got %+v", u)
	}
	if u.RealName != "Alice Smith" {
		t.Fatalf("should sanitise realName when it holds ':' and a newline, got %q", u.RealName)
	}
	// The local gid 1500 stays under its local name (README U12); sudo's
	// system gid and the invalid name go; the duplicate alice is listed
	// once.
	if want := []string{"alice", "localgrp", "devs"}; !slices.Equal(u.Groups, want) {
		t.Fatalf("got groups %v, want %v", u.Groups, want)
	}
}

func TestUserDisabledAndDeleted(t *testing.T) {
	t.Parallel()

	for _, status := range []string{apitypes.NSSStatusDisabled, apitypes.NSSStatusDeleted, "suspended"} {
		t.Run("should lock with the disabled shell when the status is "+status, func(t *testing.T) {
			t.Parallel()
			// allowed_shells excludes nologin: the disabled shell is exempt.
			f := newFixture(t, func(c *config.Config) { c.AllowedShells = []string{"/bin/bash"} })
			resp := activeUser()
			resp.Status = status
			u, err := f.engine.User(resp)
			if err != nil {
				t.Fatal(err)
			}
			if !u.Locked || u.Shell != "/usr/sbin/nologin" {
				t.Fatalf("got %+v", u)
			}
		})
	}
}

func TestUserOverrideGID(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.OverrideGID = 100 })
	u, err := f.engine.User(activeUser())
	if err != nil || u.GID != 100 {
		t.Fatalf("should replace the primary gid after the range rule, got %+v, %v", u, err)
	}
}

func TestShellPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*config.Config)
		shell  *string
		want   string
	}{
		{name: "should take override_shell when it is acceptable", mutate: func(c *config.Config) { c.OverrideShell = "/usr/bin/fish" }, want: "/usr/bin/fish"},
		{name: "should skip override_shell when it is not acceptable", mutate: func(c *config.Config) { c.OverrideShell = "/opt/sh" }, want: "/bin/bash"},
		{name: "should take the server shell when /etc/shells lists it", want: "/bin/bash"},
		{name: "should take the server shell when allowed_shells lists it", mutate: func(c *config.Config) { c.AllowedShells = []string{"/opt/homebrew/bin/zsh"} },
			shell: str("/opt/homebrew/bin/zsh"), want: "/opt/homebrew/bin/zsh"},
		{name: "should fall to shell_fallback when the server shell is vetoed", mutate: func(c *config.Config) { c.VetoedShells = []string{"/bin/bash"} }, want: "/bin/sh"},
		{name: "should fall to shell_fallback when the server shell is not in /etc/shells", shell: str("/bin/ksh"), want: "/bin/sh"},
		{name: "should fall to shell_fallback when allowed_shells excludes the server shell", mutate: func(c *config.Config) { c.AllowedShells = []string{"/usr/bin/fish"} }, want: "/bin/sh"},
		{name: "should fall to shell_fallback when the server sends no shell", shell: str(""), want: "/bin/sh"},
		{name: "should refuse a relative shell when it is not a path", shell: str("bash"), want: "/bin/sh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tt.mutate)
			resp := activeUser()
			if tt.shell != nil {
				resp.Shell = tt.shell
			}
			u, err := f.engine.User(resp)
			if err != nil || u.Shell != tt.want {
				t.Fatalf("got %q, %v, want %q", u.Shell, err, tt.want)
			}
		})
	}
}

func TestShellShouldUseAllowedShellsOnlyWhenEtcShellsIsUnreadable(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.AllowedShells = []string{"/bin/bash"} })
	if err := os.Remove(f.paths.Shells); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.paths.Shells, 0o755); err != nil {
		t.Fatal(err)
	}
	f.engine.Reload()
	u, err := f.engine.User(activeUser())
	if err != nil || u.Shell != "/bin/bash" {
		t.Fatalf("got %q, %v", u.Shell, err)
	}
	if !strings.Contains(f.logs.String(), "cannot read /etc/shells") {
		t.Fatalf("should log the unreadable file, got %s", f.logs)
	}
}

func TestHomeExpansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		override string
		fallback string
		home     *string
		want     string
	}{
		{name: "should take the server home when no override is set", home: str("/home/alice"), want: "/home/alice"},
		{name: "should take override_homedir over the server's when it is set", override: "/srv/%u", home: str("/home/alice"), want: "/srv/alice"},
		{name: "should expand %U %d %h and %% when the template uses them", override: "%h/%U/%d/100%%", home: str("/data"), want: "/data/5001/corp.example/100%"},
		{name: "should keep an unknown sequence when the template has one", override: "/srv/%x/%u%", home: str("/home/alice"), want: "/srv/%x/alice%"},
		{name: "should take fallback_homedir when the server sends none", home: nil, want: "/home/alice"},
		{name: "should skip a relative server home when it is not absolute", home: str("alice"), fallback: "/home/%d/%u", want: "/home/corp.example/alice"},
		{name: "should skip an override that uses %h when the server sends no home", override: "%h/x", home: nil, want: "/home/alice"},
		{name: "should not mistake %%h for %h when the server sends no home", override: "/srv/%%h", home: nil, want: "/srv/%h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(c *config.Config) {
				c.OverrideHomedir = tt.override
				if tt.fallback != "" {
					c.FallbackHomedir = tt.fallback
				}
			})
			resp := activeUser()
			resp.Home = tt.home
			u, err := f.engine.User(resp)
			if err != nil || u.Home != tt.want {
				t.Fatalf("got %q, %v, want %q", u.Home, err, tt.want)
			}
		})
	}
}

func TestOverrides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides string
		wantErr   string
		check     func(User) bool
	}{
		{name: "should apply every field when the override sets them", overrides: "alice:\n  uid: 6001\n  gid: 6002\n  home: /srv/alice\n  shell: /usr/bin/fish\n",
			check: func(u User) bool {
				return u.UID == 6001 && u.GID == 6002 && u.Home == "/srv/alice" && u.Shell == "/usr/bin/fish"
			}},
		{name: "should beat the server shell when the override sets a shell", overrides: "alice:\n  shell: /usr/bin/fish\n",
			check: func(u User) bool { return u.Shell == "/usr/bin/fish" }},
		{name: "should still check the override shell when it is not acceptable", overrides: "alice:\n  shell: /opt/evil\n",
			check: func(u User) bool { return u.Shell == "/bin/sh" }},
		{name: "should refuse an override uid below min_id when it would serve a system id", overrides: "alice:\n  uid: 0\n", wantErr: "uid 0 is outside"},
		{name: "should refuse an override uid of a local account when it would take its files", overrides: "alice:\n  uid: 1001\n", wantErr: "belongs to a local account"},
		{name: "should leave other users alone when the override names someone else", overrides: "carol:\n  uid: 6001\n",
			check: func(u User) bool { return u.UID == 5001 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			f.writeOverrides(t, tt.overrides)
			u, err := f.engine.User(activeUser())
			if tt.wantErr != "" {
				if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want a refusal containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !tt.check(u) {
				t.Fatalf("got %+v, %v", u, err)
			}
		})
	}
}

func TestOverridesShouldKeepTheLastGoodWhenAnEditBreaksTheFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.writeOverrides(t, "alice:\n  uid: 6001\n")
	if got := f.engine.Overrides(); got["alice"].UID == nil {
		t.Fatalf("got %+v", got)
	}
	f.writeOverrides(t, "alice:\n  uidd: 6001\n")
	for range 2 {
		u, err := f.engine.User(activeUser())
		if err != nil || u.UID != 6001 {
			t.Fatalf("should keep the last good override, got %+v, %v", u, err)
		}
	}
	if n := strings.Count(f.logs.String(), "cannot read the host override file"); n != 1 {
		t.Fatalf("should log the broken file once, logged %d times:\n%s", n, f.logs)
	}
}

func TestGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(*config.Config)
		resp        *apitypes.NSSGroupResponse
		wantErr     string
		wantMembers []string
		wantLog     string
	}{
		{name: "should keep valid directory members when the group passes", resp: &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice", "bob", "root", "x:y", "alice", "carol"}},
			wantMembers: []string{"alice", "carol"}},
		{name: "should drop members when ignore_group_members is set", mutate: func(c *config.Config) { c.IgnoreGroupMembers = true },
			resp: &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice"}}, wantMembers: nil},
		{name: "should log when the server truncated the members", resp: &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice"}, MembersTruncated: true},
			wantMembers: []string{"alice"}, wantLog: "truncated"},
		{name: "should refuse a nil answer when the server sent none", wantErr: "no group record"},
		{name: "should refuse a local group name when /etc/group lists it", resp: &apitypes.NSSGroupResponse{Name: "localgrp", GID: 5100}, wantErr: "local group"},
		{name: "should refuse a gid below min_id when it is a system id", resp: &apitypes.NSSGroupResponse{Name: "devs", GID: 10}, wantErr: "gid 10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tt.mutate)
			g, err := f.engine.Group(tt.resp)
			if tt.wantErr != "" {
				if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want a refusal containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(g.Members, tt.wantMembers) {
				t.Fatalf("got %+v, %v, want members %v", g, err, tt.wantMembers)
			}
			if tt.wantLog != "" && !strings.Contains(f.logs.String(), tt.wantLog) {
				t.Fatalf("want a log containing %q, got %s", tt.wantLog, f.logs)
			}
		})
	}
}

func TestGroupStub(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	if err := f.engine.GroupStub(apitypes.NSSMembership{GID: 5100, Name: "devs"}); err != nil {
		t.Fatalf("should pass a directory group when nothing refuses it, got %v", err)
	}
	if err := f.engine.GroupStub(apitypes.NSSMembership{GID: 27, Name: "sudo"}); err == nil {
		t.Fatal("should refuse a local group when it fills the group cache")
	}
	if err := f.engine.GroupStub(apitypes.NSSMembership{GID: 27, Name: "wheel2"}); err == nil {
		t.Fatal("should refuse a system gid when it fills the group cache")
	}
}

func TestDefaultPathsShouldNameTheSystemFilesWhenUsedInProduction(t *testing.T) {
	t.Parallel()
	p := DefaultPaths()
	if p.Passwd != "/etc/passwd" || p.Group != "/etc/group" || p.Shells != "/etc/shells" || p.Overrides != config.DefaultOverridesPath {
		t.Fatalf("got %+v", p)
	}
}

func TestWatchedShouldFailWhenThePathCannotBeStatted(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w := newWatched(filepath.Join(file, "child"), readShells)
	if _, err := w.get(); err == nil {
		t.Fatal("want a stat error")
	}
}

func TestParseLocalDBShouldReadAVeryLongLineWhenOneIsPresent(t *testing.T) {
	t.Parallel()
	db := parseLocalDB([]byte("long:x:2000:2000:" + strings.Repeat("g", 70000) + ":/h:/s\n"))
	if !db.hasName("long") || !db.hasID(2000) {
		t.Fatal("a long gecos should not hide the entry")
	}
}

// fixedNow is the time findings are stamped with in tests.
var fixedNow = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// debugFixture is newFixture with debug logging and a fixed clock.
func debugFixture(t *testing.T, mutate func(*config.Config)) *fixture {
	t.Helper()
	f := newFixture(t, mutate)
	f.engine.log = slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.engine.now = func() time.Time { return fixedNow }
	return f
}

// u32 returns a pointer to v.
func u32(v uint32) *uint32 { return &v }

func TestMembershipsGIDRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		mutate         func(*config.Config)
		groups         []apitypes.NSSMembership
		want           []string
		wantLog        string
		wantNoLog      string
		wantCollisions []NameCollision
		wantRefused    []RefusedGroup
	}{
		{name: "should drop a membership when its gid is below min_id",
			groups: []apitypes.NSSMembership{{GID: 27, Name: "sudo"}}, want: []string{},
			wantLog:     "outside min_id..max_id",
			wantRefused: []RefusedGroup{{Name: "sudo", GID: 27, Reason: "gid 27 is outside min_id..max_id (1000..unlimited)", FirstSeen: fixedNow}}},
		{name: "should drop a membership when its gid is above max_id", mutate: func(c *config.Config) { c.MaxID = 6000 },
			groups: []apitypes.NSSMembership{{GID: 7000, Name: "big"}}, want: []string{},
			wantRefused: []RefusedGroup{{Name: "big", GID: 7000, Reason: "gid 7000 is outside min_id..max_id (1000..6000)", FirstSeen: fixedNow}}},
		{name: "should list the local name without a warning when a local group has the same name and gid",
			groups: []apitypes.NSSMembership{{GID: 1500, Name: "localgrp"}}, want: []string{"localgrp"}, wantNoLog: "level="},
		{name: "should list the local name with a debug line when the gid is a local group's under another name",
			groups: []apitypes.NSSMembership{{GID: 1500, Name: "dirgrp"}}, want: []string{"localgrp"},
			wantLog: "level=DEBUG msg=\"a directory membership's gid is a local group's"},
		{name: "should drop a membership when its gid is a local group's that filter_groups lists", mutate: func(c *config.Config) { c.FilterGroups = []string{"ops"} },
			groups: []apitypes.NSSMembership{{GID: 1600, Name: "dirops"}}, want: []string{}},
		{name: "should drop a membership and warn when its name is a local group's with another gid",
			groups: []apitypes.NSSMembership{{GID: 2005, Name: "users"}}, want: []string{},
			wantLog:        `level=WARN msg="a directory group has the same name as a local group; it is not resolved on this host`,
			wantCollisions: []NameCollision{{Name: "users", GID: 2005, LocalGID: u32(100), FirstSeen: fixedNow}}},
		{name: "should drop a membership and record no local gid when the local entry gives none",
			groups: []apitypes.NSSMembership{{GID: 2006, Name: "nisgrp"}}, want: []string{},
			wantCollisions: []NameCollision{{Name: "nisgrp", GID: 2006, FirstSeen: fixedNow}}},
		{name: "should drop a membership when its name is invalid",
			groups: []apitypes.NSSMembership{{GID: 5101, Name: "bad:name"}}, want: []string{}, wantNoLog: "level="},
		{name: "should drop a membership when filter_groups lists its name", mutate: func(c *config.Config) { c.FilterGroups = []string{"devs"} },
			groups: []apitypes.NSSMembership{{GID: 5100, Name: "devs"}}, want: []string{}},
		{name: "should list the directory name when no rule refuses it",
			groups: []apitypes.NSSMembership{{GID: 5100, Name: "devs"}}, want: []string{"devs"}, wantNoLog: "level="},
		{name: "should list a name once when two memberships give it",
			groups: []apitypes.NSSMembership{{GID: 1500, Name: "localgrp"}, {GID: 1500, Name: "alias"}, {GID: 5100, Name: "devs"}, {GID: 5100, Name: "devs"}},
			want:   []string{"localgrp", "devs"}},
		{name: "should list nothing when the user has no memberships", groups: nil, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := debugFixture(t, tt.mutate)
			resp := activeUser()
			resp.Groups = tt.groups
			u, err := f.engine.User(resp)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(u.Groups, tt.want) {
				t.Fatalf("got groups %v, want %v", u.Groups, tt.want)
			}
			logs := f.logs.String()
			if tt.wantLog != "" && !strings.Contains(logs, tt.wantLog) {
				t.Fatalf("want a log containing %q, got %s", tt.wantLog, logs)
			}
			if tt.wantNoLog != "" && strings.Contains(logs, tt.wantNoLog) {
				t.Fatalf("want no log containing %q, got %s", tt.wantNoLog, logs)
			}
			got := f.engine.Findings()
			if !slices.EqualFunc(got.Collisions, tt.wantCollisions, equalCollision) || !slices.Equal(got.Refused, tt.wantRefused) {
				t.Fatalf("got findings %+v, want collisions %+v and refused %+v", got, tt.wantCollisions, tt.wantRefused)
			}
		})
	}
}

// equalCollision compares two collisions by value.
func equalCollision(a, b NameCollision) bool {
	return a.Name == b.Name && a.GID == b.GID && a.FirstSeen.Equal(b.FirstSeen) &&
		(a.LocalGID == nil) == (b.LocalGID == nil) && (a.LocalGID == nil || *a.LocalGID == *b.LocalGID)
}

func TestMembershipsShouldRefuseTheUserWhenTheLocalGroupsCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	if err := os.Remove(f.paths.Group); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.paths.Group, 0o755); err != nil {
		t.Fatal(err)
	}
	f.engine.Reload()
	if _, err := f.engine.User(activeUser()); !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "local groups") {
		t.Fatalf("got %v, want a refusal naming the local groups", err)
	}
}

func TestGroupAnswerGIDRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		check          func(*Engine) error
		wantErr        string
		wantCollisions int
		wantRefused    int
	}{
		{name: "should refuse and record a group answer when its name collides with a local group",
			check: func(e *Engine) error {
				_, err := e.Group(&apitypes.NSSGroupResponse{Name: "users", GID: 2005})
				return err
			}, wantErr: "local group", wantCollisions: 1},
		{name: "should refuse and record a group answer when its gid is outside min_id..max_id",
			check: func(e *Engine) error {
				_, err := e.Group(&apitypes.NSSGroupResponse{Name: "oldstaff", GID: 500})
				return err
			}, wantErr: "gid 500", wantRefused: 1},
		{name: "should refuse without recording a group answer when it is the local group itself",
			check: func(e *Engine) error {
				_, err := e.Group(&apitypes.NSSGroupResponse{Name: "localgrp", GID: 1500})
				return err
			}, wantErr: "local group"},
		{name: "should refuse and record a membership stub when its name collides with a local group",
			check: func(e *Engine) error { return e.GroupStub(apitypes.NSSMembership{Name: "users", GID: 2005}) }, wantErr: "local group", wantCollisions: 1},
		{name: "should refuse and record a membership stub when its gid is outside min_id..max_id",
			check: func(e *Engine) error { return e.GroupStub(apitypes.NSSMembership{Name: "sudo", GID: 27}) }, wantErr: "local group", wantRefused: 1},
		{name: "should not record a local group name when the short-circuit refuses it",
			check: func(e *Engine) error { return e.CheckGroupName("users") }, wantErr: "local group"},
		{name: "should not record a system gid when the short-circuit refuses it",
			check: func(e *Engine) error { return e.CheckGID(27) }, wantErr: "gid 27"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := debugFixture(t, nil)
			err := tt.check(f.engine)
			if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want a refusal containing %q", err, tt.wantErr)
			}
			got := f.engine.Findings()
			if len(got.Collisions) != tt.wantCollisions || len(got.Refused) != tt.wantRefused {
				t.Fatalf("got findings %+v, want %d collisions and %d refused", got, tt.wantCollisions, tt.wantRefused)
			}
			if tt.wantCollisions+tt.wantRefused == 0 && strings.Contains(f.logs.String(), "level=WARN") {
				t.Fatalf("want no warning, got %s", f.logs)
			}
		})
	}
}

func TestFindingsShouldWarnOnceWhenACollisionRecurs(t *testing.T) {
	t.Parallel()
	f := debugFixture(t, nil)
	resp := activeUser()
	resp.Groups = append(resp.Groups, apitypes.NSSMembership{GID: 2005, Name: "users"})
	for range 3 {
		if _, err := f.engine.User(resp); err != nil {
			t.Fatal(err)
		}
		_, _ = f.engine.Group(&apitypes.NSSGroupResponse{Name: "users", GID: 2005})
		_ = f.engine.GroupStub(apitypes.NSSMembership{Name: "users", GID: 2005})
	}
	if n := strings.Count(f.logs.String(), "same name as a local group"); n != 1 {
		t.Fatalf("got %d warnings, want 1:\n%s", n, f.logs)
	}
}

func TestFindingsShouldWarnAgainWhenTheLocalGIDDiffers(t *testing.T) {
	t.Parallel()
	f := debugFixture(t, nil)
	stub := apitypes.NSSMembership{GID: 2005, Name: "users"}
	_ = f.engine.GroupStub(stub)
	if err := os.WriteFile(f.paths.Group, []byte(strings.Replace(testGroup, "users:x:100:", "users:x:101:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	f.engine.Reload()
	_ = f.engine.GroupStub(stub)
	if got := f.engine.Findings(); len(got.Collisions) != 2 {
		t.Fatalf("got %+v, want one collision per local gid", got)
	}
}

func TestFindingsShouldStartOverWhenCleared(t *testing.T) {
	t.Parallel()
	f := debugFixture(t, nil)
	stub := apitypes.NSSMembership{GID: 2005, Name: "users"}
	_ = f.engine.GroupStub(stub)
	_ = f.engine.GroupStub(apitypes.NSSMembership{GID: 27, Name: "sudo"})
	f.engine.ClearFindings()
	if got := f.engine.Findings(); len(got.Collisions)+len(got.Refused) != 0 {
		t.Fatalf("got %+v, want nothing after a clear", got)
	}
	_ = f.engine.GroupStub(stub)
	if n := strings.Count(f.logs.String(), "same name as a local group"); n != 2 {
		t.Fatalf("got %d warnings, want 2 (one each side of the clear):\n%s", n, f.logs)
	}
}

func TestFindingsShouldCapTheListWhenManyGroupsAreRefused(t *testing.T) {
	t.Parallel()
	f := debugFixture(t, nil)
	extra := 5
	for i := range findingsShown + extra {
		_ = f.engine.GroupStub(apitypes.NSSMembership{GID: uint32(3000 + i), Name: "users"})
		_ = f.engine.GroupStub(apitypes.NSSMembership{GID: uint32(100 + i), Name: "low" + strconv.Itoa(i)})
	}
	got := f.engine.Findings()
	if len(got.Collisions) != findingsShown || got.MoreCollisions != extra || len(got.Refused) != findingsShown || got.MoreRefused != extra {
		t.Fatalf("got %d (+%d) collisions and %d (+%d) refused, want %d (+%d) of each",
			len(got.Collisions), got.MoreCollisions, len(got.Refused), got.MoreRefused, findingsShown, extra)
	}
}
