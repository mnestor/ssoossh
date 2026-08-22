//go:build pam

package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_ext.h>
#include <security/pam_modules.h>

extern int authenticate(pam_handle_t *pamh, int flags, int argc, const char **argv);

// Function to get the password (or authentication token) from PAM. Writes
// the result through password rather than a module-level variable, so
// concurrent PAM transactions in the same process never share this memory.
int get_authtok(pam_handle_t* pamh, const char** password) {
    return pam_get_authtok(pamh, PAM_AUTHTOK, password, NULL);
}

// Function to get the username from PAM. Writes the result through
// username rather than a module-level variable, so concurrent PAM
// transactions in the same process never share this memory.
int get_user(pam_handle_t* pamh, const char** username) {
    return pam_get_user(pamh, username, "Username: ");
}

int pam_sm_authenticate(pam_handle_t *pamh, int flags, int argc, const char **argv) {
    return authenticate(pamh, flags, argc, argv);
}

int pam_sm_setcred(pam_handle_t *pamh, int flags, int argc, const char **argv) {
    return PAM_SUCCESS;
}
*/
import "C"

import (
	"errors"
)

// GetUser calls into libpam through cgo and needs a live PAM transaction, so
// it has no Go unit test (test-go.md); pam_ssoossh/testing/pamtest.c is the
// manual harness that exercises it against a real PAM stack.
func GetUser(pamh *C.pam_handle_t) (string, error) {
	var cUsername *C.char
	ret := C.get_user(pamh, &cUsername)
	if ret != C.PAM_SUCCESS {
		return "", errors.New("username could not be retrieved")
	}
	return C.GoString(cUsername), nil
}
