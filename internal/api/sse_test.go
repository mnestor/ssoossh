package api

// Test methodology: unit tests for waitForOutcome and the stream reader
// under it, against a real httptest.NewServer. Broader create-then-wait
// flows (including the reconnect case) are covered in certrequest_test.go;
// these focus on the SSE-reading unit itself, including the wire-format
// cases readEvents has to get right that a live server never produces.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForOutcome_ShouldDecodeApprovedEvent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
	if result.Certificate != "ssh-ed25519-cert-v01@openssh.com AAAA..." {
		t.Errorf("got certificate %q, want the signed certificate", result.Certificate)
	}
}

func TestWaitForOutcome_ShouldDecodeDeniedEventWithNoCertificate(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "denied", map[string]string{})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusDenied {
		t.Errorf("got status %q, want %q", result.Status, StatusDenied)
	}
	if result.Certificate != "" {
		t.Errorf("got certificate %q, want empty", result.Certificate)
	}
}

// TestWaitForOutcome_ShouldTreatEnrolledAsTerminal is a regression test:
// "enrolled" was emitted by the server but missing from this client's
// terminal-status list, so a service enrollment blocked until the stream
// closed instead of resolving. Any status the server can send must be
// registered here — see apitypes.TerminalStatuses.
func TestWaitForOutcome_ShouldTreatEnrolledAsTerminal(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "enrolled", map[string]string{"code": "token-abc"})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusEnrolled {
		t.Errorf("got status %q, want %q", result.Status, StatusEnrolled)
	}
	if result.Code != "token-abc" {
		t.Errorf("got code %q, want %q", result.Code, "token-abc")
	}
}

// TestWaitForOutcome_ShouldCarryEveryFieldOfTheEnrolledPayload is a
// regression test: the listener used to copy Certificate and Code out of
// the envelope by hand, so service_account and expires_at decoded correctly
// and were then dropped on the floor. It is caught here rather than only
// end to end because a field-by-field copy fails silently.
func TestWaitForOutcome_ShouldCarryEveryFieldOfTheEnrolledPayload(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "enrolled", map[string]string{
			"code":            "token-abc",
			"service_account": "svc-deploy",
			"expires_at":      "2026-09-23T14:05:00Z",
		})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ServiceAccount != "svc-deploy" {
		t.Errorf("got service account %q, want %q", result.ServiceAccount, "svc-deploy")
	}
	if result.ExpiresAt == nil {
		t.Fatal("got no code expiry, want the one on the event")
	}
	want := time.Date(2026, 9, 23, 14, 5, 0, 0, time.UTC)
	if !result.ExpiresAt.Equal(want) {
		t.Errorf("got code expiry %v, want %v", result.ExpiresAt, want)
	}
}

// An outcome with no enrollment behind it must leave the two enrollment
// fields unset, not carry a zero time the caller has to know to disbelieve.
func TestWaitForOutcome_ShouldLeaveEnrollmentFieldsUnsetWhenAbsent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ServiceAccount != "" {
		t.Errorf("got service account %q, want empty", result.ServiceAccount)
	}
	if result.ExpiresAt != nil {
		t.Errorf("got code expiry %v, want nil", result.ExpiresAt)
	}
}

func TestWaitForOutcome_ShouldTreatFailedAsTerminal(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "failed", map[string]string{})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFailed {
		t.Errorf("got status %q, want %q", result.Status, StatusFailed)
	}
	if result.Certificate != "" {
		t.Errorf("expected no certificate on a failed outcome, got %q", result.Certificate)
	}
}

// TestWaitForOutcome_ShouldSurfaceGoneWithoutRetrying covers the ephemeral
// certificate case: the server answers 410 when a certificate is no longer
// available. That must surface as a *ResponseError the caller can act on,
// and must not send the client into a reconnect loop — a refused connect is
// definitive, unlike a stream that was established and then dropped.
func TestWaitForOutcome_ShouldSurfaceGoneWithoutRetrying(t *testing.T) {
	t.Parallel()

	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"data":null,"error":"certificate is no longer available"}`))
	}))
	t.Cleanup(ts.Close)

	_, err := waitForOutcome(context.Background(), nil, ts.URL)

	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusGone {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusGone)
	}
	if attempts != 1 {
		t.Errorf("expected exactly one attempt (410 must not be retried), got %d", attempts)
	}
}

func TestWaitForOutcome_ShouldErrorOnNon2xxConnect(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":"certificate request \"abc\" not found"}`))
	}))
	t.Cleanup(ts.Close)

	_, err := waitForOutcome(context.Background(), nil, ts.URL)
	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusNotFound {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusNotFound)
	}
	if respErr.Message != `certificate request "abc" not found` {
		t.Errorf("got message %q, want the decoded error body", respErr.Message)
	}
}

func TestWaitForOutcome_ShouldRespectContextCancellation(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForOutcome(ctx, nil, ts.URL)
	if err == nil {
		t.Fatal("expected an error from an already-canceled context, got nil")
	}
}

