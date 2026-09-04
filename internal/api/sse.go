package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// reconnectDelay is how long waitForOutcome waits before reconnecting after
// an established events connection drops mid-stream (see waitForOutcome's
// doc comment for why that's this package's job).
const reconnectDelay = 250 * time.Millisecond

// maxEventBytes caps a single line of the events stream. ssoosshd sends one
// event carrying one certificate, so the real figure is a couple of KB; the
// cap is here because this code runs inside sudo, where an endpoint that
// streamed without ever ending a line would otherwise grow the host
// process's heap without bound.
const maxEventBytes = 1 << 20

// Terminal certificate-request statuses, re-exported from the shared wire
// contract so existing callers of api.StatusApproved keep working. The
// canonical list lives in internal/apitypes — see apitypes.TerminalStatuses,
// which readOutcome treats as terminal. Recognizing fewer than the full set
// means an unlisted status arrives as an *informational* event and the
// client blocks forever waiting for a terminal one that never comes.
const (
	StatusApproved = apitypes.StatusApproved
	StatusDenied   = apitypes.StatusDenied
	StatusExpired  = apitypes.StatusExpired
	StatusEnrolled = apitypes.StatusEnrolled
	StatusFailed   = apitypes.StatusFailed
)

// errStreamEnded reports that an established events connection ended
// without resolving the request — a dropped stream rather than an answer.
// It wraps the read error that ended it, when there was one; a server that
// simply closed the connection produces it bare.
var errStreamEnded = errors.New("certificate request events stream ended without an outcome")

// waitForOutcome opens a real SSE connection (per the SSE spec: GET, no
// body) to eventsURL and blocks until a terminal event arrives, decoding it
// into a CertificateResult.
//
// A connection that is established and then drops (idle-timed-out proxy,
// network blip) is not a failure to report: ssoosshd's Wait is idempotent
// per requestID (see server/service/certrequest.go's Wait doc comment), so
// a fresh connection to the same events URL always picks up wherever the
// request actually is. This function reconnects in that case and keeps
// waiting, bounded only by ctx. So does a connect refused with a status
// that says to come back (see retryableConnect).
//
// Any other refusal is definitive and returned as it is: ssoosshd's 404 for
// an unknown request ID, its 410 for a certificate no longer available, and
// a transport error. The last is a deliberate choice rather than the
// obvious one — the request is probably still pending server-side, so
// retrying would sometimes work. But this code runs inside sudo with
// nothing on the terminal to say why it is waiting, and "connection
// refused" now is worth more to whoever is standing there than the same
// answer a minute from now.
func waitForOutcome(ctx context.Context, tlsConfig *tls.Config, eventsURL string) (*CertificateResult, error) {
	// One client across reconnects, so a dropped stream can reuse the
	// transport's connection pool rather than redialing and re-handshaking.
	client := &http.Client{Transport: newTransport(tlsConfig)}

	for {
		result, err := readOutcome(ctx, client, eventsURL)
		if !errors.Is(err, errStreamEnded) {
			return result, err
		}

		select {
		case <-ctx.Done():
			// err rather than ctx.Err(): what the last connection actually
			// did is more use in a bug report than "context canceled"
			// alone, and it carries the cancellation anyway when that is
			// what ended the read.
			return nil, err
		case <-time.After(reconnectDelay):
		}
	}
}

// readOutcome opens one events connection and reads it until a terminal
// event arrives or the stream ends. An error wrapping errStreamEnded means
// the request is still unresolved and reconnecting is the right response;
// any other error is definitive.
func readOutcome(ctx context.Context, client *http.Client, eventsURL string) (*CertificateResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to certificate request events stream: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	// An intermediate cache holding an events stream would serve a stale
	// outcome, or none at all, forever.
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to certificate request events stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		if retryableConnect(resp.StatusCode) {
			return nil, fmt.Errorf("%w: %w", errStreamEnded, responseError(resp))
		}
		return nil, responseError(resp)
	}

	terminal := apitypes.TerminalStatuses()
	var (
		result *CertificateResult
		outErr error
	)
	readErr := readEvents(resp.Body, func(evt sseEvent) bool {
		if !slices.Contains(terminal, evt.name) {
			return true // informational: keep reading for a terminal one
		}
		result, outErr = decodeOutcome(evt)
		return false
	})

	switch {
	case outErr != nil:
		return nil, outErr
	case result != nil:
		return result, nil
	case readErr != nil:
		return nil, fmt.Errorf("%w: %w", errStreamEnded, readErr)
	default:
		return nil, errStreamEnded
	}
}

// decodeOutcome turns a terminal event into a CertificateResult. The event
// data is enveloped like every other JSON body the API emits, so the status
// comes from the event name and the payload from the envelope's data half.
func decodeOutcome(evt sseEvent) (*CertificateResult, error) {
	out := &CertificateResult{Status: evt.name}
	if evt.data == "" {
		return out, nil
	}

	var envelope apitypes.Envelope[CertificateResult]
	if err := json.Unmarshal([]byte(evt.data), &envelope); err != nil {
		return nil, fmt.Errorf("failed to decode certificate request event %q: %w", evt.name, err)
	}
	if envelope.Error != "" {
		return nil, fmt.Errorf("certificate request %s: %s", evt.name, envelope.Error)
	}

	// The whole decoded payload, not a field-by-field copy: a new field on
	// CertificateResult would otherwise decode correctly here and then be
	// silently dropped on the way out. Status is reapplied because it comes
	// from the event name and is excluded from the JSON.
	*out = envelope.Data
	out.Status = evt.name
	return out, nil
}

// retryableConnect reports whether a refused connect is worth coming back
// for. A rate limit or a server-side fault is transient and the certificate
// request is still pending behind it, so reconnecting is how the wait
// survives an ssoosshd restart or a burst of load. A 4xx other than 429 is
// ssoosshd's final answer about this request, and repeating the connect
// would only collect it again.
func retryableConnect(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

// sseEvent is one dispatched server-sent event.
type sseEvent struct {
	// name is the "event:" field, "" for an event that carried none. The
	// SSE spec calls that a "message" event; ssoosshd always names its own,
	// since the name is what carries the status.
	name string

	// data is the "data:" field, with multiple such lines joined by
	// newlines as the spec requires.
	data string
}

// readEvents parses the SSE stream in body, calling onEvent for each
// dispatched event and stopping early when it returns false. It returns the
// error that ended the stream, or nil for a clean end.
//
// This is the reading half of the spec (WHATWG HTML §9.2), less the parts
// that only matter to a browser's EventSource: no Last-Event-ID (a
// reconnect here does not resume, it re-waits, because ssoosshd's Wait is
// idempotent) and no server-set retry interval (reconnectDelay is this
// client's own).
func readEvents(body io.Reader, onEvent func(sseEvent) bool) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(nil, maxEventBytes)

	var evt sseEvent
	for scanner.Scan() {
		line := scanner.Text()

		// A blank line dispatches whatever has accumulated. An empty event
		// is not dispatched: that is what a stream of nothing but
		// keep-alives looks like.
		if line == "" {
			if evt != (sseEvent{}) && !onEvent(evt) {
				return nil
			}
			evt = sseEvent{}
			continue
		}

		// A line beginning with a colon is a comment, which is how servers
		// and proxies keep an idle stream alive.
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			// A whole line with no colon names a field whose value is
			// empty. Neither field this client reads means anything empty.
			continue
		}
		// A single leading space after the colon is separator, not data.
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			evt.name = value
		case "data":
			if evt.data != "" {
				evt.data += "\n"
			}
			evt.data += value
		}
	}

	return scanner.Err()
}
