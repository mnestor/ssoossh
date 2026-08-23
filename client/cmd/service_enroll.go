package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bep/simplecobra"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

func newServiceEnrollCommand() simplecobra.Commander {
	var keyPath string
	var retrieve bool

	cmd := &simpleCommand{
		name:  "enroll",
		short: "Enroll a service keypair for unattended certificate issuance.",
		long: "Enrolls a keypair bound by the server to an enrollment code for later " +
			"certificate retrieval. The keypair is either generated here or already " +
			"present on disk.\n\n" +
			"The code is printed once and stored nowhere. Save it where the unattended " +
			"job that runs `service retrieve` can read it.\n\n" +
			"The key files follow OpenSSH naming: the private key is <name>, the public " +
			"key is <name>.pub, and the certificate (once retrieved) is <name>-cert.pub. " +
			"The three files must be in the same directory for ssh to find them.\n\n" +
			"With --retrieve, the command immediately redeems the code once, writing the " +
			"certificate to <name>-cert.pub and reporting its details. If retrieval fails " +
			"after the code is printed, an error is returned but the code is not lost.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().StringVar(&keyPath, "key", "",
				"keypair path (relative or absolute); generates both if neither <name> nor <name>.pub "+
					"exist, enrolls the existing <name>.pub otherwise")
			cd.CobraCommand.Flags().BoolVar(&retrieve, "retrieve", false,
				"immediately redeem the code and write the certificate to <name>-cert.pub")
			return nil
		},
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runServiceEnroll(ctx, root, os.Stdout, keyPath, retrieve)
		},
	}
	return cmd
}

// runServiceEnroll enrolls a public key and prints the resulting enrollment
// code. See resolveServiceKey for how keyPath decides between generating a
// keypair and enrolling one that is already there.
func runServiceEnroll(ctx context.Context, root *RootCommand, out io.Writer, keyPath string, retrieve bool) error {
	if keyPath == "" {
		return errors.New("--key is required")
	}

	cfg := root.Config()

	publicKey, err := resolveServiceKey(cfg, keyPath)
	if err != nil {
		return err
	}

	pending, err := root.API().CreateServiceEnrollment(ctx, publicKey, api.RequestedOptions{})
	if err != nil {
		return fmt.Errorf("request enrollment: %w", err)
	}

	fmt.Fprintf(out, "This is for obtaining a code for automatic certificate issuance for a single account:\n\n")
	fmt.Fprintf(out, "Approve this request in your browser:\n\n    %s\n\n", pending.ApprovalURL)
	if cfg.TryOpenBrowser {
		openBrowser(ctx, out, pending.ApprovalURL)
	}
	fmt.Fprintln(out, "Waiting…")

	result, err := root.API().AwaitCertificate(ctx, pending)
	if err != nil {
		return describeWaitError(err)
	}

	code, err := enrollmentCode(result)
	if err != nil {
		return err
	}

	// Print the code and instructions.
	fmt.Fprintf(out, "\nYour enrollment code is: %s\n", code)
	printEnrollmentCodeAndPaths(out, keyPath)

	if retrieve {
		if err := retrieveRightAway(ctx, root, out, code, keyPath); err != nil {
			return err
		}
	}

	printEnrollmentGuidance(out, code, keyPath)
	return nil
}

// resolveServiceKey decides what to enroll for keyPath and returns the
// public key in authorized_keys form. What exists on disk decides:
//
//   - neither <keyPath> nor <keyPath>.pub: generate both
//   - both: enroll the existing <keyPath>.pub, leaving the private half alone
//   - only one of the two: an error naming the missing file
//
// The half-present cases are errors rather than something to work around
// because ssh will not use a certificate without the private key sitting
// beside it, so an enrollment built from a lone public key produces
// certificates nothing on this host can present.
func resolveServiceKey(cfg *config.Config, keyPath string) (string, error) {
	pubKeyPath := publicKeyPathFor(keyPath)

	privateExists, err := fileExists(keyPath)
	if err != nil {
		return "", err
	}
	publicExists, err := fileExists(pubKeyPath)
	if err != nil {
		return "", err
	}

	switch {
	case !privateExists && !publicExists:
		return generateServiceKeypair(cfg, keyPath)
	case privateExists && publicExists:
		pubData, err := os.ReadFile(pubKeyPath)
		if err != nil {
			return "", fmt.Errorf("read public key: %w", err)
		}
		return string(pubData), nil
	case publicExists:
		return "", fmt.Errorf("missing %s (ssh needs the private key beside the public one)", keyPath)
	default:
		return "", fmt.Errorf("missing %s (the public key must sit beside the private key)", pubKeyPath)
	}
}

