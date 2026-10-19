package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// silentServer accepts every request and never answers until the test ends,
// the failure the request timeout exists for.
func silentServer(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(func() {
		close(done)
		ts.Close()
	})
	return ts
}

func TestDoJSON_ShouldReturnATransportErrorWhenTheServerNeverAnswers(t *testing.T) {
	t.Parallel()

	ts := silentServer(t)
	c, err := NewClient(Config{ServerURL: ts.URL, RequestTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected a *TransportError, got %v", err)
	}
}

func TestDoJSON_ShouldNameTheTimeoutWhenTheServerNeverAnswers(t *testing.T) {
	t.Parallel()

	ts := silentServer(t)
	c, err := NewClient(Config{ServerURL: ts.URL, RequestTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())

	if err == nil || !strings.Contains(err.Error(), "no response within 50ms") {
		t.Errorf("got %v, want it to name the 50ms timeout", err)
	}
}

func TestNewClient_ShouldDefaultTheRequestTimeout(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{ServerURL: "https://ssh.example.test"})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}
	if c.http.Timeout != DefaultRequestTimeout {
		t.Errorf("Timeout = %s, want %s", c.http.Timeout, DefaultRequestTimeout)
	}
}

func TestDescribeTransportError_ShouldPassThroughAnUnrecognisedError(t *testing.T) {
	t.Parallel()

	original := errors.New("something else")

	var transportErr *TransportError
	if got := describeTransport("https://ssh.example.test", time.Second, connFresh, original); errors.As(got, &transportErr) {
		t.Errorf("got %v, want the original error unchanged", got)
	}
}

// TestClassifyTransportError pins the reason and next step for each
// transport failure the client recognises.
func TestClassifyTransportError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		timeout    time.Duration
		conn       connUse
		wantReason string
	}{
		{
			name:       "should say a host name does not resolve",
			err:        &net.OpError{Op: "dial", Err: &net.DNSError{Name: "nonexistent.invalid", Err: "no such host"}},
			wantReason: "the host name nonexistent.invalid does not resolve",
		},
		{
			name:       "should say the connection was refused",
			err:        &net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}},
			wantReason: "connection refused",
		},
		{
			name:       "should say the server did not speak TLS",
			err:        tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"},
			wantReason: "the server did not answer with TLS",
		},
		{
			name:       "should say the certificate authority is not trusted",
			err:        &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
			wantReason: "its TLS certificate is signed by an authority this machine does not trust",
		},
		{
			name:       "should name the host a certificate does not cover",
			err:        x509.HostnameError{Certificate: &x509.Certificate{}, Host: "ssh.example.test"},
			wantReason: "its TLS certificate is not valid for ssh.example.test",
		},
		{
			name:       "should describe a dial failure it has no specific wording for",
			err:        &net.OpError{Op: "dial", Err: errors.New("network is unreachable")},
			wantReason: "could not connect (network is unreachable)",
		},
		{
			name:       "should call a dial that timed out a connect failure when the dialer gave up",
			err:        &net.OpError{Op: "dial", Err: timeoutErr{}},
			conn:       connNone,
			wantReason: "could not connect (i/o timeout)",
		},
		{
			name:       "should say it could not connect when the deadline passed before a connection",
			err:        context.DeadlineExceeded,
			conn:       connNone,
			wantReason: "could not connect within 1s",
		},
		{
			name:       "should name the reused connection when one went silent",
			err:        context.DeadlineExceeded,
			conn:       connReused,
			wantReason: "no response within 1s on a reused connection",
		},
		{
			name:       "should name the timeout when a fresh connection went silent",
			err:        context.DeadlineExceeded,
			conn:       connFresh,
			wantReason: "no response within 1s",
		},
		{
			name:       "should name the timeout when the call did not record its connection",
			err:        context.DeadlineExceeded,
			conn:       connUnknown,
			wantReason: "no response within 1s",
		},
		{
			name:       "should say only that it timed out when no bound is known",
			err:        context.DeadlineExceeded,
			timeout:    -1,
			conn:       connReused,
			wantReason: "the connection timed out",
		},
		{
			name:       "should leave an unknown error alone",
			err:        errors.New("something else"),
			wantReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			timeout := tt.timeout
			if timeout == 0 {
				timeout = time.Second
			}
			if reason, _ := classifyTransportError(tt.err, timeout, tt.conn); reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// timeoutErr is a net.Error that timed out, as a dialer's i/o timeout is.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestUndecodableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{
			name:        "should suspect the server address for an HTML reply",
			contentType: "text/html",
			want:        "the server's reply is not the JSON ssoosshd sends (Content-Type: text/html); check that the server address points at ssoosshd",
		},
		{
			name:        "should say none when there is no Content-Type",
			contentType: "",
			want:        "the server's reply is not the JSON ssoosshd sends (Content-Type: none); check that the server address points at ssoosshd",
		},
		{
			name:        "should call JSON that does not parse malformed",
			contentType: "application/json",
			want:        "the server sent malformed JSON (unexpected end of JSON input); retry, and report it if it persists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := &undecodableError{contentType: tt.contentType, err: errors.New("unexpected end of JSON input")}
			if err.Error() != tt.want {
				t.Errorf("got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

// TestNewClient_UserAgent covers the default header and a caller's own.
func TestNewClient_UserAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		userAgent  string
		wantPrefix string
		wantExact  bool
	}{
		{"should send the ssoossh client's user agent when none is configured", "", "ssoossh/", false},
		{"should send the configured user agent verbatim when one is set", "ssoossh-nss/1.2.3", "ssoossh-nss/1.2.3", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := make(chan string, 1)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got <- r.Header.Get("User-Agent")
				http.Error(w, "not here", http.StatusNotFound)
			}))
			t.Cleanup(ts.Close)

			c, err := NewClient(Config{ServerURL: ts.URL, UserAgent: tt.userAgent})
			if err != nil {
				t.Fatalf("unexpected error building client: %v", err)
			}
			_, _ = c.GetCA(context.Background())

			ua := <-got
			if tt.wantExact && ua != tt.wantPrefix {
				t.Errorf("User-Agent = %q, want %q", ua, tt.wantPrefix)
			}
			if !tt.wantExact && !strings.HasPrefix(ua, tt.wantPrefix) {
				t.Errorf("User-Agent = %q, want the prefix %q", ua, tt.wantPrefix)
			}
		})
	}
}

// TestNewClient_ShouldStillConnectWhenADialTimeoutIsSet proves the
// replacement dialer is installed and dials, and leaves the request
// timeout alone.
func TestNewClient_ShouldStillConnectWhenADialTimeoutIsSet(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not here", http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL, DialTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}
	transport, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("DialContext() error = %v, want a connection", err)
	}
	_ = conn.Close()
	if c.http.Timeout != DefaultRequestTimeout {
		t.Errorf("Timeout = %s, want the default %s untouched", c.http.Timeout, DefaultRequestTimeout)
	}
}
