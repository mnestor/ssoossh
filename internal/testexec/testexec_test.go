package testexec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fatalRecorder is a testing.TB whose Fatalf records the message instead of
// ending the goroutine, so WriteScript's failure path can be observed.
type fatalRecorder struct {
	testing.TB
	msg string
}

// Helper implements testing.TB.
func (*fatalRecorder) Helper() {}

// Fatalf implements testing.TB by recording the message.
func (f *fatalRecorder) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
}

func TestWriteScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		wantFile string
	}{
		{name: "should put the body behind a shebang when the body is a command", body: "exit 0\n", wantFile: "#!/bin/sh\nexit 0\n"},
		{name: "should write only the shebang when the body is empty", body: "", wantFile: "#!/bin/sh\n"},
		{name: "should keep the body byte for byte when it has no trailing newline", body: `echo "$@"`, wantFile: "#!/bin/sh\necho \"$@\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			path := WriteScript(t, dir, "sshd", tt.body)

			got, err := os.ReadFile(path)
			if err != nil || string(got) != tt.wantFile {
				t.Errorf("WriteScript wrote %q (%v), want %q", got, err, tt.wantFile)
			}
		})
	}
}

func TestWriteScriptShouldReturnThePathInsideDirWhenWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if got, want := WriteScript(t, dir, "ssoossh", "exit 0\n"), filepath.Join(dir, "ssoossh"); got != want {
		t.Errorf("WriteScript() = %q, want %q", got, want)
	}
}

func TestWriteScriptShouldFailTheTestWhenDirDoesNotExist(t *testing.T) {
	t.Parallel()
	rec := &fatalRecorder{TB: t}

	WriteScript(rec, filepath.Join(t.TempDir(), "missing"), "sshd", "exit 0\n")

	if !strings.HasPrefix(rec.msg, "write script: ") {
		t.Errorf("Fatalf message = %q, want one starting %q", rec.msg, "write script: ")
	}
}
