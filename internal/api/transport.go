package api

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// TransportError is a call that never got a usable HTTP response, worded
// for the person running the client. Err keeps the underlying error for
// errors.Is and errors.As.
type TransportError struct {
	// Server is the server address the call went to.
	Server string
	// Reason says what happened, in a few words.
	Reason string
	// Hint is the next step to try.
	Hint string
	// Err is the error the transport returned.
	Err error
}

// Error implements error.
func (e *TransportError) Error() string {
	return fmt.Sprintf("cannot reach %s: %s; %s", e.Server, e.Reason, e.Hint)
}

// Unwrap returns the transport's own error.
func (e *TransportError) Unwrap() error { return e.Err }

// connUse is how far a failed call got with a connection, which tells a
// path that refuses or cannot be reached from one that accepted the request
// and then went silent.
type connUse int32

const (
	// connUnknown is a call that did not record it.
	connUnknown connUse = iota
	// connNone is a call that never got a connection: the dial or the TLS
	// handshake did not finish.
	connNone
	// connFresh is a call sent on a connection dialled for it.
	connFresh
	// connReused is a call sent on a kept-alive connection from the pool.
	connReused
)

// connTracker records, through httptrace, which connection a call got.
// Atomic because the transport may call GotConn on a goroutine of its own.
type connTracker struct {
	got atomic.Int32
}

// trace returns the hooks to install on the request.
func (t *connTracker) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if info.Reused {
			t.got.Store(int32(connReused))
		} else {
			t.got.Store(int32(connFresh))
		}
	}}
}

// use reports what the call got, connNone when GotConn never fired.
func (t *connTracker) use() connUse {
	if got := connUse(t.got.Load()); got != connUnknown {
		return got
	}
	return connNone
}

// describeTransport turns a transport failure into a TransportError when it
// recognises the cause, and returns it unchanged otherwise. Go's own wording
// ("dial tcp 127.0.0.1:9: connect: connection refused", "tls: first record
// does not look like a TLS handshake") says what failed but not what to do;
// the raw error stays reachable through Unwrap and in -vv tracing. timeout
// is the bound that applied, conn how far the call got.
func describeTransport(server string, timeout time.Duration, conn connUse, err error) error {
	reason, hint := classifyTransportError(err, timeout, conn)
	if reason == "" {
		return err
	}
	return &TransportError{Server: server, Reason: reason, Hint: hint, Err: err}
}

// classifyTransportError names the cause of a transport failure and the
// next step, or returns two empty strings for a cause it does not know.
func classifyTransportError(err error, timeout time.Duration, conn connUse) (reason, hint string) {
	var (
		dnsErr      *net.DNSError
		recordErr   tls.RecordHeaderError
		unknownAuth x509.UnknownAuthorityError
		hostnameErr x509.HostnameError
		invalidCert x509.CertificateInvalidError
		netErr      net.Error
		opErr       *net.OpError
	)
	switch {
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("the host name %s does not resolve", dnsErr.Name),
			"check the server address and your network"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused",
			"check the server address and that ssoosshd is running"
	case errors.As(err, &recordErr):
		return "the server did not answer with TLS",
			"if it serves plain HTTP, give the address with http://"
	case errors.As(err, &unknownAuth):
		return "its TLS certificate is signed by an authority this machine does not trust",
			"install the issuing CA on this machine, or for a development server set insecure_skip_verify"
	case errors.As(err, &hostnameErr):
		return fmt.Sprintf("its TLS certificate is not valid for %s", hostnameErr.Host),
			"check the server address against the name on the certificate"
	case errors.As(err, &invalidCert):
		return "its TLS certificate is not valid (" + strings.TrimPrefix(invalidCert.Error(), "x509: ") + ")",
			"the server's certificate needs fixing"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		// Ahead of the timeout case: a dial that timed out is a connect
		// failure, not a server that took the request and never answered.
		return fmt.Sprintf("could not connect (%v)", opErr.Err),
			"check the server address and your network"
	case errors.As(err, &netErr) && netErr.Timeout():
		return timeoutReason(timeout, conn)
	}
	return "", ""
}

// timeoutReason words a timeout by how far the call got, so a silent
// reused connection (something on the path dropped it) does not read like
// an unreachable server.
func timeoutReason(timeout time.Duration, conn connUse) (reason, hint string) {
	switch {
	case timeout <= 0:
		return "the connection timed out", "check the server address and your network"
	case conn == connNone:
		return fmt.Sprintf("could not connect within %s", timeout),
			"check the server address and your network"
	case conn == connReused:
		return fmt.Sprintf("no response within %s on a reused connection", timeout),
			"something between this host and the server may be dropping idle connections; check firewall and NAT state timeouts"
	default:
		return fmt.Sprintf("no response within %s", timeout),
			"check that the server address is right and ssoosshd is running, or raise request_timeout"
	}
}

// undecodableError is a 2xx response whose body is not the JSON the call
// expects. Usually the address reaches something other than ssoosshd: a
// captive portal, a proxy's sign-in page, or another web server.
type undecodableError struct {
	contentType string
	err         error
}

// Error implements error.
func (e *undecodableError) Error() string {
	if strings.Contains(e.contentType, "json") {
		return fmt.Sprintf("the server sent malformed JSON (%v); retry, and report it if it persists", e.err)
	}
	contentType := e.contentType
	if contentType == "" {
		contentType = "none"
	}
	return fmt.Sprintf("the server's reply is not the JSON ssoosshd sends (Content-Type: %s); "+
		"check that the server address points at ssoosshd", contentType)
}

// Unwrap returns the JSON decoder's error.
func (e *undecodableError) Unwrap() error { return e.err }
