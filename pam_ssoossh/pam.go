//go:build pam

package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_ext.h>
#include <security/pam_modules.h>

extern int authenticate(pam_handle_t *pamh, int flags, int argc, const char **argv);

const char* c_username;
const char* c_password;

// Function to get the username from PAM.
int get_authtok(pam_handle_t* pamh) {
    return pam_get_authtok(pamh, PAM_AUTHTOK, &c_password , NULL);
}

// Function to get the password (or authentication token) from PAM.
int get_user(pam_handle_t* pamh) {
    return pam_get_user(pamh, &c_username, "Username: ");
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
	ret := C.get_user(pamh)
	if ret != C.PAM_SUCCESS {
		return "", errors.New("username could not be retrieved")
	}
	return C.GoString(C.c_username), nil
}
