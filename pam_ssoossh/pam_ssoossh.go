//go:build pam

package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_appl.h>
#include <security/pam_modules.h>
*/
import "C"

import (
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

	success, err := Authenticate(&w, username, cfg)
	if err != nil {
		// Log the error and return PAM authentication failure
		w.Errorf("%s", err.Error())
		return C.int(success)
	}

	w.Infof("successful authentication: %s", username)

	// Add your logic to authenticate the user
	// If everything went well
	return C.int(success)
}

func main() {}
