package ssoossh_context

type ContextKey int

const (
	ContextKeyConfig ContextKey = iota
	ContextKeyAPIClient
	ContextKeyAgent
	ContextKeyNeedAgent
)

func (k ContextKey) String() string {
	switch k {
	case ContextKeyConfig:
		return "config"
	case ContextKeyAPIClient:
		return "api_client"
	case ContextKeyAgent:
		return "agent"
	default:
		return "unknown"
	}
}

// Example usage in a command:
// func NewRootCmd() *cobra.Command {
//   return &cobra.Command{
//     Use:   "ssoossh",
//     Short: "SSO SSH client",
//     Run: func(cmd *cobra.Command, args []string) {
//       ctx := cmd.Context() // Retrieve the config from context
//       config := ctx.Value(ContextKeyConfig).(*Config)
//       // Use the config as needed
//     },
//     PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
//       // Initialize the context with config, apiClient, and agent
//       config := &Config{ /* initialize config */ }
//       apiClient := api.NewClient(config.Server)
//       agent := ssh.NewAgent()
//       ctx := context.WithValue(cmd.Context(), ContextKeyConfig, config)
//       ctx = context.WithValue(ctx, ContextKeyAPIClient, apiClient)
//       ctx = context.WithValue(ctx, ContextKeyAgent, agent)
//       cmd.SetContext(ctx)
//       return nil
//     },
//   }
// }
// This code defines a set of context keys for use in a command-line application.
// It provides a way to store and retrieve configuration, API client, and SSH agent instances in the command context.
// It also includes a function to get the string representation of a context key.
// This allows for better organization and access to shared resources across different parts of the application.
