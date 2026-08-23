package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/api"
)

// offlineCommander is implemented by commands that must complete without
// contacting the server. RootCommand.PreRun asks the invoked command
// before building anything that reaches out, so "this command does not use
// the network" is declared once, next to the command's own definition,
// rather than as a condition threaded through shared init.
type offlineCommander interface {
	Offline() bool
}

// isOffline reports whether cmd declared itself offline. A Commander that
// doesn't implement offlineCommander is treated as online, which is the
// right default for this seam: forgetting to declare costs a command some
// needless initialization, whereas defaulting the other way would let a
// command that does need the server run without one.
func isOffline(cmd simplecobra.Commander) bool {
	oc, ok := cmd.(offlineCommander)
	return ok && oc.Offline()
}

var _ api.Client = (*offlineAPIClient)(nil)

// offlineAPIClient is the api.Client an offline command gets instead of a
// real one. PreRun could leave the client nil, but then an offline command
// that later grows a server call would panic in production; a client whose
// every method refuses turns the same mistake into a named error at the
// call site, and lets a test assert the refusal directly.
type offlineAPIClient struct {
	// command is the invoked command's full path, so the error says which
	// command was supposed to be offline rather than just that one was.
	command string
}

// refuse builds the error every method returns, naming both the offline
// command and the call it tried to make.
func (c *offlineAPIClient) refuse(call string) error {
	return fmt.Errorf("%s runs offline and must not contact the server: %s", c.command, call)
}

// GetCA implements api.Client.
func (c *offlineAPIClient) GetCA(ctx context.Context) (string, error) {
	return "", c.refuse("GetCA")
}

// CreateUserRequest implements api.Client.
func (c *offlineAPIClient) CreateUserRequest(ctx context.Context, publicKey, localUsername, localHostname string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	return nil, c.refuse("CreateUserRequest")
}

// CreateServiceEnrollment implements api.Client.
func (c *offlineAPIClient) CreateServiceEnrollment(ctx context.Context, publicKey string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	return nil, c.refuse("CreateServiceEnrollment")
}

// CreatePAMRequest implements api.Client.
func (c *offlineAPIClient) CreatePAMRequest(ctx context.Context, publicKey, username string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	return nil, c.refuse("CreatePAMRequest")
}

// AwaitCertificate implements api.Client.
func (c *offlineAPIClient) AwaitCertificate(ctx context.Context, req *api.PendingRequest) (*api.CertificateResult, error) {
	return nil, c.refuse("AwaitCertificate")
}

// RetrieveServiceCertificate implements api.Client.
func (c *offlineAPIClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	return "", c.refuse("RetrieveServiceCertificate")
}
