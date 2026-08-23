package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().StringVar(&code, "code", "",
				"enrollment code (required)")
			cd.CobraCommand.Flags().StringVar(&keyPath, "key", "",
				"keypair path (relative or absolute; required)")
			cd.CobraCommand.Flags().BoolVar(&force, "force", false,
				"retrieve a new certificate even if a valid one exists on disk")
			cd.CobraCommand.Flags().StringVar(&grace, "grace", "1m",
				"duration of additional validity required before refreshing (default 1m); "+
					"e.g. 30s, 5m, 1h")
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

	graceDuration, err := time.ParseDuration(graceStr)
	if err != nil {
		return fmt.Errorf("invalid --grace duration: %w", err)
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
		respErr := &api.ResponseError{}
		if errors.As(err, &respErr) {
			err = errors.New("enrollment code not found or expired")
		} else {
			err = fmt.Errorf("retrieve certificate: %w", err)
		}

		// Check for a still-valid certificate on disk.
		if cert := reusableCertificateFile(certPath, reuseGrace); cert != nil {
			fmt.Fprintf(errOut, "WARNING: could not refresh certificate (%v), but %s is still valid %s\n",
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
