package hostinfo

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Collect is best-effort by design: a field the platform cannot answer is
// empty rather than an error. These pin the parts that are not
// platform-dependent -- the env-derived fields, the bounding, and the
// promise that a login is never failed over audit metadata.

func TestCollect_ShouldReportTheProcessIdentity(t *testing.T) {
	hc := Collect()

	if hc.CallerPID == nil || *hc.CallerPID != int64(os.Getpid()) {
		t.Errorf("caller_pid = %v, want this process's pid %d", hc.CallerPID, os.Getpid())
	}
	if hc.CallerPPID == nil || *hc.CallerPPID != int64(os.Getppid()) {
		t.Errorf("caller_ppid = %v, want this process's ppid %d", hc.CallerPPID, os.Getppid())
	}
	// uid and gid exist everywhere this test runs; Windows is the platform
	// that reports neither, and the guard for it is in Collect.
	if hc.CallerUID == nil || *hc.CallerUID != int64(os.Getuid()) {
		t.Errorf("caller_uid = %v, want %d", hc.CallerUID, os.Getuid())
	}
	if hc.CallerGID == nil || *hc.CallerGID != int64(os.Getgid()) {
		t.Errorf("caller_gid = %v, want %d", hc.CallerGID, os.Getgid())
	}
}

func TestCollect_ShouldNameTheClientAndItsVersion(t *testing.T) {
	hc := Collect()

	// The same shape the C module uses, which is what lets one log tell the
	// implementations apart.
	if !strings.HasPrefix(hc.Client, "ssoossh/") {
		t.Errorf("client = %q, want a ssoossh/<version> string", hc.Client)
	}
}

func TestCollect_ShouldStampTheClientClock(t *testing.T) {
	before := time.Now().UTC()
	hc := Collect()

	if hc.ClientTime == nil {
		t.Fatal("client_time is absent; the server reads it to warn about clock skew")
	}
	if hc.ClientTime.Before(before.Add(-time.Minute)) || hc.ClientTime.After(time.Now().UTC().Add(time.Minute)) {
		t.Errorf("client_time = %v, want roughly now", hc.ClientTime)
	}
}

// argv, not the resolved path: /usr/local/bin/ssoossh tells an approver
// nothing that "ssoossh" does not.
func TestCommandLine_ShouldReportArgvWithoutTheResolvedPath(t *testing.T) {
	got := commandLine()

	if got == "" {
		t.Fatal("process is empty; os.Args always has at least argv[0]")
	}
	if strings.HasPrefix(got, "/") {
		t.Errorf("process = %q, want the base name of argv[0] rather than its path", got)
	}
}

func TestBaseName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "should strip a unix path", in: "/usr/local/bin/ssoossh", want: "ssoossh"},
		{name: "should strip a windows path", in: `C:\Program Files\ssoossh.exe`, want: "ssoossh.exe"},
		{name: "should leave a bare name alone", in: "ssoossh", want: "ssoossh"},
		{name: "should return empty for a trailing separator", in: "/usr/bin/", want: ""},
		{name: "should return empty for an empty path", in: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := baseName(tt.in); got != tt.want {
				t.Errorf("baseName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRemoteHost(t *testing.T) {
	tests := []struct {
		name       string
		connection string
		client     string
		want       string
	}{
		{
			name:       "should take the peer from SSH_CONNECTION",
			connection: "203.0.113.9 54321 10.0.0.5 22",
			want:       "203.0.113.9",
		},
		{
			name:   "should fall back to SSH_CLIENT when SSH_CONNECTION is absent",
			client: "203.0.113.9 54321 22",
			want:   "203.0.113.9",
		},
		{
			name:       "should prefer SSH_CONNECTION when both are set",
			connection: "198.51.100.7 1 10.0.0.5 22",
			client:     "203.0.113.9 54321 22",
			want:       "198.51.100.7",
		},
		// A local terminal sets neither, and reporting no remote host is
		// the answer that distinguishes it from an SSH session.
		{name: "should report nothing at a local terminal", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSH_CONNECTION", tt.connection)
			t.Setenv("SSH_CLIENT", tt.client)

			if got := remoteHost(); got != tt.want {
				t.Errorf("remoteHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

// SUDO_USER equal to the account we are running as is the ordinary case and
// adds a row that explains nothing, so the client drops it before sending —
// the same rule the approval page applies to the PAM field.
func TestRequestingUser(t *testing.T) {
	tests := []struct {
		name      string
		sudoUser  string
		wantEmpty bool
	}{
		{name: "should report the invoker when it differs", sudoUser: "alice", wantEmpty: false},
		{name: "should report nothing when it repeats the account", sudoUser: currentUsername(), wantEmpty: true},
		{name: "should report nothing when not run under sudo", sudoUser: "", wantEmpty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SUDO_USER", tt.sudoUser)

			got := requestingUser()
			if tt.wantEmpty && got != "" {
				t.Errorf("requestingUser() = %q, want empty", got)
			}
			if !tt.wantEmpty && got != tt.sudoUser {
				t.Errorf("requestingUser() = %q, want %q", got, tt.sudoUser)
			}
		})
	}
}

// Every string is bounded to what the server keeps, so what the approver
// sees is what the client chose to send rather than a silently halved value.
func TestTrunc_ShouldBoundAFieldToTheServersLimit(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", maxFieldLen+50)

	if got := trunc(long); len(got) != maxFieldLen {
		t.Errorf("trunc kept %d bytes, want %d", len(got), maxFieldLen)
	}
	if got := trunc("short"); got != "short" {
		t.Errorf("trunc(%q) = %q, want it unchanged", "short", got)
	}
}

func TestSSHTTY_ShouldReadTheTerminalFromTheEnvironment(t *testing.T) {
	t.Setenv("SSH_TTY", " /dev/pts/9 ")

	if got := sshTTY(); got != "/dev/pts/9" {
		t.Errorf("sshTTY() = %q, want /dev/pts/9", got)
	}
}

func TestFirstLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := dir + "/machine-id"
	if err := os.WriteFile(path, []byte("9aff2b3ff2f94fb192703c82948a68c8\ntrailing\n"), 0o600); err != nil {
		t.Fatalf("failed to write the fixture: %v", err)
	}

	if got := firstLine(path); got != "9aff2b3ff2f94fb192703c82948a68c8" {
		t.Errorf("firstLine = %q, want the first line only", got)
	}
	// An absent file is "not reported", not an error: a host with no
	// /etc/machine-id still authenticates.
	if got := firstLine(dir + "/does-not-exist"); got != "" {
		t.Errorf("firstLine of a missing file = %q, want empty", got)
	}
}

func TestNullTerminated_ShouldStopAtTheFirstNUL(t *testing.T) {
	t.Parallel()

	// A Utsname field is a fixed-size array, so the padding after the value
	// would otherwise ride into the string.
	padded := append([]byte("Linux"), make([]byte, 60)...)

	if got := nullTerminated(padded); got != "Linux" {
		t.Errorf("nullTerminated = %q, want Linux", got)
	}
	if got := nullTerminated([]byte("Linux")); got != "Linux" {
		t.Errorf("nullTerminated of an unpadded value = %q, want Linux", got)
	}
}
