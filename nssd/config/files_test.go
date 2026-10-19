package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leakCanary is the secret line of every key file a test expects refused:
// distinctive enough that finding it in an error proves the error carries
// the secret.
const leakCanary = "S3CRET-MUST-NOT-LEAK"

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
		{name: "should refuse one line when it is only the secret", content: leakCanary + "\n", mode: 0o600, wantErr: "holds 1"},
		{name: "should refuse three lines when there is a stray one", content: "nss_abc\n" + leakCanary + "\nextra\n", mode: 0o600, wantErr: "holds 3"},
		{name: "should refuse a group-readable file when the secret would leak", content: "nss_abc\n" + leakCanary + "\n", mode: 0o640, wantErr: "group- or world-accessible"},
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
				if strings.Contains(err.Error(), leakCanary) {
					t.Fatalf("the error carries the secret: %v", err)
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
	path := writeFile(t, t.TempDir(), "nss.key", "nss_abc\n"+leakCanary+"\n", 0o600)
	ownByNonRoot(t, path)
	_, err := readKeyFile(path, 0)
	if err == nil || !strings.Contains(err.Error(), "not root") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), leakCanary) {
		t.Fatalf("the error carries the secret: %v", err)
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
