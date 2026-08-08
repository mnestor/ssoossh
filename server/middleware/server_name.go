package middleware

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

// ServerNameMiddleware rejects requests that are not addressed to the
// configured server name.
type ServerNameMiddleware struct{}

// NewServerNameMiddleware creates a ServerNameMiddleware.
func NewServerNameMiddleware() *ServerNameMiddleware {
	return &ServerNameMiddleware{}
}

// Add returns a gin.HandlerFunc that responds with 421 Misdirected Request
// unless the request is addressed to serverName, comparing both the HTTP
// Host (ignoring any port) and, on TLS connections that carried SNI, the
// SNI value. Names are compared case-insensitively. An empty serverName
// disables the check.
func (m *ServerNameMiddleware) Add(serverName string) gin.HandlerFunc {
	if serverName == "" {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		if !hostMatches(c.Request.Host, serverName) {
			_ = c.Error(&MisdirectedRequestError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		// SNI is absent when clients connect by IP; only a mismatching
		// value is rejected.
		if tlsState := c.Request.TLS; tlsState != nil &&
			tlsState.ServerName != "" && !strings.EqualFold(tlsState.ServerName, serverName) {
			_ = c.Error(&MisdirectedRequestError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		c.Next()
	}
}

// hostMatches reports whether hostport (an HTTP Host header value, possibly
// carrying a port) names serverName, comparing case-insensitively.
func hostMatches(hostport, serverName string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	// A bracketed IPv6 literal without a port isn't split above; strip the
	// brackets so it compares equal to a configured bare address.
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return strings.EqualFold(host, serverName)
}
