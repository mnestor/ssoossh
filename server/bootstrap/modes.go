package bootstrap

import (
	"context"

	"github.com/mnestor/ssoossh/server/logging"
)

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

// logStarting announces that the process is up and which mode it is in.
//
// Tagged rather than logged plainly so it survives the default logging
// level: logging.level defaults to WARN, so an ordinary Info record here
// prints nothing at all, and a correctly configured process looks
// identical to one that has hung. See server/logging's destination
// contract for the routing.
func logStarting(ctx context.Context, mode ServerMode) {
	logging.Tagged(logging.TagStartup).
		InfoContext(ctx, "ssoosshd is starting", "mode", mode.String())
}
