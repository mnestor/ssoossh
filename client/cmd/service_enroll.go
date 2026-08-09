package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newServiceEnrollCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "enroll",
		short: "Enroll a service keypair for unattended certificate issuance.",
		long: "The keypair is either operator-supplied (the server never sees the private " +
			"half) or client-generated. After OIDC approval, the server returns an " +
			"enrollment code bound to both the public key and the authorized option set; " +
			"`service retrieve` posts only that code on later invocations.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "service enroll"}
		},
	}
}
