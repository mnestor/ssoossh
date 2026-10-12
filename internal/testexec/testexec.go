// Package testexec writes executable stand-ins, such as a shell script
// playing sshd or ssoossh, for tests that then run them. Import it only from
// _test.go files.
package testexec

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// WriteScript writes body behind a "#!/bin/sh" line to dir/name, executable
// by its owner, and returns the path. It fails the test if the file cannot
// be written.
//
// Use it instead of os.WriteFile for any file a test will exec, because
// writing an executable races with fork in a parallel test binary (see
// writeExecutable).
func WriteScript(t testing.TB, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := writeExecutable(path, []byte("#!/bin/sh\n"+body)); err != nil {
		t.Fatalf("write script: %v", err)
		return ""
	}
	return path
}

// writeExecutable writes content to path with mode 0700 while holding
// syscall.ForkLock for reading.
//
// The race it closes (https://go.dev/issue/22315): os.WriteFile holds a
// write descriptor on path between open and close. If another goroutine
// forks in that window, the child inherits the descriptor and keeps it until
// it reaches exec, where O_CLOEXEC finally closes it. When this goroutine
// then execs path, the kernel sees the file still open for writing in that
// child and refuses with ETXTBSY ("text file busy"). Parallel subtests that
// each write a script and run it hit this regularly.
//
// syscall.ForkLock is the standard library's lock for exactly this: its doc
// (syscall/exec_unix.go) says descriptor creation is done holding it for
// reading and the fork itself holding it for writing, and forkExec takes it
// for writing around the fork. Holding the read lock from open to close
// therefore means no fork in this process can start while the write
// descriptor exists, so no child can inherit it. Children forked before are
// unaffected, since they were created before the descriptor existed. Other
// processes cannot inherit our descriptors at all. On Windows ForkLock is
// unused and the lock is a harmless no-op.
func writeExecutable(path string, content []byte) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, content, 0o700) //nolint:gosec // the point is an executable the test itself runs
}
