//go:build pam

package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_appl.h>
#include <security/pam_modules.h>
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/mnestor/ssoossh/internal/version"
)

// http://www.fifi.org/doc/libpam-doc/html/pam_modules-3.html#ss3.2
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

	fmt.Fprintf(os.Stderr, "username: %s\n", username)
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
		w.Errorf(err.Error())
		return C.int(success)
	}

	w.Infof("successful authentication: %s", username)

	// Add your logic to authenticate the user
	// If everything went well
	return C.int(success)
}

func main() {}
