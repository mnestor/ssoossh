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
	"os"
	"os/signal"
	"syscall"
	"unsafe"

	"github.com/mnestor/ssoossh/internal/version"
)

// http://www.fifi.org/doc/libpam-doc/html/pam_modules-3.html#ss3.2
//
// No Go unit test covers this function (test-go.md): every branch past the
// nil check calls GetUser, which needs a live PAM handle.
// pam_ssoossh/testing/pamtest.c is the manual harness; parseArgs and
// Authenticate, the logic this function is otherwise a thin cgo wrapper
// around, are unit tested directly in args_test.go and auth_test.go.
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

	w := initLogger(version.Name)
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
	cfg := parseArgs(pamArgs)

	w.SetDebug(cfg.debug)
	w.Debugf("args: %+v", cfg)

	// A human has to open a browser and approve — signal.NotifyContext lets
	// Ctrl-C at the sudo prompt abandon the request instead of leaving this
	// blocked on SSE, and the timeout bounds how long a human with no
	// browser can hang a sudo prompt. See docs/release-phase5-pam-client.md,
	// "Timeouts and cancellation".
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.waitTimeout)
	defer cancel()

	conv := &pamConversation{pamh: pamh}

	success, err := Authenticate(ctx, w, conv, username, cfg)
	if err != nil {
		w.Errorf("%s", err.Error())
	}

	// Logging success is gated on the return code, not on err being nil —
	// the two are tracked separately so a failure path that returns a
	// non-success code is never reported as a successful authentication
	// merely because it forgot to attach an error. See
	// docs/release-phase5-pam-client.md, "Fix the nil-error success
	// logging".
	if success == PamSuccess {
		w.Infof("successful authentication: %s", username)
	}

	return C.int(success)
}

func main() {}
