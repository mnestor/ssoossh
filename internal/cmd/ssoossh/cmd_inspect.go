// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"

	"github.com/mnestor/ssoossh/pkg/crypto/sshutil"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:     "inspect",
	Short:   "inspect key that exists in ssh-agent",
	RunE:    inspectRun,
	PreRunE: preRun,
}

func inspectRun(cmd *cobra.Command, args []string) error {
	agentCerts, err := agent.ListCertificates()
	if err != nil {
		return err
	}

	if len(agentCerts) == 0 {
		fmt.Fprintf(outWriter, "no certificates from our ca present in ssh-agent")
	}

	for _, agentKey := range agentCerts {
		inspect, err := sshutil.InspectCertificate(agentKey)
		if err != nil {
			return err
		}

		space := ""
		fmt.Println("")
		fmt.Println("Valid certificates that are signed by SSH Certificate Service")
		fmt.Println("")
		fmt.Printf("%8sType: %s %s certificate\n", space, inspect.KeyName, inspect.Type)
		fmt.Printf("%8sPublic key: %s-CERT %s\n", space, inspect.KeyAlgo, inspect.KeyFingerprint)
		fmt.Printf("%8sSigning CA: %s %s\n", space, inspect.SigningKeyAlgo, inspect.SigningKeyFingerprint)
		fmt.Printf("%8sKey ID: \"%s\"\n", space, inspect.KeyID)
		fmt.Printf("%8sSerial: %d\n", space, inspect.Serial)
		fmt.Printf("%8sValid: %s\n", space, inspect.Validity())
		fmt.Printf("%8sPrincipals: ", space)
		if len(inspect.Principals) == 0 {
			fmt.Println("(none)")
		} else {
			fmt.Println()
			for _, p := range inspect.Principals {
				fmt.Printf("%16s%s\n", space, p)
			}
		}
		fmt.Printf("%8sCritical Options: ", space)
		if len(inspect.CriticalOptions) == 0 {
			fmt.Println("(none)")
		} else {
			fmt.Println()
			for k, v := range inspect.CriticalOptions {
				fmt.Printf("%16s%s %v\n", space, k, v)
			}
		}
		fmt.Printf("%8sExtensions: ", space)
		if len(inspect.Extensions) == 0 {
			fmt.Println("(none)")
		} else {
			fmt.Println()
			for k, v := range inspect.Extensions {
				fmt.Printf("%16s%s %v\n", space, k, v)
			}
		}
	}

	return nil
}
