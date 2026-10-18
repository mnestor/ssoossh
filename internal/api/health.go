package api

import (
	"net/http"
	"time"
)

// HTTP/2 health checks on the ordinary-call transport. A connection that
// has read nothing for pingAfter is sent a PING, and is closed when no
// answer comes within pingTimeout. Added after a field failure: a stateful
// middlebox between a host and the server silently dropped one direction
// of a kept-alive connection, and the next lookup was written into it and
// waited out its whole timeout. The ping finds such a connection while it
// is idle, so the next call dials anew, and on a path that is only idle
// the pings themselves keep the middlebox's flow state alive.
//
// 15 s is well inside the idle timeouts of common firewalls and NATs, and
// a well-behaved peer answers a PING within one round trip, so 5 s only
// fails a path that is already unusable. Neither applies to HTTP/1.1,
// which has no PING: there the dialer's TCP keep-alive (30 s, on both
// dialers NewClient can install) and the caller's own retries are what
// recover.
const (
	pingAfter   = 15 * time.Second
	pingTimeout = 5 * time.Second
)

// healthChecks is the HTTP/2 configuration NewClient installs.
//
// It goes on every ordinary-call client, the CLI's included, rather than
// on ssoosshd-nss's alone: those calls are short and have no stream to
// keep, so a ping only ever finds a dead connection sooner, and one
// transport setup is simpler to reason about than two. It does not go on
// the events stream's transport (AwaitCertificate builds its own with
// newTransport). That stream is silent until a human decides, and a
// connection closed for a lost ping fails client.Do, which readOutcome
// treats as a definitive transport error, not as a drop to reconnect
// from; enabling pings there needs that classification changed first.
func healthChecks() *http.HTTP2Config {
	return &http.HTTP2Config{SendPingTimeout: pingAfter, PingTimeout: pingTimeout}
}

// IdleCloser is a client that pools connections. ssoosshd-nss calls it
// after an attempt that got no answer, so the retry dials a fresh
// connection rather than reusing one that may be dead. HTTPClient
// implements it.
type IdleCloser interface {
	// CloseIdleConnections closes every pooled connection not carrying a
	// request.
	CloseIdleConnections()
}

var _ IdleCloser = (*HTTPClient)(nil)

// CloseIdleConnections implements IdleCloser.
func (c *HTTPClient) CloseIdleConnections() { c.http.CloseIdleConnections() }