// retrieveRightAway redeems code once and writes the certificate, so
// `--retrieve` proves the enrollment works rather than leaving the operator
// to find out when cron first runs. The code has already been printed by
// the time this is called: a failure here must not cost the operator the
// one copy of it they will ever see.
func retrieveRightAway(ctx context.Context, root *RootCommand, out io.Writer, code, keyPath string) error {
	certText, err := root.API().RetrieveServiceCertificate(ctx, code)
	if err != nil {
		return fmt.Errorf("retrieve certificate: %w", err)
	}

	if err := writeFileAtomic(certificatePathFor(keyPath), []byte(certText), 0644); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nYour certificate has been retrieved right away:\n")

	// ParseAuthorizedKey, not ParsePublicKey: the server returns the
	// certificate in authorized_keys text form, and ParsePublicKey wants
	// wire-format bytes, so it fails on every certificate it is handed.
	cert, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certText)) //nolint:dogsled // the comment/options/rest returns say nothing about a certificate.
	if err != nil {
		return fmt.Errorf("parse the retrieved certificate: %w", err)
	}
	sshCert, ok := cert.(*ssh.Certificate)
	if !ok {
		return fmt.Errorf("the server returned a public key, not a certificate")
	}
	writeCertificate(out, sshCert)
	return nil
}

// printEnrollmentGuidance prints the ssh_config recipe and the rules that
// make it work. Paths are absolute so a relative --key is unambiguous about
// where its files actually landed.
func printEnrollmentGuidance(out io.Writer, code, keyPath string) {
	keyAbs := absOrAsGiven(keyPath)
	certAbs := absOrAsGiven(certificatePathFor(keyPath))
	pubAbs := absOrAsGiven(publicKeyPathFor(keyPath))

	fmt.Fprintln(out, "\nTo automatically refresh the certificate from ssh_config before each connection add to your ~/.ssh/config")
	fmt.Fprintf(out, "  Match user USERNAME exec 'ssoossh service retrieve --code %s --key %s'\n", code, keyAbs)
	fmt.Fprintf(out, "    IdentityFile %s\n", keyAbs)
	fmt.Fprintln(out, "    IdentitiesOnly yes")
	fmt.Fprintf(out, "\nNo CertificateFile line is needed - ssh derives %s from\n", certAbs)
	fmt.Fprintln(out, "IdentityFile's name, which is why the three names above are not negotiable.")
	fmt.Fprintln(out, "Match exec runs before ssh reads the key files, so this needs no agent.")
	fmt.Fprintln(out, "\nYour certificate will be automatically updated if it is expired or will expire within 1 minute.")
	fmt.Fprintln(out, "This can be changed by adding --grace <time> to the retrieve call, or forced every time with")
	fmt.Fprintln(out, "--force. <time> is a number followed by s=seconds, m=minutes, or h=hours.")
	fmt.Fprintln(out, "\nThe code is reusable, so that is safe to run from cron or a systemd timer.")
	fmt.Fprintln(out, "\nThe code only works with the key it was enrolled against. service retrieve never sends a public")
	fmt.Fprintf(out, "key, so the certificate it returns is always bound to %s and does nothing\n", pubAbs)
	fmt.Fprintln(out, "without the corresponding private key.")
}

