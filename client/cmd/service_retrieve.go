package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bep/simplecobra"
	"github.com/mnestor/ssoossh/internal/api"
)

func newServiceRetrieveCommand() simplecobra.Commander {
	var code string
	var output string

	cmd := &simpleCommand{
		name:  "retrieve",
		short: "Retrieve a service certificate using a previously issued enrollment code.",
		long: "Posts only the enrollment code from `service enroll` — never resubmits the " +
			"public key — so a stolen code cannot be paired with an attacker's own keypair. " +
			"Codes are reusable; retries are safe for cron/systemd.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().StringVar(&code, "code", "",
				"enrollment code; if unset, reads from the enrollment file in config")
			cd.CobraCommand.Flags().StringVar(&output, "output", "",
				"write certificate to this file instead of stdout")
			return nil
		},
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runServiceRetrieve(ctx, root, code, output)
		},
	}
	return cmd
}

func runServiceRetrieve(ctx context.Context, root *RootCommand, codeFlag, outputFlag string) error {
	code := codeFlag
	if code == "" {
		enrollment, err := loadEnrollment(root.Config().ServiceEnrollmentFile)
		if err != nil {
			return fmt.Errorf("load enrollment: %w", err)
		}
		code = enrollment.Code
	}

	certText, err := root.API().RetrieveServiceCertificate(ctx, code)
	if err != nil {
		if respErr, ok := err.(*api.ResponseError); ok && respErr.IsNotFound() {
			return fmt.Errorf("enrollment code not found or expired")
		}
		return fmt.Errorf("retrieve certificate: %w", err)
	}

	if outputFlag != "" {
		tmpfile, err := os.CreateTemp("", ".ssoossh-cert-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		defer os.Remove(tmpfile.Name())

		if _, err := tmpfile.WriteString(certText); err != nil {
			tmpfile.Close()
			return fmt.Errorf("write certificate file: %w", err)
		}
		if err := tmpfile.Close(); err != nil {
			return fmt.Errorf("close certificate file: %w", err)
		}

		if err := os.Rename(tmpfile.Name(), outputFlag); err != nil {
			return fmt.Errorf("write certificate file: %w", err)
		}

		if err := os.Chmod(outputFlag, 0644); err != nil {
			return fmt.Errorf("chmod certificate file: %w", err)
		}
	} else {
		fmt.Fprint(os.Stdout, certText)
	}

	return nil
}

func loadEnrollment(path string) (*ServiceEnrollment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read enrollment file: %w", err)
	}

	var enrollment ServiceEnrollment
	if err := json.Unmarshal(data, &enrollment); err != nil {
		return nil, fmt.Errorf("malformed enrollment file: %w", err)
	}

	return &enrollment, nil
}
