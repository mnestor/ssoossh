package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/api"
)

// enrollmentCodeEnvVar is where `service retrieve` looks for the code when
// --code is absent. An environment variable rather than a file because the
// code is not stored anywhere after `service enroll` prints it, and rather
// than a required flag because a command line is visible in ps output to
// every user on the host.
const enrollmentCodeEnvVar = "SSOOSSH_ENROLLMENT_CODE"

func newServiceRetrieveCommand() simplecobra.Commander {
	var code string
	var output string

	cmd := &simpleCommand{
		name:  "retrieve",
		short: "Retrieve a service certificate using a previously issued enrollment code.",
		long: "Posts only the enrollment code from `service enroll` — never resubmits the " +
			"public key — so a stolen code cannot be paired with an attacker's own keypair. " +
			"Codes are reusable; retries are safe for cron/systemd.\n\n" +
			"The code comes from --code or $" + enrollmentCodeEnvVar + "; nothing on disk " +
			"remembers it for you.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().StringVar(&code, "code", "",
				"enrollment code; if unset, read from $"+enrollmentCodeEnvVar)
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
		code = os.Getenv(enrollmentCodeEnvVar)
	}
	if code == "" {
		return fmt.Errorf("no enrollment code: pass --code or set $%s to the code `service enroll` printed",
			enrollmentCodeEnvVar)
	}

	certText, err := root.API().RetrieveServiceCertificate(ctx, code)
	if err != nil {
		respErr := &api.ResponseError{}
		if errors.As(err, &respErr) {
			return fmt.Errorf("enrollment code not found or expired")
		}
		return fmt.Errorf("retrieve certificate: %w", err)
	}

	if outputFlag != "" {
		if err := writeFileAtomic(outputFlag, []byte(certText), 0644); err != nil {
			return err
		}
	} else {
		fmt.Fprint(os.Stdout, certText)
	}

	return nil
}
