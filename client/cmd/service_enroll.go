package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
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

	if err := checkOutcome(result); err != nil {
		return err
	}

	// Service enrollments return a code, not a certificate
	code := result.Certificate

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

	data, err := json.MarshalIndent(enrollment, "", "  ")
	if err != nil {
		return fmt.Errorf("encode enrollment: %w", err)
	}

	tmpfile, err := ioutil.TempFile("", ".ssoossh-enroll-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(data); err != nil {
		tmpfile.Close()
		return fmt.Errorf("write enrollment file: %w", err)
	}
	if err := tmpfile.Close(); err != nil {
		return fmt.Errorf("close enrollment file: %w", err)
	}

	enrollmentPath := cfg.ServiceEnrollmentFile
	if err := os.Rename(tmpfile.Name(), enrollmentPath); err != nil {
		return fmt.Errorf("write enrollment file: %w", err)
	}

	if err := os.Chmod(enrollmentPath, 0600); err != nil {
		return fmt.Errorf("chmod enrollment file: %w", err)
	}

	fmt.Fprintf(out, "Enrollment code saved to %s.\n", enrollmentPath)
	fmt.Fprintf(out, "Run `ssoossh service retrieve` to fetch certificates.\n")
	return nil
}