// A connect that never reaches a server at all is definitive: without this
// the reconnect loop would spin on an unresolvable host until the caller's
// timeout, hiding the real reason from an operator reading the log.
func TestWaitForOutcome_ShouldNotRetryAConnectThatNeverReachedAServer(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close() // nothing is listening on url now

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := waitForOutcome(ctx, nil, url)
	if err == nil {
		t.Fatal("expected an error connecting to a closed port, got nil")
	}
	if ctx.Err() != nil {
		t.Error("expected the failure to be reported immediately, not retried until the deadline")
	}
	if !strings.Contains(err.Error(), "failed to connect") {
		t.Errorf("got %q, want an error naming the failed connect", err)
	}
}

// readEvents implements the reading half of the SSE wire format, which a
// live ssoosshd only ever exercises one way. These are the cases the format
// allows that it does not send.
func TestReadEvents_ShouldParseTheWireFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []sseEvent
	}{
		{
			name: "should dispatch a named event with data",
			in:   "event:approved\ndata:{\"a\":1}\n\n",
			want: []sseEvent{{name: "approved", data: `{"a":1}`}},
		},
		{
			name: "should strip one space after the colon but not a second",
			in:   "event: approved\ndata:  padded\n\n",
			want: []sseEvent{{name: "approved", data: " padded"}},
		},
		{
			name: "should join multiple data lines with a newline",
			in:   "event:approved\ndata:one\ndata:two\n\n",
			want: []sseEvent{{name: "approved", data: "one\ntwo"}},
		},
		{
			name: "should ignore comment lines used as keep-alives",
			in:   ":keep-alive\n\n:another\nevent:approved\ndata:x\n\n",
			want: []sseEvent{{name: "approved", data: "x"}},
		},
		{
			name: "should ignore fields this client does not read",
			in:   "id:42\nretry:5000\nevent:approved\ndata:x\n\n",
			want: []sseEvent{{name: "approved", data: "x"}},
		},
		{
			name: "should ignore a line with no colon at all",
			in:   "garbage\nevent:approved\ndata:x\n\n",
			want: []sseEvent{{name: "approved", data: "x"}},
		},
		{
			name: "should dispatch each event separately",
			in:   "event:pending\ndata:a\n\nevent:approved\ndata:b\n\n",
			want: []sseEvent{{name: "pending", data: "a"}, {name: "approved", data: "b"}},
		},
		{
			name: "should accept CRLF line endings",
			in:   "event:approved\r\ndata:x\r\n\r\n",
			want: []sseEvent{{name: "approved", data: "x"}},
		},
		{
			name: "should dispatch an event that carries no data",
			in:   "event:denied\n\n",
			want: []sseEvent{{name: "denied"}},
		},
		{
			name: "should dispatch nothing for a stream of only keep-alives",
			in:   ":ping\n\n:ping\n\n",
			want: nil,
		},
		{
			name: "should not dispatch an event left unterminated by a blank line",
			in:   "event:approved\ndata:x\n",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []sseEvent
			err := readEvents(strings.NewReader(tt.in), func(e sseEvent) bool {
				got = append(got, e)
				return true
			})
			if err != nil {
				t.Fatalf("readEvents() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("readEvents() dispatched %d events (%+v), want %d (%+v)", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Returning false is how readOutcome stops on the first terminal event.
// Reading on would block until the server closed the stream, which for a
// server that keeps it open is forever.
func TestReadEvents_ShouldStopWhenTheCallbackReturnsFalse(t *testing.T) {
	t.Parallel()

	var got []sseEvent
	err := readEvents(strings.NewReader("event:a\ndata:1\n\nevent:b\ndata:2\n\n"), func(e sseEvent) bool {
		got = append(got, e)
		return false
	})
	if err != nil {
		t.Fatalf("readEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Errorf("readEvents() dispatched %d events, want it to stop after the first", len(got))
	}
}

// A line longer than the cap ends the read rather than growing sudo's heap
// without bound. waitForOutcome treats that as a dropped stream, so the
// caller keeps waiting rather than failing the login on it.
func TestReadEvents_ShouldStopOnALineBeyondTheCap(t *testing.T) {
	t.Parallel()

	oversized := "data:" + strings.Repeat("x", maxEventBytes+1) + "\n\n"

	var dispatched int
	err := readEvents(strings.NewReader(oversized), func(sseEvent) bool {
		dispatched++
		return true
	})
	if err == nil {
		t.Fatal("expected an error for a line beyond the cap, got nil")
	}
	if dispatched != 0 {
		t.Errorf("dispatched %d events from an oversized line, want none", dispatched)
	}
}

// writeRawSSE writes an events response verbatim, for the wire shapes
// writeSSEEvent's envelope cannot produce.
func writeRawSSE(w http.ResponseWriter, raw string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(raw))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ssoosshd sends one terminal event today, but the stream is specified to
// allow informational ones before it. Reading past them is what stops a
// future progress event from resolving the wait with nothing in it.
func TestWaitForOutcome_ShouldKeepReadingPastANonTerminalEvent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawSSE(w, "event:pending\ndata:{\"data\":{},\"error\":null}\n\n")
		writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
}

func TestWaitForOutcome_ShouldAcceptATerminalEventWithNoData(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawSSE(w, "event:denied\n\n")
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusDenied {
		t.Errorf("got status %q, want %q", result.Status, StatusDenied)
	}
}

func TestWaitForOutcome_ShouldErrorOnATerminalEventThatIsNotJSON(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawSSE(w, "event:approved\ndata:not json at all\n\n")
	}))
	t.Cleanup(ts.Close)

	_, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err == nil {
		t.Fatal("expected an error for an undecodable event payload, got nil")
	}
	if !strings.Contains(err.Error(), "failed to decode certificate request event") {
		t.Errorf("got %q, want an error naming the undecodable event", err)
	}
}

// The envelope has an error half, and a terminal event that fills it is
// reporting a failure rather than an outcome — resolving it as an approval
// with an empty certificate would be worse than any error.
func TestWaitForOutcome_ShouldSurfaceAnErrorCarriedInTheEventEnvelope(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawSSE(w, "event:failed\ndata:{\"data\":null,\"error\":\"signing key unavailable\"}\n\n")
	}))
	t.Cleanup(ts.Close)

	_, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err == nil {
		t.Fatal("expected an error for an event carrying one, got nil")
	}
	if !strings.Contains(err.Error(), "signing key unavailable") {
		t.Errorf("got %q, want the error the event carried", err)
	}
}

