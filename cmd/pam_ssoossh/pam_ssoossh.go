// Created By Mike Nestor <me@mikenestor.org>
package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_appl.h>
#include <security/pam_modules.h>
*/
import "C"

import (
	"fmt"
	"log/syslog"
	"os"
	"unsafe"

	"github.com/mnestor/ssoossh/internal/cmd/pam_ssoossh"
)

// http://www.fifi.org/doc/libpam-doc/html/pam_modules-3.html#ss3.2
//
//export go_authenticate
func go_authenticate(pamh *C.pam_handle_t, flags C.int, argc C.int, args **C.char) C.int {
	if args == nil {
		return C.PAM_AUTH_ERR
	}

	username, err := GetUser(pamh)
	if err != nil {
		// Log the error and return PAM authentication failure
		return C.PAM_AUTH_ERR
	}

	w, err := syslog.New(syslog.LOG_AUTHPRIV, "ssoossh")
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		return C.PAM_AUTH_ERR
	}

	length := int(argc)
	tmpSlice := (*[1 << 30]*C.char)(unsafe.Pointer(args))[:length:length]
	pamArgs := make([]string, length)
	for i, s := range tmpSlice {
		t := C.GoString(s)
		pamArgs[i] = t
		// do we need to free this? How? // could not determine what C.free refers to
		// defer C.free(unsafe.Pointer(t))
	}

	success, err := pam_ssoossh.Authenticate(username, pamArgs)
	if err != nil {
		// Log the error and return PAM authentication failure
		w.Err(err.Error())
		return C.int(success)
	}

	w.Info(fmt.Sprintf("successful authentication: %s", username))

	// Add your logic to authenticate the user
	// If everything went well
	return C.int(success)
}

func main() {}
