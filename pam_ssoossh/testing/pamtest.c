// pamtest drives pam_start/pam_authenticate/pam_acct_mgmt against a named
// PAM service, using the standard misc_conv so any PAM_TEXT_INFO messages
// (the approval URL) print to the terminal. Build and use against a real
// pam.d stack, not as part of the Go test suite.
//
//   gcc -o pamtest pamtest.c -lpam -lpam_misc
//
// See README.md in this directory for the full recipe: build environment,
// pam.d stanza, and how to install the module for a manual run.
#include <security/pam_appl.h>
#include <security/pam_misc.h>
#include <stdio.h>

int main(int argc, char **argv) {
    const char *service = (argc > 1) ? argv[1] : "ssoossh-test";
    pam_handle_t *p = NULL;
    struct pam_conv conv = { misc_conv, NULL };

    int r = pam_start(service, "games", &conv, &p);
    if (r != PAM_SUCCESS) { fprintf(stderr, "start %d\n", r); return 1; }
    r = pam_authenticate(p, 0); printf("auth=%s\n", pam_strerror(p, r));
    r = pam_acct_mgmt(p, 0); printf("acct=%s\n", pam_strerror(p, r));
    pam_end(p, r);
    return r == PAM_SUCCESS ? 0 : 1;
}
