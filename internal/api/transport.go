package api

import (
	"errors"
	"fmt"
	"net"
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

// describeTransportError turns a transport failure into a TransportError
// when it recognises the cause, and returns it unchanged otherwise.
func (c *HTTPClient) describeTransportError(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &TransportError{
			Server: c.serverURL,
			Reason: fmt.Sprintf("no response within %s", c.timeout),
			Hint:   "check that the server address is right and ssoosshd is running, or raise request_timeout",
			Err:    err,
		}
	}
	return err
}
