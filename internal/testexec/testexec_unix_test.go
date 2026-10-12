//go:build unix

package testexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestWriteScriptShouldMakeTheFileOwnerOnlyExecutableWhenWritten(t *testing.T) {
	t.Parallel()

	path := WriteScript(t, t.TempDir(), "sshd", "exit 0\n")

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v (%v), want 0700", info.Mode().Perm(), err)
	}
}

func TestWriteScriptShouldRunTheBodyWhenExecuted(t *testing.T) {
	t.Parallel()

	path := WriteScript(t, t.TempDir(), "sshd", `echo "ran with $@"`+"\n")

	out, err := exec.Command(path, "-T").Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != "ran with -T" {
		t.Errorf("script printed %q (%v), want %q", got, err, "ran with -T")
	}
}

// Regression for #153: goroutines that each write a script and run it while
// the others fork must never see ETXTBSY. Without syscall.ForkLock in
// writeExecutable this failed on every run, with about one exec in ten
// refused.
func TestWriteScriptShouldNeverBeTextFileBusyWhenOtherGoroutinesFork(t *testing.T) {
	t.Parallel()
	const (
		workers = 8
		rounds  = 25
	)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	// fail records a write or run error from a worker; t.Fatal is not
	// allowed off the test goroutine.
	fail := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}
	for range workers {
		dir := t.TempDir()
		wg.Go(func() {
			for r := range rounds {
				path := filepath.Join(dir, fmt.Sprintf("s%d", r))
				if err := writeExecutable(path, []byte("#!/bin/sh\nexit 0\n")); err != nil {
					fail(err)
					continue
				}
				if err := exec.Command(path).Run(); err != nil {
					fail(err)
				}
			}
		})
	}
	wg.Wait()

	busy := 0
	for _, err := range errs {
		if errors.Is(err, syscall.ETXTBSY) {
			busy++
		}
	}
	if len(errs) != 0 {
		t.Errorf("%d of %d runs failed (%d with ETXTBSY), first: %v", len(errs), workers*rounds, busy, errs[0])
	}
}
