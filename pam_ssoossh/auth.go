//go:build pam

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// Authenticate generates a per-attempt keypair, asks the server to certify
// it for user, displays the approval URL through conv, and validates the
// certificate that comes back against all four checks (see checks.go).
//
// The per-attempt keypair is the freshness guarantee — no nonce is needed —
// and nothing about it is retained after this returns. ctx bounds the whole
// attempt: the caller is expected to combine a wait timeout with interrupt
// handling (see pam_ssoossh.go's authenticate), so a Ctrl-C at the sudo
// prompt or a timeout both unblock this the same way, via ctx.
func Authenticate(ctx context.Context, log Logger, conv Conversation, user string, cfg config) (int, error) {
	// Validate configuration before generating a key or making a network
	// call, so a misconfigured pam.d entry fails without paying for either.
	if cfg.server == "" {
		return PamUserUnknown, errors.New("not configured correctly in pam.d")
	}
	if cfg.trustedCAFile == "" {
		return PamNoModuleData, errors.New("not configured correctly in pam.d")
	}
	cas, err := parseTrustedCAs(cfg.trustedCAFile)
	if err != nil {
		return PamNoModuleData, fmt.Errorf("load trusted CA file: %w", err)
	}

	apiClient, err := api.NewClient(api.Config{
		ServerURL:     cfg.server,
		SkipVerifySSL: cfg.insecureSkipVerify,
	})
	if err != nil {
		return PamAbort, fmt.Errorf("build API client: %w", err)
	}

	kp, err := keypair.NewSSHKeypair("ed25519", 0)
	if err != nil {
		return PamAbort, err
	}
	pub, err := kp.MarshalAuthorizedKey()
	if err != nil {
		return PamAbort, err
	}
	log.Debugf("generated ephemeral keypair for %s: %s", user, pub)

	pending, err := apiClient.CreatePAMRequest(ctx, pub, user, api.RequestedOptions{})
	if err != nil {
		return classifyRequestError(err)
	}

	if err := conv.Info(fmt.Sprintf("Approve this request in your browser:\n%s", pending.ApprovalURL)); err != nil {
		// Not fatal: the request still resolves without the human having
		// seen the URL through this channel, and the URL is also below at
		// debug level for a support case working from the logs.
		log.Warningf("could not display the approval URL via the PAM conversation: %v", err)
	}
	log.Debugf("approval URL for %s: %s", user, pending.ApprovalURL)

	result, err := apiClient.AwaitCertificate(ctx, pending)
	if err != nil {
		return classifyRequestError(err)
	}

	code, certStr, err := outcomeCertificate(result)
	if err != nil {
		return code, err
	}

	if err := kp.ParseCertificateFromString(certStr); err != nil {
		return PamAuthErr, fmt.Errorf("the issued certificate could not be parsed: %w", err)
	}
	cert := kp.Certificate()

	if err := checkCASignature(kp, cas); err != nil {
		return PamAuthErr, err
	}
	if err := checkKeyBinding(cert, kp); err != nil {
		return PamAuthErr, err
	}
	if err := checkPrincipal(cert, user); err != nil {
		return PamAuthErr, err
	}
	if err := checkValidityWindow(cert, time.Now(), cfg.skewTolerance); err != nil {
		return PamAuthErr, err
	}

	return PamSuccess, nil
}

// outcomeCertificate turns a resolved request into either the certificate to
// validate or the failure code and error explaining why there isn't one.
// Every terminal status ssoosshd can send is handled explicitly: an
// unhandled one would otherwise fall through with no certificate and no
// error, which is exactly the nil-error-success bug this phase fixes (see
// pam_ssoossh.go).
func outcomeCertificate(result *api.CertificateResult) (int, string, error) {
	if result == nil {
		return PamAuthErr, "", errors.New("the certificate request resolved with no outcome")
	}

	switch result.Status {
	case api.StatusApproved:
		if result.Certificate == "" {
			return PamAuthErr, "", errors.New("the request was approved but no certificate was delivered")
		}
		return PamSuccess, result.Certificate, nil
	case api.StatusDenied:
		return PamAuthErr, "", errors.New("the request was denied")
	case api.StatusExpired:
		return PamAuthErr, "", errors.New("the request expired before anyone approved it")
	case api.StatusFailed:
		// Deliberately PamAuthErr, not PamAuthInfoUnavail: the request
		// reached ssoosshd and was processed — this is a definitive "no"
		// from a reachable server (e.g. the signing backend rejected it),
		// not the network/connectivity problem PamAuthInfoUnavail and
		// classifyRequestError's fail-fast reasoning are about.
		return PamAuthErr, "", errors.New("ssoosshd could not issue the certificate")
	default:
		return PamAuthErr, "", fmt.Errorf("the server reported an unrecognized outcome %q", result.Status)
	}
}

// classifyRequestError turns an error from CreatePAMRequest or
// AwaitCertificate into a PAM return code. A context deadline or
// cancellation (timeout, or Ctrl-C at the sudo prompt — see
// docs/release-phase5-pam-client.md, "Timeouts and cancellation") means the
// attempt was abandoned, not that the server is unreachable, so it gets its
// own code and message; anything else is treated as the server being
// unreachable, which is PamAuthInfoUnavail's documented meaning ("cannot
// retrieve authentication information ... due to a network or hardware
// failure") and lets the PAM stack fall through to whatever comes next in
// /etc/pam.d rather than making an outage of the ssoossh server an outage of
// sudo on every host.
func classifyRequestError(err error) (int, error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return PamAuthErr, fmt.Errorf("timed out waiting for approval: %w", err)
	case errors.Is(err, context.Canceled):
		return PamAuthErr, fmt.Errorf("authentication was interrupted: %w", err)
	default:
		return PamAuthInfoUnavail, fmt.Errorf("could not reach the ssoossh server: %w", err)
	}
}
