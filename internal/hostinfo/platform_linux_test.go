package hostinfo

import (
	"os"
	"strings"
	"testing"
)

// The Linux readers, driven against temporary files so the branches a real
// host cannot be made to take -- no machine-id at all, an os-release
// without a PRETTY_NAME -- are still exercised.

func TestMachineIDFrom(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := dir + "/machine-id"
	second := dir + "/dbus-machine-id"
	if err := os.WriteFile(second, []byte("fallbackid\n"), 0o600); err != nil {
		t.Fatalf("failed to write the fallback: %v", err)
	}

	t.Run("should fall back to the second path when the first is absent", func(t *testing.T) {
		if got := machineIDFrom([]string{first, second}); got != "fallbackid" {
			t.Errorf("machineIDFrom = %q, want fallbackid", got)
		}
	})

	t.Run("should prefer the first path once it exists", func(t *testing.T) {
		if err := os.WriteFile(first, []byte("primaryid\n"), 0o600); err != nil {
			t.Fatalf("failed to write the primary: %v", err)
		}
		if got := machineIDFrom([]string{first, second}); got != "primaryid" {
			t.Errorf("machineIDFrom = %q, want primaryid", got)
		}
	})

	// A host with neither file still authenticates; it just reports no
	// machine id.
	t.Run("should report nothing when no path exists", func(t *testing.T) {
		if got := machineIDFrom([]string{dir + "/nope", dir + "/also-nope"}); got != "" {
			t.Errorf("machineIDFrom = %q, want empty", got)
		}
	})
}

func TestPrettyNameFrom(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	quoted := dir + "/quoted"
	bare := dir + "/bare"
	none := dir + "/none"
	for path, body := range map[string]string{
		quoted: "ID=debian\nPRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n",
		bare:   "PRETTY_NAME=Alpine Linux v3.20\n",
		none:   "ID=weird\nNAME=Weird\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
	}

	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{name: "should strip the quotes the format allows", paths: []string{quoted}, want: "Debian GNU/Linux 13 (trixie)"},
		{name: "should read an unquoted value", paths: []string{bare}, want: "Alpine Linux v3.20"},
		{name: "should fall through a file with no PRETTY_NAME", paths: []string{none, quoted}, want: "Debian GNU/Linux 13 (trixie)"},
		{name: "should report nothing when no file has one", paths: []string{none}, want: ""},
		{name: "should report nothing when no file exists", paths: []string{dir + "/missing"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := prettyNameFrom(tt.paths); got != tt.want {
				t.Errorf("prettyNameFrom = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTTYFromLink(t *testing.T) {
	tests := []struct {
		name string
		link string
		want string
	}{
		{name: "should report the terminal fd 0 points at", link: "/dev/pts/3", want: "/dev/pts/3"},
		{name: "should report a virtual console", link: "/dev/tty1", want: "/dev/tty1"},
		// A service or a cron job has no terminal, and saying so is worth
		// more on an approval page than a path to a pipe.
		{name: "should report nothing for a pipe", link: "pipe:[12345]", want: ""},
		{name: "should report nothing for /dev/null", link: "/dev/null", want: ""},
		{name: "should report nothing for a redirected file", link: "/tmp/output.log", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Cleared so the fallback cannot answer for the branch under
			// test; its own behaviour is covered by TestSSHTTY.
			t.Setenv("SSH_TTY", "")

			if got := ttyFromLink(tt.link); got != tt.want {
				t.Errorf("ttyFromLink(%q) = %q, want %q", tt.link, got, tt.want)
			}
		})
	}
}

// The kernel half of the platform string, which every Linux host can answer.
func TestUnameString_ShouldNameTheKernel(t *testing.T) {
	got := unameString()

	if !strings.HasPrefix(got, "Linux ") {
		t.Errorf("unameString = %q, want it to start with the sysname", got)
	}
	if strings.ContainsRune(got, 0) {
		t.Errorf("unameString = %q, want the NUL padding stripped", got)
	}
}

func TestOSName_ShouldCombineTheDistributionAndTheKernel(t *testing.T) {
	got := osName()

	if !strings.Contains(got, "Linux") {
		t.Errorf("osName = %q, want it to name the kernel", got)
	}
}
