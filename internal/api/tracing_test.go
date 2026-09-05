package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/internal/tracelog"
)

// tracingServer answers one retrieve call, which is enough to drive both
// middlewares.
func tracingServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=super-secret-value")
		_, _ = w.Write([]byte(`{"data":{"certificate":"ssh-ed25519-cert-v01@openssh.com AAAA"},"error":null}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// traceLogger returns a logger writing to buf at the deepest level, so a
// test can assert on everything the tracing would ever emit.
func traceLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: tracelog.LevelTrace}))
}

// pam_ssoossh (github.com/mnestor/ssoossh-pam) is loaded into sudo and sshd,
// where anything written to stdout or stderr corrupts the host process's own
// output. It builds its API client without a Logger and routes its own
// logging to syslog, so this package must
// emit nothing at all in that configuration — including via slog.Default(),
// whose built-in handler writes to stderr.
func TestNewClient_ShouldEmitNothingWhenNoLoggerIsConfigured(t *testing.T) {
	ts := tracingServer(t)

	var captured bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(traceLogger(&captured))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := c.RetrieveServiceCertificate(context.Background(), "enroll-code-123"); err != nil {
		t.Fatalf("RetrieveServiceCertificate() error = %v", err)
	}

	if captured.Len() != 0 {
		t.Errorf("a client with no Logger wrote to the default logger:\n%s", captured.String())
	}
}

func TestNewClient_ShouldTraceRequestsWhenALoggerIsConfigured(t *testing.T) {
	ts := tracingServer(t)

	var captured bytes.Buffer
	c, err := NewClient(Config{ServerURL: ts.URL, Logger: traceLogger(&captured)})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := c.RetrieveServiceCertificate(context.Background(), "enroll-code-123"); err != nil {
		t.Fatalf("RetrieveServiceCertificate() error = %v", err)
	}

	got := captured.String()
	if !strings.Contains(got, "http request") {
		t.Errorf("no request line in the trace:\n%s", got)
	}
	if !strings.Contains(got, "http response") {
		t.Errorf("no response line in the trace:\n%s", got)
	}
}

// The trace is meant to be pasted into bug reports, so the two things that
// would make that unsafe must never appear.
func TestNewClient_ShouldNotTraceTheEnrollmentCode(t *testing.T) {
	ts := tracingServer(t)

	var captured bytes.Buffer
	c, err := NewClient(Config{ServerURL: ts.URL, Logger: traceLogger(&captured)})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := c.RetrieveServiceCertificate(context.Background(), "enroll-code-123"); err != nil {
		t.Fatalf("RetrieveServiceCertificate() error = %v", err)
	}

	if strings.Contains(captured.String(), "enroll-code-123") {
		t.Errorf("the enrollment code reached the trace:\n%s", captured.String())
	}
}

func TestNewClient_ShouldNotTraceASetCookieHeader(t *testing.T) {
	ts := tracingServer(t)

	var captured bytes.Buffer
	c, err := NewClient(Config{ServerURL: ts.URL, Logger: traceLogger(&captured)})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := c.RetrieveServiceCertificate(context.Background(), "enroll-code-123"); err != nil {
		t.Fatalf("RetrieveServiceCertificate() error = %v", err)
	}

	if strings.Contains(captured.String(), "super-secret-value") {
		t.Errorf("a session cookie reached the trace:\n%s", captured.String())
	}
}

// Levels have to mean something: at -vv the reader wants to know a request
// happened, not to receive its body.
func TestNewClient_ShouldNotTraceBodiesBelowTheDeepestLevel(t *testing.T) {
	ts := tracingServer(t)

	var captured bytes.Buffer
	debugOnly := slog.New(slog.NewTextHandler(&captured, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, err := NewClient(Config{ServerURL: ts.URL, Logger: debugOnly})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := c.RetrieveServiceCertificate(context.Background(), "enroll-code-123"); err != nil {
		t.Fatalf("RetrieveServiceCertificate() error = %v", err)
	}

	got := captured.String()
	if !strings.Contains(got, "http request") {
		t.Errorf("expected the request line at debug level:\n%s", got)
	}
	if strings.Contains(got, "http response body") {
		t.Errorf("bodies must not appear below the deepest level:\n%s", got)
	}
}
