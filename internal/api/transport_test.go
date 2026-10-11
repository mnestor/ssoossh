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

	c := &HTTPClient{serverURL: "https://ssh.example.test"}
	original := errors.New("something else")

	if got := c.describeTransportError(original); got != original {
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
			name:       "should leave an unknown error alone",
			err:        errors.New("something else"),
			wantReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if reason, _ := classifyTransportError(tt.err, time.Second); reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

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
