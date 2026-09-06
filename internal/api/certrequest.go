package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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

	approvalURL, err := joinServerPath(c.serverURL, "approval_url", created.Data.ApprovalURL)
	if err != nil {
		return nil, err
	}
	eventsURL, err := joinServerPath(c.serverURL, "events_url", created.Data.EventsURL)
	if err != nil {
		return nil, err
	}

	return &PendingRequest{
		RequestID:   created.Data.RequestID,
		ApprovalURL: approvalURL,
		eventsURL:   eventsURL,
	}, nil
}

// joinServerPath joins one of the relative URLs a create call returns onto
// the server's base URL, refusing any value that is not an absolute path.
//
// serverURL is a bare origin ("https://host" - normalizeServerURL strips
// any trailing slash and adds no path), so these two values are the whole
// path of the URL that results, and a value not rooted at "/" escapes the
// origin entirely. "@evil.example/x" concatenates to
// "https://host@evil.example/x", where "host" is userinfo and the actual
// host is evil.example; a leading "//" carries an authority the same way.
// The result is both fetched by this client and printed to a human as the
// link to approve, so a bad value is a redirect of the approval itself.
//
// This guards the contract, not a live bug: ssoosshd builds all four of
// these URLs from string literals plus a UUID or a Crockford code (see
// server/controller/certrequests.go), and apitypes.CreateRequestResponse
// documents them as relative. The C PAM module enforces the same rule on
// its side, and enforcing it here too means a server change that emitted a
// fully-qualified URL fails loudly in both clients rather than only in one.
func joinServerPath(serverURL, field, path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("server returned a %s that is not an absolute path: %q", field, path)
	}
	return serverURL + path, nil
}
