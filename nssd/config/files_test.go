package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadKeyFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		mode       os.FileMode
		wantID     string
		wantSecret string
		wantErr    string
	}{
		{name: "should read the id and secret when the file has two lines", content: "nss_abc\nssoossh_nss_secret\n", mode: 0o600, wantID: "nss_abc", wantSecret: "ssoossh_nss_secret"},
		{name: "should ignore blank lines and CRLF when they surround the key", content: "\r\nnss_abc\r\n\r\nssoossh_nss_secret\r\n\n", mode: 0o600, wantID: "nss_abc", wantSecret: "ssoossh_nss_secret"},
		{name: "should refuse one line when the secret is missing", content: "nss_abc\n", mode: 0o600, wantErr: "holds 1"},
		{name: "should refuse three lines when there is a stray one", content: "a\nb\nc\n", mode: 0o600, wantErr: "holds 3"},
		{name: "should refuse a group-readable file when the secret would leak", content: "a\nb\n", mode: 0o640, wantErr: "group- or world-accessible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeFile(t, t.TempDir(), "nss.key", tt.content, tt.mode)
			key, err := ReadKeyFile(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "b") && strings.Contains(tt.content, "secret") {
					t.Fatal("the error carries the secret")
				}
				return
			}
			if err != nil || key.ID != tt.wantID || key.Secret != tt.wantSecret {
				t.Fatalf("got %+v, %v", key, err)
			}
		})
	}
}

func TestReadKeyFileShouldRefuseAFileNotOwnedByRootWhenRunningAsRoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("a root-owned temporary file passes the check")
	}
	path := writeFile(t, t.TempDir(), "nss.key", "a\nb\n", 0o600)
	if _, err := readKeyFile(path, 0); err == nil || !strings.Contains(err.Error(), "not root") {
		t.Fatalf("got %v", err)
	}
}

func TestStampOf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")

	missing, err := StampOf(path)
	if err != nil || missing.Exists() {
		t.Fatalf("should stamp a missing file as absent, got %+v, %v", missing, err)
	}
	writeFile(t, dir, "f", "one", 0o644)
	first, err := StampOf(path)
	if err != nil || !first.Exists() || first == missing {
		t.Fatalf("should stamp a new file differently, got %+v, %v", first, err)
	}
	same, _ := StampOf(path)
	if same != first {
		t.Fatal("should give the same stamp when nothing changed")
	}
	// Replacing by rename changes the inode even when size and mtime match.
	other := writeFile(t, dir, "g", "two", 0o644)
	if err := os.Chtimes(other, first.mtime, first.mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	replaced, _ := StampOf(path)
	if replaced == first {
		t.Fatal("should notice a replacement by rename")
	}
}

func TestStampOfShouldFailWhenThePathCannotBeStatted(t *testing.T) {
	t.Parallel()
	file := writeFile(t, t.TempDir(), "f", "x", 0o644)
	if _, err := StampOf(filepath.Join(file, "child")); err == nil {
		t.Fatal("want an error for a path under a regular file")
	}
}

func TestDomainFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hostname string
		resolv   string
		want     string
	}{
		{name: "should take the hostname's domain when it is fully qualified", hostname: "web1.corp.example.", want: "corp.example"},
		{name: "should take resolv.conf's domain when the hostname is short", hostname: "web1", resolv: "nameserver 10.0.0.1\ndomain corp.example\n", want: "corp.example"},
		{name: "should take the first search entry when there is no domain line", hostname: "web1", resolv: "search a.example b.example\n", want: "a.example"},
		{name: "should know none when neither says", hostname: "web1", resolv: "nameserver 10.0.0.1\n", want: ""},
		{name: "should skip a trailing dot when the hostname ends in one", hostname: "web1.", resolv: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domainFrom(tt.hostname, []byte(tt.resolv)); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDomainOfHost(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resolv := writeFile(t, dir, "resolv.conf", "search corp.example\n", 0o644)
	failing := func() (string, error) { return "", errors.New("no hostname") }

	tests := []struct {
		name     string
		hostname func() (string, error)
		path     string
		want     string
	}{
		{name: "should fall back on resolv.conf when the hostname lookup fails", hostname: failing, path: resolv, want: "corp.example"},
		{name: "should know none when both lookups fail", hostname: failing, path: filepath.Join(dir, "absent"), want: ""},
		{name: "should use the hostname when it is qualified", hostname: func() (string, error) { return "h.example.org", nil }, path: resolv, want: "example.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domainOfHost(tt.hostname, tt.path); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	_ = hostDomain() // the production wiring, whatever this host says
}
