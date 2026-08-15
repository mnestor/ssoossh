//go:build pam

package main

import (
	"errors"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// errNotImplemented is what every authentication attempt currently ends in.
// The module deliberately fails closed: until the certificate request and the
// four validation checks are implemented, there is no path that can decide a
// user is authentic, so it must never report success.
var errNotImplemented = errors.New("pam_ssoossh is not implemented yet")

// Authenticate generates a per-attempt keypair, asks the server to certify
// it, and validates the certificate that comes back.
//
// The per-attempt keypair is the freshness guarantee — no nonce is needed —
// and nothing about it is retained after this returns.
//
// Not implemented yet: the certificate request and all four validation checks
// (signed by the expected CA, certificate public key matches the key just
// sent, principals identify the authenticating user, and inside the validity
// window with a skew tolerance). See docs/delivery-phase7-pam.md, and the
// reference implementation preserved at the bottom of this file. Until they
// exist, this returns a failure code for every caller.
func Authenticate(log *Logger, user string, cfg config) (int, error) {
	// Validate configuration before generating a key, so a misconfigured
	// pam.d entry fails without paying for keygen.
	if cfg.server == "" {
		return PamUserUnknown, errors.New("not configured correctly in pam.d")
	}
	if cfg.trustedCAFile == "" {
		return PamNoModuleData, errors.New("not configured correctly in pam.d")
	}

	kp, err := keypair.NewSSHKeypair("ed25519", 0)
	if err != nil {
		return PamAbort, err
	}

	pub, err := kp.MarshalAuthorizedKey()
	if err != nil {
		return PamAbort, err
	}
	(*log).Debugf("generated ephemeral keypair for %s: %s", user, pub)

	return PamAuthInfoUnavail, errNotImplemented
}

/*
Reference implementation, preserved verbatim.

This is the body this function had when it was ported from an earlier working
version of the same flow. It does not compile against the current tree —
`api.PostPubKey`, `api.GetCertificate`, `kp.ParseCertificate`, and
`kp.GetCertficiate` were all removed when internal/api and
internal/crypto/ssh/keypair were rewritten — so treat it as a specification of
the checks, not as resumable code. See the notes after it for what it gets
right and, more importantly, what it misses.

	//TODO: read cfg.trustedCAFile and split contents into []string
	// cas := strings.Split(cfg.trustedCAFile, "\n")

	// ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	// defer cancel()

	// apiClient, err := api.NewClient(api.Config{
	// 	ServerURL:     cfg.server,
	// 	SkipVerifySSL: cfg.insecureSkipVerify,
	// })

	// id, err := apiClient.PostPubKey(kp)
	// if err != nil {
	// 	return PAM_AUTH_ERR, err
	// }

	// url := fmt.Sprintf("%s/approve/%s", cfg.server, id)

	// fmt.Fprintf(os.Stdout, "Please visit the URL to continue!:\n%s\n", url)

	// // wait for cert
	// var cert string
	// if cert, err = apiClient.GetCertificate(id); err != nil {
	// 	return PAM_AUTH_ERR, err
	// }

	// if cfg.debug == "stdout" {
	// 	fmt.Fprintf(os.Stdout, "Got Cert: %s\n", cert)
	// }

	// if err = kp.ParseCertificate(cert); err != nil {
	// 	return PAM_AUTH_ERR, err
	// }

	// certData := kp.GetCertficiate()
	// vbTime := time.Unix(int64(certData.ValidBefore), 0)
	// signature := strings.Trim(string(cssh.MarshalAuthorizedKey(certData.SignatureKey)), "\n")
	// validPrincipal := slices.Contains(certData.ValidPrincipals, user)
	// validBefore := time.Now().Before(vbTime)
	// validCA := slices.Contains(cas, signature)

	// if cfg.debug == "stdout" {
	// 	fmt.Fprintf(os.Stdout, "Principals:%t: %s\n", validPrincipal, strings.Join(certData.ValidPrincipals, ", "))
	// 	fmt.Fprintf(os.Stdout, "Signature:%t: %s\n", validCA, signature)
	// 	fmt.Fprintf(os.Stdout, "CA List: \n%s", strings.Join(cas, "\n"))
	// 	fmt.Fprintf(os.Stdout, "ValidBefore:%t: %s\n", validBefore, vbTime.Format(time.RFC1123))
	// }

	// if validPrincipal && validBefore && validCA {
	// 	return PAM_SUCCESS, nil
	// }

	// e := fmt.Errorf("valid certificate but invalid for user %s, principals:%t:[%s], before:%t:%s, ca:%t:%s",
	// 	user,
	// 	validPrincipal,
	// 	strings.Join(certData.ValidPrincipals, ","),
	// 	validBefore,
	// 	vbTime.Format(time.RFC1123),
	// 	validCA,
	// 	signature,
	// )

What the reference covers, against the four checks in docs/ssoossh-context.md:

 1. Signed by the expected CA — `validCA`. Compares the marshaled
    SignatureKey against lines of the trusted-CA file. Note this is a string
    membership test, not a signature verification: it asserts "the cert names
    a CA I trust" and relies on the server having actually signed with it.
    `keypair.SSHKeypair.SignedBy` verifies properly and should be preferred.
    Also note `cas` is never populated — the TODO at the top was never done,
    so `validCA` would always have been false.

 2. Certificate public key matches the key just sent — ABSENT. The reference
    never compares `certData.Key` against `kp`'s public key. This is the gap
    that matters most: with checks 1, 3, and 4 passing but not 2, any
    CA-signed certificate carrying the right principals is accepted,
    including one issued to somebody else's keypair. An attacker who can
    present a valid certificate for the target user — one they legitimately
    hold from their own session, say — authenticates as them.

 3. Principals identify the user — `validPrincipal`. Reasonable as written.

 4. Inside the validity window — `validBefore` only. It checks ValidBefore
    and ignores ValidAfter entirely, so a not-yet-valid certificate passes.
    There is also no skew tolerance and no logging of observed skew, both of
    which the design calls for; clock skew is the real operational
    constraint here.

Two other things worth carrying forward as cautions rather than patterns:

  - The debug output writes the certificate and the whole CA list to stdout.
    Under PAM, stdout is the conversation channel with whatever is invoking
    the module. Use the logger.

  - The final `return PamCredInsufficient, nil` returns a failure code with a
    nil error, and pam_ssoossh.go treats nil-error as success and logs
    "successful authentication" before returning that code. Every failure
    path needs a non-nil error.
*/
