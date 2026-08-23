package bootstrap

// ServerMode specifies which mode to run the server in.
type ServerMode int

const (
	// ServerModeFull runs the complete server: webserver + listener + in-process signer.
	// This is suitable for single-instance deployments.
	ServerModeFull ServerMode = iota

	// ServerModeAPI runs just the webserver and listener, no signer.
	// Requires a separate signer process and a shared message broker (NATS).
	ServerModeAPI

	// SignerModeOnly runs just the signer component: consumes signing requests
	// and publishes signed certificates. No database, HTTP server, or OIDC/LDAP.
	// Requires NATS for communication with API instances.
	SignerModeOnly
)

// String returns a human-readable name for the mode.
func (m ServerMode) String() string {
	switch m {
	case ServerModeFull:
		return "full"
	case ServerModeAPI:
		return "api"
	case SignerModeOnly:
		return "sign"
	default:
		return "unknown"
	}
}
