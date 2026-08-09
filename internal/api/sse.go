package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"resty.dev/v3"
)

// reconnectDelay is how long waitForOutcome waits before reconnecting after
// an established events connection drops mid-stream (see waitForOutcome's
// doc comment for why that's this package's job, not SSESource's).
const reconnectDelay = 250 * time.Millisecond

// terminal certificate-request statuses — the only event names ssoosshd's
// events endpoint ever sends (see server/controller/certrequests.go's
// eventsHandler): it blocks until the request resolves, then sends exactly
// one of these and closes.
const (
	StatusApproved = "approved"
	StatusDenied   = "denied"
	StatusExpired  = "expired"
)

// waitForOutcome opens a real SSE connection (via resty's SSESource, per
// the SSE spec: GET, no body) to eventsURL and blocks until a terminal
// event arrives, decoding it into a CertificateResult.
//
// resty's SSESource only retries/backs off failures during the initial
// connect (its retryConditions cover status 0/429/5xx there) — once a
// stream is actually established, an unexpected mid-stream disconnect
// (idle-timed-out proxy, network blip) makes Get() return directly rather
// than reconnecting on its own. Since ssoosshd's Wait is idempotent per
// requestID (see server/service/certrequest.go's Wait doc comment) — a
// fresh connection to the same events URL always picks up correctly
// wherever the request actually is — it's always correct to just
// reconnect after a failure like that, so this function's outer loop does
// that itself rather than relying on SSESource for it.
func waitForOutcome(ctx context.Context, tlsConfig *tls.Config, eventsURL string) (*CertificateResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		once   sync.Once
		result *CertificateResult
		outErr error
	)
	finish := func(r *CertificateResult, err error) {
		once.Do(func() {
			result, outErr = r, err
			cancel()
		})
	}

	source := newCertificateEventSource(ctx, tlsConfig, eventsURL, finish)

	for {
		getErr := source.Get()

		if result != nil || outErr != nil {
			return result, outErr
		}
		if ctx.Err() != nil {
			if getErr != nil {
				return nil, fmt.Errorf("failed to read certificate request events: %w", getErr)
			}
			return nil, ctx.Err()
		}

		// Get() returned with no terminal outcome and our own context
		// still live — a mid-stream drop rather than a definitive
		// connect-time failure (which would already have called finish()
		// via OnRequestFailure above). Reconnect after a short delay.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

// newCertificateEventSource builds the SSESource waitForOutcome drives:
// finish is called at most once, either from OnRequestFailure (a
// definitive connect-time failure, e.g. ssoosshd's 404 for an unknown
// request ID) or from one of the three terminal-status event listeners.
func newCertificateEventSource(ctx context.Context, tlsConfig *tls.Config, eventsURL string, finish func(*CertificateResult, error)) *resty.SSESource {
	source := resty.NewSSESource().
		SetContext(ctx).
		SetURL(eventsURL).
		SetTLSClientConfig(tlsConfig).
		OnRequestFailure(func(err error, res *http.Response) {
			if res != nil {
				finish(nil, decodeSSEConnectError(res, err))
				return
			}
			finish(nil, fmt.Errorf("failed to connect to certificate request events stream: %w", err))
		})

	for _, status := range []string{StatusApproved, StatusDenied, StatusExpired} {
		status := status
		source.AddEventListener(status, func(e any) {
			evt, ok := e.(*resty.SSE)
			if !ok {
				finish(nil, fmt.Errorf("unexpected SSE event value type %T", e))
				return
			}

			out := &CertificateResult{Status: status}
			if evt.Data != "" {
				if err := json.Unmarshal([]byte(evt.Data), out); err != nil {
					finish(nil, fmt.Errorf("failed to decode certificate request event %q: %w", status, err))
					return
				}
			}
			finish(out, nil)
		}, nil)
	}

	return source
}

// decodeSSEConnectError builds a *ResponseError from a failed SSE connect
// attempt's HTTP response (e.g. ssoosshd's 404 for an unknown request ID —
// see server/service/certrequest.go's Wait). Mirrors decodeResponseError's
// body-shape handling but reads directly off the http.Response resty's
// SSESource hands to OnRequestFailure, since that's a different type than
// the *resty.Response decodeResponseError works with.
func decodeSSEConnectError(res *http.Response, connectErr error) *ResponseError {
	respErr := &ResponseError{StatusCode: res.StatusCode, Message: connectErr.Error()}

	if res.Body == nil {
		return respErr
	}
	defer func() { _ = res.Body.Close() }()

	var body errorBody
	if err := json.NewDecoder(res.Body).Decode(&body); err == nil && body.Error != "" {
		respErr.Message = body.Error
	}

	return respErr
}