// A stream that is established and then breaks is not an answer: the wait
// reconnects and keeps going, and only the caller's context ends it. The
// error it ends with names the drop, because "context deadline exceeded"
// alone would send an operator looking in the wrong place.
func TestWaitForOutcome_ShouldReconnectAfterABrokenStreamUntilTheContextEnds(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writeRawSSE(w, "event:app")
		panic(http.ErrAbortHandler) // break the connection mid-event
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	_, err := waitForOutcome(ctx, nil, ts.URL)
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("got %v, want an error reporting the stream ended without an outcome", err)
	}
	if got := attempts.Load(); got < 2 {
		t.Errorf("made %d attempts, want the broken stream to have been retried", got)
	}
}

// A server that closes the stream cleanly with nothing on it drops out the
// same way, just without a read error to report.
func TestWaitForOutcome_ShouldReportAnEmptyStreamWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawSSE(w, "")
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := waitForOutcome(ctx, nil, ts.URL)
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("got %v, want an error reporting the stream ended without an outcome", err)
	}
}

// The events URL is built by concatenating the server URL with a path
// ssoosshd returned, so an unusable one has to fail as an error rather than
// send the reconnect loop spinning on a request that can never be built.
func TestWaitForOutcome_ShouldErrorOnAnEventsURLThatCannotBeARequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := waitForOutcome(ctx, nil, "http://exa\x7fmple.com/api/certs/requests/x/events")
	if err == nil {
		t.Fatal("expected an error for an events URL that cannot be parsed, got nil")
	}
	if ctx.Err() != nil {
		t.Error("expected the failure to be reported immediately, not retried until the deadline")
	}
}

// A 503 while ssoosshd restarts, or a 429 under load, says to come back —
// the certificate request is still pending behind it. Losing this would
// turn a rolling restart into a failed login for everyone mid-wait.
func TestWaitForOutcome_ShouldReconnectAfterARetryableStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
	}{
		{name: "should come back after a rate limit", status: http.StatusTooManyRequests},
		{name: "should come back after a server fault", status: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int64
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(`{"data":null,"error":"try again"}`))
					return
				}
				writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
			}))
			t.Cleanup(ts.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			result, err := waitForOutcome(ctx, nil, ts.URL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != StatusApproved {
				t.Errorf("got status %q, want %q", result.Status, StatusApproved)
			}
			if got := attempts.Load(); got < 2 {
				t.Errorf("made %d attempts, want the %d to have been retried", got, tt.status)
			}
		})
	}
}

// The retryable statuses still have to reach the caller as a *ResponseError
// when the wait runs out, so a login that failed because ssoosshd was down
// says so rather than only "deadline exceeded".
func TestWaitForOutcome_ShouldStillReportARetryableStatusWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"data":null,"error":"ssoosshd is restarting"}`))
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	_, err := waitForOutcome(ctx, nil, ts.URL)
	respErr := &ResponseError{}
	if !errors.As(err, &respErr) {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusServiceUnavailable)
	}
	if respErr.Message != "ssoosshd is restarting" {
		t.Errorf("got message %q, want the decoded error body", respErr.Message)
	}
}
