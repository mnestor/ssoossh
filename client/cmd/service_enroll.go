package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// ServiceEnrollment holds the enrollment code and public key for later retrieval.
type ServiceEnrollment struct {
	Code               string `json:"code"`
	PublicKey          string `json:"public_key"`
	PrivateKeyFile     string `json:"private_key_file,omitempty"`
	PrivateKeyMaterial string `json:"private_key_material,omitempty"`
}

func newServiceEnrollCommand() simplecobra.Commander {
	var keyPath string

	cmd := &simpleCommand{
		name:  "enroll",
		short: "Enroll a service keypair for unattended certificate issuance.",
		long: "The keypair is either operator-supplied (the server never sees the private " +
			"half) or client-generated. After OIDC approval, the server returns an " +
			"enrollment code bound to both the public key and the authorized option set; " +
			"`service retrieve` posts only that code on later invocations.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().StringVar(&keyPath, "key", "",
				"path to an existing public key file (authorized_keys format); "+
					"if unset, a keypair is generated")
			return nil
		},
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runServiceEnroll(ctx, root, os.Stdout, keyPath)
		},
	}
	return cmd
}

func runServiceEnroll(ctx context.Context, root *RootCommand, out io.Writer, keyPath string) error {
	cfg := root.Config()
	var publicKey string
	var kp *keypair.SSHKeypair
	var err error

	if keyPath != "" {
		pubData, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("read public key file: %w", err)
		}
		publicKey = string(pubData)
	} else {
		algorithm, size, _, err := cfg.ResolveSSHKey()
		if err != nil {
			return fmt.Errorf("invalid ssh key configuration: %w", err)
		}

		kp, err = keypair.NewSSHKeypair(algorithm, size)
		if err != nil {
			return fmt.Errorf("generate keypair: %w", err)
		}

		pubBytes, err := kp.MarshalAuthorizedKey()
		if err != nil {
			return fmt.Errorf("encode public key: %w", err)
		}
		publicKey = pubBytes
	}

	pending, err := root.API().CreateServiceEnrollment(ctx, publicKey, api.RequestedOptions{})
	if err != nil {
		return fmt.Errorf("request enrollment: %w", err)
	}

	fmt.Fprintf(out, "Approve this request in your browser:\n\n    %s\n\n", pending.ApprovalURL)
	if cfg.TryOpenBrowser {
		openBrowser(ctx, out, pending.ApprovalURL)
	}
	fmt.Fprintln(out, "Waiting for approval…")

	result, err := root.API().AwaitCertificate(ctx, pending)
	if err != nil {
		return describeWaitError(err)
	}

	code, err := enrollmentCode(result)
	if err != nil {
		return err
	}

	enrollment := ServiceEnrollment{
		Code:      code,
		PublicKey: publicKey,
	}
	if kp != nil {
		privPEM, err := kp.MarshalPrivateKey()
		if err != nil {
			return fmt.Errorf("encode private key: %w", err)
		}
		enrollment.PrivateKeyMaterial = string(privPEM)
	} else {
		enrollment.PrivateKeyFile = keyPath
	}

	enrollmentPath := cfg.ServiceEnrollmentFile
	if err := saveEnrollment(enrollmentPath, enrollment); err != nil {
		return err
	}

	fmt.Fprintf(out, "Enrollment code saved to %s.\n", enrollmentPath)
	fmt.Fprintf(out, "Run `ssoossh service retrieve` to fetch certificates.\n")
	return nil
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

// saveEnrollment persists the enrollment file. 0600 because the code
// redeems certificates until the enrollment expires, and the private key
// material may be inline.
func saveEnrollment(path string, enrollment ServiceEnrollment) error {
	if path == "" {
		return errors.New("service_enrollment_file not configured")
	}
	data, err := json.MarshalIndent(enrollment, "", "  ")
	if err != nil {
		return fmt.Errorf("encode enrollment: %w", err)
	}
	return writeFileAtomic(path, data, 0600)
}
