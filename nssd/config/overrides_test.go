package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

func TestParseOverrides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    Overrides
		wantErr string
	}{
		{name: "should return none when the file is empty", content: "  \n", want: Overrides{}},
		{name: "should decode every field when they are set", content: "alice:\n  uid: 5001\n  gid: 5002\n  home: /srv/alice\n  shell: /bin/zsh\n",
			want: Overrides{"alice": {UID: ptr(uint32(5001)), GID: ptr(uint32(5002)), Home: "/srv/alice", Shell: "/bin/zsh"}}},
		{name: "should refuse an unknown field when it is probably a typo", content: "alice:\n  homedir: /srv\n", wantErr: "homedir"},
		{name: "should refuse an invalid user name when it can never match", content: "\"a:b\":\n  uid: 5001\n", wantErr: "invalid POSIX name"},
		{name: "should refuse a relative home when it must be absolute", content: "alice:\n  home: srv\n", wantErr: "home"},
		{name: "should refuse a relative shell when it must be absolute", content: "alice:\n  shell: zsh\n", wantErr: "shell"},
		{name: "should refuse a negative uid when it is not a uint32", content: "alice:\n  uid: -1\n", wantErr: "parse overrides"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseOverrides([]byte(tt.content))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(got) != len(tt.want) {
				t.Fatalf("got %+v, %v", got, err)
			}
			for name, o := range tt.want {
				g := got[name]
				if *g.UID != *o.UID || *g.GID != *o.GID || g.Home != o.Home || g.Shell != o.Shell {
					t.Fatalf("got %+v for %s", g, name)
				}
			}
		})
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		missing bool
		euid    int
		// foreignOwner gives the file a non-root owner.
		foreignOwner bool
		wantLen      int
		wantErr      string
	}{
		{name: "should return none when the file is missing", missing: true, euid: os.Geteuid()},
		{name: "should read a world-readable file when only root can write it", content: "alice:\n  uid: 5001\n", mode: 0o644, euid: os.Geteuid(), wantLen: 1},
		{name: "should refuse a group-writable file when others could change uids", content: "alice:\n  uid: 5001\n", mode: 0o664, euid: os.Geteuid(), wantErr: "writable"},
		{name: "should refuse a file not owned by root when running as root", content: "", mode: 0o644, euid: 0, foreignOwner: true, wantErr: "not root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "nss-overrides.yaml")
			if !tt.missing {
				writeFile(t, dir, "nss-overrides.yaml", tt.content, tt.mode)
			}
			if tt.foreignOwner {
				ownByNonRoot(t, path)
			}
			got, err := loadOverrides(path, tt.euid)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(got) != tt.wantLen {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
	if _, err := LoadOverrides(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("should return none through LoadOverrides when missing, got %v", err)
	}
}

func TestWriteOverridesShouldRoundTripWhenWrittenThenLoaded(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nss-overrides.yaml")
	want := Overrides{
		"bob":   {Shell: "/bin/bash"},
		"alice": {UID: ptr(uint32(5001)), Home: "/srv/alice"},
	}
	if err := WriteOverrides(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("should write 0644, got %v, %v", info, err)
	}
	got, err := LoadOverrides(path)
	if err != nil || len(got) != 2 || *got["alice"].UID != 5001 || got["bob"].Shell != "/bin/bash" {
		t.Fatalf("got %+v, %v", got, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# ssoossh-nss host overrides") || strings.Index(string(data), "alice") > strings.Index(string(data), "bob") {
		t.Fatalf("should write a header and sorted names, got:\n%s", data)
	}

	if err := WriteOverrides(path, Overrides{}); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadOverrides(path); err != nil || len(got) != 0 {
		t.Fatalf("should write an empty file when no overrides are left, got %+v, %v", got, err)
	}
}

func TestWriteOverridesShouldRefuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      func(t *testing.T) string
		overrides Overrides
		wantErr   string
	}{
		{name: "should refuse an invalid override when it would not load back", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "o.yaml") },
			overrides: Overrides{"alice": {Home: "relative"}}, wantErr: "home"},
		{name: "should fail when the directory does not exist", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent", "o.yaml") },
			overrides: Overrides{}, wantErr: "write overrides"},
		{name: "should fail when the target is a directory it cannot replace", path: func(t *testing.T) string {
			dir := t.TempDir()
			target := filepath.Join(dir, "o.yaml")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "x"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return target
		}, overrides: Overrides{}, wantErr: "write overrides"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := WriteOverrides(tt.path(t), tt.overrides); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestOverrideIsZero(t *testing.T) {
	t.Parallel()
	if !(Override{}).IsZero() || (Override{Shell: "/bin/sh"}).IsZero() {
		t.Fatal("IsZero should be true only when nothing is set")
	}
}
