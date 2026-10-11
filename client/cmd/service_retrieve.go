package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/bep/simplecobra"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/api"
)

func newServiceRetrieveCommand() simplecobra.Commander {
	var code string
	var keyPath string
	var force bool
	var grace string

	cmd := &simpleCommand{
		name:  "retrieve",
		short: "Retrieve a service certificate using a previously issued enrollment code.",
		long: "Redeems an enrollment code from `service enroll`, writing the certificate to " +
			"<key>-cert.pub and checking local disk to avoid unnecessary server calls.\n\n" +
			"The certificate is skipped (exit 0) if one at <key>-cert.pub is valid beyond --grace " +
			"(default 1 minute) unless --force is passed. If retrieval fails but a still-valid " +
			"certificate exists, a warning is printed and exit 0 is returned (so `Match exec` " +
			"does not block SSH when the server is temporarily unreachable). Only when there is " +
			"no readable, valid certificate does an error return a non-zero exit.\n\n" +
			"The key files follow OpenSSH naming: the private key is <name>, the public key is " +
			"<name>.pub, and the certificate is <name>-cert.pub.",
		example: "  # From cron or a systemd timer: refresh only when the certificate is about to expire\n" +
			"  ssoossh service retrieve --code \"$ENROLLMENT_CODE\" --key /var/lib/deploy/.ssh/id_deploy\n\n" +
			"  # Fetch a new certificate even if the one on disk is still valid\n" +
			"  ssoossh service retrieve --code \"$ENROLLMENT_CODE\" --key /var/lib/deploy/.ssh/id_deploy --force",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().StringVar(&code, "code", "",
				"enrollment code (required)")
			cd.CobraCommand.Flags().StringVar(&keyPath, "key", "",
				"keypair path (relative or absolute; required)")
			cd.CobraCommand.Flags().BoolVar(&force, "force", false,
				"retrieve a new certificate even if a valid one exists on disk")
			cd.CobraCommand.Flags().StringVar(&grace, "grace", "1m",
				"how much validity a certificate on disk must still have to be kept, e.g. 30s, 5m, 1h")
			return nil
		},
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runServiceRetrieve(ctx, root, os.Stderr, code, keyPath, force, grace)
		},
	}
	return cmd
}

// runServiceRetrieve has three possible outcomes:
// 1. skipped: certificate on disk is valid beyond grace period; writes message to stderr and returns nil
// 2. refreshed: certificate was retrieved and written to disk
// 3. degraded: retrieval failed but a valid certificate exists on disk; writes warning and returns nil
// An error is returned only when retrieval fails AND there is no usable certificate on disk.
func runServiceRetrieve(ctx context.Context, root *RootCommand, errOut io.Writer, code, keyPath string, force bool, graceStr string) error {
	if code == "" {
		return errors.New("--code is required")
	}
	if keyPath == "" {
		return errors.New("--key is required")
	}

	graceDuration, err := parseGrace(graceStr)
	if err != nil {
		return err
	}

	certPath := certificatePathFor(keyPath)

	// Check if we can skip retrieval.
	if !force {
		if cert := reusableCertificateFile(certPath, graceDuration); cert != nil {
			fmt.Fprintf(errOut, "certificate at %s is valid %s; pass --force to replace it\n",
				certPath, expiryPhrase(cert))
			return nil
		}
	}

	// Attempt to retrieve the certificate.
	certText, err := root.API().RetrieveServiceCertificate(ctx, code)
	if err != nil {
		// Retrieval failed. Check if we have a degraded certificate to fall back to.
		err = describeRetrieveError(err)

		// Check for a still-valid certificate on disk.
		if cert := reusableCertificateFile(certPath, reuseGrace); cert != nil {
			fmt.Fprintf(errOut, "Warning: could not refresh certificate (%v), but %s is still valid %s\n",
				err, certPath, expiryPhrase(cert))
			return nil
		}

		// No usable certificate on disk; the retrieval failure is fatal.
		return err
	}

	// Refreshed. Silent on success: this runs from cron, a systemd timer and
	// ssh_config's Match exec, none of which want a certificate dump per
	// invocation. `service enroll --retrieve` prints the details once, and
	// `ssh inspect` shows them on demand.
	return writeFileAtomic(certPath, []byte(certText), 0644)
}

// reusableCertificateFile reads a certificate from disk, parses it, and
// returns it only if it is an ssh.Certificate with validity extending beyond
// now + within. Any error (missing file, unparseable, not a certificate)
// returns nil meaning "go retrieve".
func reusableCertificateFile(path string, within time.Duration) *ssh.Certificate {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	cert, _, _, _, err := ssh.ParseAuthorizedKey(data) //nolint:dogsled // the comment/options/rest returns say nothing about a certificate.
	if err != nil {
		return nil
	}

	sshCert, ok := cert.(*ssh.Certificate)
	if !ok {
		return nil
	}

	cutoff := uint64(time.Now().Add(within).Unix()) //nolint:gosec // a Unix timestamp is positive for any real date
	if sshCert.ValidBefore > cutoff {
		return sshCert
	}

	return nil
}

// retrieveError is a retrieval failure worded for the person or job running
// `service retrieve`, with the underlying error kept for errors.As.
type retrieveError struct {
	msg string
	err error
}

// Error implements error.
func (e *retrieveError) Error() string { return e.msg }

// Unwrap returns the underlying error.
func (e *retrieveError) Unwrap() error { return e.err }

// describeRetrieveError says what a failed retrieval means for the caller.
// The HTTP status decides it: an unknown or expired code needs a new
// enrollment, a refusal or rate limit needs a different response, and a
// server failure is not the code's fault at all. Calling all of them "code
// not found or expired" sent operators to re-enroll over a server outage.
func describeRetrieveError(err error) error {
	respErr := &api.ResponseError{}
	if !errors.As(err, &respErr) {
		return fmt.Errorf("retrieve certificate: %w", err)
	}

	detail := fmt.Sprintf("status %d", respErr.StatusCode)
	if respErr.Message != "" {
		detail += ": " + respErr.Message
	}

	var msg string
	switch code := respErr.StatusCode; {
	case code == http.StatusNotFound || code == http.StatusGone:
		msg = "enrollment code not found or expired; run 'ssoossh service enroll' to get a new one"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		msg = fmt.Sprintf("the server refused this enrollment code (%s)", detail)
	case code == http.StatusTooManyRequests:
		msg = "the server is rate limiting retrieval from this address; retry in a minute"
	case code >= http.StatusInternalServerError:
		msg = fmt.Sprintf("the server failed to issue the certificate (%s). This is a server problem, "+
			"not your code; retry shortly or contact the ssoossh administrator", detail)
	default:
		msg = fmt.Sprintf("the server rejected the retrieval (%s)", detail)
	}
	return &retrieveError{msg: msg, err: err}
}

// parseGrace reads --grace. time.ParseDuration's own errors ("time: missing
// unit in duration \"5\"") name the parser rather than the flag, and it
// accepts a negative duration, which would make an expired certificate
// count as fresh.
func parseGrace(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, &usageError{msg: fmt.Sprintf("invalid value %q for --grace: use a number with a unit, e.g. 30s, 5m or 1h", value)}
	}
	if d < 0 {
		return 0, &usageError{msg: fmt.Sprintf("invalid value %q for --grace: it must not be negative", value)}
	}
	return d, nil
}
