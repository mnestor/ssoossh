package ssh

import (
	_ "embed"

	config "github.com/mnestor/ssoossh/internal/config/client"

	"github.com/spf13/cobra"
	"github.com/thediveo/enumflag"
)

//go:embed root_example.txt
var exampleText string

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ssh",
		Short:   "Manage SSH certificates",
		Long:    `Manage SSH certificates using Single Sign-On (SSO) on Secure Shell (SSH).`,
		Example: exampleText,
	}

	var kt config.KeyType
	keyType := enumflag.New(&kt, "key-type", config.KeyTypeNames, enumflag.EnumCaseInsensitive)
	cmd.PersistentFlags().VarP(keyType, "key-type", "t", "Set the key type for SSH certificates (ed25519, rsa).")
	cmd.PersistentFlags().IntP("key-size", "b", 4096, "Set the key size for RSA keys (default 4096). Ignored for ed25519 keys.")

	cmd.PersistentFlags().BoolP("use-agent", "a", true, "Use SSH agent for key management (default true).")
	cmd.PersistentFlags().BoolP("fallback-file-agent", "f", true, "Use file-based key management if SSH agent is not available (default false).")

	cmd.DisableFlagsInUseLine = true

	cmd.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newProxyCmd(),
		newConfigCmd(),
	)

	return cmd
}
