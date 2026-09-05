package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/hostinfo"
)

// PendingRequest is a certificate request ssoosshd has created but nobody
// has resolved yet. It exists because creating and waiting are two separate
// calls on the wire, and the caller needs what came back from the first one
// — the URL a human has to open — before the second one blocks. A single
// create-and-wait call could only ever hand back the approval URL after the
// approval had already happened, which is no use to `ssh login`.
type PendingRequest struct {
	// RequestID is the request's UUID. It is also the capability that
	// authorizes waiting on the outcome, so treat it as a secret.
	RequestID string

	// ApprovalURL is the absolute URL a human opens to approve or deny.
	// ssoosshd returns it relative (it does not know its own public base
	// URL); this is that path joined to the server URL, since a caller
	// printing it for a human needs something openable.
	ApprovalURL string

	// eventsURL is the absolute SSE endpoint AwaitCertificate connects to.
	// Unexported: it is this package's business, and nothing outside builds
	// a PendingRequest that is meant to be waited on.
	eventsURL string
}

// CreateUserRequest implements Client.
//
// hc is what this machine reports about itself (internal/hostinfo). It
// carries LocalUsername and LocalHostname too, so the caller does not read
// the same two values twice, and the rest of the host context rides with
// them -- see apitypes.UserRequestBody for the set and what it is for.
func (c *HTTPClient) CreateUserRequest(ctx context.Context, hc hostinfo.HostContext, publicKey string, trustedCAFingerprints []string, opts RequestedOptions) (*PendingRequest, error) {
	return c.create(ctx, "/certs/user", apitypes.UserRequestBody{
		PublicKey:             publicKey,
		LocalUsername:         hc.Username,
		LocalHostname:         hc.Hostname,
		RequestingUser:        hc.RequestingUser,
		Process:               hc.Process,
		TTY:                   hc.TTY,
		RemoteHost:            hc.RemoteHost,
		CallerUID:             hc.CallerUID,
		CallerGID:             hc.CallerGID,
		CallerPID:             hc.CallerPID,
		CallerPPID:            hc.CallerPPID,
		MachineID:             hc.MachineID,
		OS:                    hc.OS,
		Client:                hc.Client,
		ClientTime:            hc.ClientTime,
		TrustedCAFingerprints: trustedCAFingerprints,
		RequestedOptions:      opts,
	})
}

// CreateServiceEnrollment implements Client.
func (c *HTTPClient) CreateServiceEnrollment(ctx context.Context, publicKey string, opts RequestedOptions) (*PendingRequest, error) {
	return c.create(ctx, "/certs/service/enroll", apitypes.ServiceEnrollRequestBody{
		PublicKey:        publicKey,
		RequestedOptions: opts,
	})
}

// CreatePAMRequest implements Client.
func (c *HTTPClient) CreatePAMRequest(ctx context.Context, publicKey, username string, opts RequestedOptions) (*PendingRequest, error) {
	return c.create(ctx, "/certs/pam", apitypes.PAMRequestBody{
		PublicKey:        publicKey,
		Username:         username,
		RequestedOptions: opts,
	})
}

// AwaitCertificate implements Client.
func (c *HTTPClient) AwaitCertificate(ctx context.Context, req *PendingRequest) (*CertificateResult, error) {
	if req == nil || req.eventsURL == "" {
		return nil, errors.New("cannot wait on a certificate request that this client did not create")
	}

	return waitForOutcome(ctx, c.tlsConfig, req.eventsURL)
}

// create POSTs body to path to create a pending certificate request and
// returns it without waiting for anyone to resolve it. Waiting is
// AwaitCertificate's job, over a separate connection — POST is not itself a
// stream under the SSE spec, and the two are separate on the wire for that
// reason.
func (c *HTTPClient) create(ctx context.Context, path string, body any) (*PendingRequest, error) {
	var created apitypes.Envelope[apitypes.CreateRequestResponse]
	if err := c.doJSON(ctx, http.MethodPost, path, body, &created); err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	return &PendingRequest{
		RequestID:   created.Data.RequestID,
		ApprovalURL: c.serverURL + created.Data.ApprovalURL,
		eventsURL:   c.serverURL + created.Data.EventsURL,
	}, nil
}
