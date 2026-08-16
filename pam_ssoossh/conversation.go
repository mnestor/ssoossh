//go:build pam

package main

/*
#include <stdlib.h>
#include <security/pam_appl.h>

// send_text_info displays a single PAM_TEXT_INFO message through pamh's
// conversation function — the only channel a PAM module has to talk to
// whoever is running the transaction (e.g. sudo's terminal). Go can't
// express the double pointer indirection pam_conv.conv wants (const struct
// pam_message **), so this stays in C.
static int send_text_info(pam_handle_t *pamh, const char *text) {
    const struct pam_conv *conv;
    int ret = pam_get_item(pamh, PAM_CONV, (const void **)&conv);
    if (ret != PAM_SUCCESS) {
        return ret;
    }
    if (conv == NULL || conv->conv == NULL) {
        return PAM_CONV_ERR;
    }

    struct pam_message msg;
    msg.msg_style = PAM_TEXT_INFO;
    msg.msg = text;
    const struct pam_message *msgs[1];
    msgs[0] = &msg;

    struct pam_response *resp = NULL;
    ret = conv->conv(1, msgs, &resp, conv->appdata_ptr);

    // The conversation function allocates the response array; the caller
    // (us) owns freeing it, per pam_conv's contract.
    if (resp != NULL) {
        if (resp[0].resp != NULL) {
            free(resp[0].resp);
        }
        free(resp);
    }
    return ret;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Conversation displays informational text to whoever is running the PAM
// transaction. It exists so Authenticate can show the approval URL without
// writing to stdout or stderr, which under PAM belong to the calling
// application (sudo/su), not this module — see pam_ssoossh.go's fail-closed
// history on that point. Production code goes through libpam
// (pamConversation); tests use a fake.
type Conversation interface {
	// Info displays msg and returns once the conversation function has
	// acknowledged it. PAM_TEXT_INFO carries no user response to read back.
	Info(msg string) error
}

// pamConversation implements Conversation over a live PAM transaction.
type pamConversation struct {
	pamh *C.pam_handle_t
}

// Info implements Conversation. It calls into libpam through cgo and needs a
// live PAM transaction, so it has no Go unit test (test-go.md);
// pam_ssoossh/testing/pamtest.c is the manual harness that exercises it
// against a real PAM stack.
func (p *pamConversation) Info(msg string) error {
	cMsg := C.CString(msg)
	defer C.free(unsafe.Pointer(cMsg))

	if ret := C.send_text_info(p.pamh, cMsg); ret != C.PAM_SUCCESS {
		return fmt.Errorf("pam conversation returned code %d", int(ret))
	}
	return nil
}
