//go:build pam

package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_appl.h>
#include <security/pam_modules.h>
*/
import "C"

import (
	"context"
	"log/syslog"
	"os"
	"os/signal"
	"syscall"
	"unsafe"

	"github.com/mnestor/ssoossh/internal/version"
)

// run is the Go half of the module entry point: everything after the
// username and module arguments have been read out of the PAM transaction.
// authenticate, the cgo shim below, hands those in and returns whatever run
// returns, so this is where the entry point's behaviour is unit tested
// (pam_ssoossh_test.go) without a live pam_handle_t.
//
// ctx is the caller's cancellation; run layers the interrupt handling and
// the wait timeout on top of it.
func run(ctx context.Context, w Logger, conv Conversation, username string, pamArgs []string) int {
	// Every invocation logs its own version at Info, not gated behind
	// debug: a module that can only be asked its version with debug
	// already enabled is a worse support problem than one extra log line
	// per sudo/su attempt. See .claude/rules/pam.md, "Version
	// stamping".
	w.Infof("pam_ssoossh %s (commit %s, built %s by %s)", version.Version, version.Commit, version.Date, version.BuiltBy)

	cfg := parseArgs(pamArgs)

	w.SetDebug(cfg.debug)
	w.Debugf("args: %+v", cfg)

	// A human has to open a browser and approve — signal.NotifyContext lets
	// Ctrl-C at the sudo prompt abandon the request instead of leaving this
	// blocked on SSE, and the timeout bounds how long a human with no
	// browser can hang a sudo prompt. See docs/operations/deployment.md's PAM section,
	// "Timeouts and cancellation".
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.waitTimeout)
	defer cancel()

	success, err := Authenticate(ctx, w, conv, username, cfg)
	if err != nil {
		w.Errorf("%s", err.Error())
	}

	// Logging success is gated on the return code, not on err being nil —
	// the two are tracked separately so a failure path that returns a
	// non-success code is never reported as a successful authentication
	// merely because it forgot to attach an error. See
	// the original design note: "Fix the nil-error success
	// logging".
	if success == PamSuccess {
		w.Infof("successful authentication: %s", username)
	}

	return success
}

// http://www.fifi.org/doc/libpam-doc/html/pam_modules-3.html#ss3.2
//
// not covered: no Go unit test covers this function (test-go.md) because
// every branch past the nil check calls GetUser, which needs a live PAM
// handle. pam_ssoossh/testing/pamtest.c is the manual harness; run, which
// holds everything this cgo wrapper does once it has the username and
// arguments in Go types, is unit tested directly in pam_ssoossh_test.go.
//
//export authenticate
func authenticate(pamh *C.pam_handle_t, flags C.int, argc C.int, args **C.char) C.int {
	if args == nil {
		return C.int(PamNoModuleData)
	}

	username, err := GetUser(pamh)
	if err != nil {
		// Log the error and return PAM authentication failure
		return C.int(PamUserUnknown)
	}

	w := initLogger(version.Name, syslog.New)
	defer w.Close()

	// args is a PAM-owned char*[argc]; unsafe.Slice is the standard,
	// bounds-safe way to view it as a Go slice. C.GoString copies each
	// string into Go-managed memory, so there's nothing here for us to
	// free — the underlying char*s are still PAM's.
	cArgs := unsafe.Slice(args, int(argc))
	pamArgs := make([]string, len(cArgs))
	for i, s := range cArgs {
		pamArgs[i] = C.GoString(s)
	}

	return C.int(run(context.Background(), w, &pamConversation{pamh: pamh}, username, pamArgs))
}

func main() {}