// absOrAsGiven renders path absolute for display, falling back to what the
// caller passed. A display path is never worth failing an enrollment over.
func absOrAsGiven(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// fileExists returns true if the file exists, false if it does not, or an
// error if the stat fails for other reasons.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// printEnrollmentCodeAndPaths names the three files, absolute so a relative
// --key is unambiguous about where they landed. The certificate is listed
// even when it does not exist yet: the name is fixed by the private key's,
// and knowing it is what lets the operator write the retrieve command.
func printEnrollmentCodeAndPaths(out io.Writer, keyPath string) {
	fmt.Fprintf(out, "ssh key files are:\n")
	fmt.Fprintf(out, "  Private key: %s\n", absOrAsGiven(keyPath))
	fmt.Fprintf(out, "  Public key:  %s\n", absOrAsGiven(publicKeyPathFor(keyPath)))
	fmt.Fprintf(out, "  Certificate: %s\n", absOrAsGiven(certificatePathFor(keyPath)))
}

// generateServiceKeypair generates a keypair for the enrollment, writing
// the private key to privateKeyPath (0600) and the public key alongside it
// as <path>.pub (0644), and returns the public key in authorized_keys
// form. Neither file is overwritten if it already exists: clobbering a
// private key destroys every certificate that depends on it.
func generateServiceKeypair(cfg *config.Config, privateKeyPath string) (string, error) {
	publicKeyPath := publicKeyPathFor(privateKeyPath)
	for _, path := range []string{privateKeyPath, publicKeyPath} {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("%s already exists; remove it or choose another path for --generate", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("check %s: %w", path, err)
		}
	}

	algorithm, size, _, err := cfg.ResolveSSHKey()
	if err != nil {
		return "", fmt.Errorf("invalid ssh key configuration: %w", err)
	}

	kp, err := keypair.NewSSHKeypair(algorithm, size)
	if err != nil {
		return "", fmt.Errorf("generate keypair: %w", err)
	}

	publicKey, err := kp.MarshalAuthorizedKey()
	if err != nil {
		return "", fmt.Errorf("encode public key: %w", err)
	}
	privatePEM, err := kp.MarshalPrivateKey()
	if err != nil {
		return "", fmt.Errorf("encode private key: %w", err)
	}

	// Private key first: a public key with no private half beside it is a
	// clearer failure than the reverse.
	if err := writeFileAtomic(privateKeyPath, privatePEM, 0600); err != nil {
		return "", err
	}
	if err := writeFileAtomic(publicKeyPath, []byte(publicKey), 0644); err != nil {
		return "", err
	}

	return publicKey, nil
}

// publicKeyPathFor is the OpenSSH convention: the public key sits beside
// the private key with a .pub suffix.
func publicKeyPathFor(privateKeyPath string) string { return privateKeyPath + ".pub" }

// certificatePathFor is the OpenSSH convention for where a certificate
// belongs relative to its key: id_ed25519 and id_ed25519.pub both give
// id_ed25519-cert.pub.
func certificatePathFor(keyPath string) string {
	return strings.TrimSuffix(keyPath, ".pub") + "-cert.pub"
}

// enrollmentCode extracts the enrollment code from a resolved service
// request. Unlike ssh login's checkOutcome, the outcome this path wants is
// "enrolled" — the server mints a code at approval, and the certificate
// itself is only issued later when `service retrieve` redeems it.
func enrollmentCode(result *api.CertificateResult) (string, error) {
	if result == nil {
		return "", errors.New("the enrollment request resolved with no outcome")
	}

	switch result.Status {
	case api.StatusEnrolled:
		if result.Code == "" {
			return "", errors.New("the request was enrolled but no code was delivered; run service enroll again")
		}
		return result.Code, nil
	case api.StatusDenied:
		return "", errors.New("the request was denied, so no enrollment was created")
	case api.StatusExpired:
		return "", errors.New("the request expired before anyone approved it; run service enroll again")
	case api.StatusFailed:
		return "", errors.New("ssoosshd could not create the enrollment; check the server logs, then run service enroll again")
	default:
		return "", fmt.Errorf("the server reported an unrecognized outcome %q", result.Status)
	}
}
