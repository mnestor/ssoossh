package hostinfo

import (
	"os"
	"strings"
)

// sshTTY is the terminal named by the environment of an SSH session, the
// one answer available on every platform without a /proc to read or a libc
// call to make. Empty when the client is not running under sshd, which is
// the ordinary case at a local terminal.
func sshTTY() string {
	return strings.TrimSpace(os.Getenv("SSH_TTY"))
}

// firstLine reads a file's first line, trimmed. Empty for a file that is
// absent, unreadable or empty -- every caller wants "no answer" rather than
// an error it would only discard.
func firstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(line)
}

// nullTerminated turns a C character array from a Utsname into a Go string,
// stopping at the first NUL. Ranging the whole array would carry its
// padding into the field.
func nullTerminated(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
