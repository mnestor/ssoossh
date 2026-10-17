// Package varlink is a minimal varlink codec with a client half and a
// server half, enough to speak systemd's io.systemd.UserDatabase interface
// from ssoosshd-nss and to send it one-way calls from the ssoossh client.
//
// The wire format (https://varlink.org/Method-Call): every message is one
// JSON object followed by a NUL byte. A call is
// {"method", "parameters", "more"?, "oneway"?}; a reply is {"parameters"}
// or {"error", "parameters"}, and a call made with "more" gets a stream of
// replies in which every one but the last carries "continues": true.
// Unknown top-level fields are ignored. A message longer than
// MaxMessageSize is a framing error, after which the connection cannot be
// trusted and must be closed.
//
// Interface descriptions, introspection and the "upgrade" flag are not
// implemented: systemd's own userdb services omit introspection too.
//
// Nothing here is Linux-specific. The client dials an AF_UNIX path, which
// Go supports on macOS and on Windows 10 and later as well.
package varlink

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MaxMessageSize bounds one message's JSON, not counting the terminating
// NUL. Generous for a user or group record, and small enough that a peer
// cannot make the other side buffer without limit.
const MaxMessageSize = 64 << 10

// Framing and protocol errors. Every one of them means the peer is not
// speaking varlink as this package understands it; the caller closes the
// connection rather than trying to resynchronise.
var (
	// ErrMessageTooLarge is a message over MaxMessageSize, read or written.
	ErrMessageTooLarge = errors.New("varlink: message exceeds 64 KiB")
	// ErrTruncated is a stream that ended partway through a message, with
	// no terminating NUL.
	ErrTruncated = errors.New("varlink: stream ended inside a message")
	// ErrInvalidMessage is a complete message that is not a valid call or
	// reply: not JSON, not an object, a field of the wrong type, a call
	// with no method, or a reply that continues when no more was asked.
	ErrInvalidMessage = errors.New("varlink: invalid message")
)

// Errors of the org.varlink.service interface every varlink service
// implements, as systemd 257's varlinkctl introspection lists them.
const (
	// ErrorInterfaceNotFound answers a call on an unknown interface.
	ErrorInterfaceNotFound = "org.varlink.service.InterfaceNotFound"
	// ErrorMethodNotFound answers a call to an unknown method.
	ErrorMethodNotFound = "org.varlink.service.MethodNotFound"
	// ErrorMethodNotImplemented answers a call to a known method this
	// service does not implement.
	ErrorMethodNotImplemented = "org.varlink.service.MethodNotImplemented"
	// ErrorInvalidParameter answers a call with a parameter this service
	// rejects; its parameters name the offending one.
	ErrorInvalidParameter = "org.varlink.service.InvalidParameter"
	// ErrorPermissionDenied answers a call the peer may not make.
	ErrorPermissionDenied = "org.varlink.service.PermissionDenied"
	// ErrorExpectedMore answers a call made without more to a method that
	// only streams.
	ErrorExpectedMore = "org.varlink.service.ExpectedMore"
)

// Call is one method call.
type Call struct {
	// Method is the fully qualified method name,
	// "io.systemd.UserDatabase.GetUserRecord" say. Required.
	Method string `json:"method"`
	// Parameters is the call's parameter object. Always an object after
	// decoding: an absent or null value decodes as {}. Empty encodes as {}.
	Parameters json.RawMessage `json:"parameters"`
	// More asks for a stream of replies rather than one.
	More bool `json:"more,omitempty"`
	// Oneway asks for no reply at all.
	Oneway bool `json:"oneway,omitempty"`
}

// Reply is one reply to a call, a success or an error.
type Reply struct {
	// Parameters is the reply's parameter object, normalised like
	// Call.Parameters.
	Parameters json.RawMessage `json:"parameters"`
	// Continues is set on every reply of a more stream but the last.
	Continues bool `json:"continues,omitempty"`
	// Error is the fully qualified error name; empty on success.
	Error string `json:"error,omitempty"`
}

// Error is an error reply returned by a client call, so a caller can
// branch on Name with errors.As.
type Error struct {
	// Name is the fully qualified error name.
	Name string
	// Parameters is the error reply's parameter object.
	Parameters json.RawMessage
}

// Error implements error.
func (e *Error) Error() string {
	return "varlink error " + e.Name
}

// emptyObject is what absent or null parameters become.
const emptyObject = "{}"

// normalizeParameters turns absent or null parameters into {} and refuses
// anything that is not a JSON object. Used on both decode and encode, so
// whatever reaches either side of a connection is always an object a
// caller can unmarshal into a struct.
func normalizeParameters(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := trimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(emptyObject), nil
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: parameters are not a JSON object", ErrInvalidMessage)
	}
	return raw, nil
}

// encodeParameters marshals a caller's parameters into an object. Nil
// means none, and a json.RawMessage is taken as already encoded.
func encodeParameters(params any) (json.RawMessage, error) {
	var raw json.RawMessage
	switch p := params.(type) {
	case nil:
	case json.RawMessage:
		raw = p
	default:
		encoded, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("encode varlink parameters: %w", err)
		}
		raw = encoded
	}
	return normalizeParameters(raw)
}

// trimSpace drops the JSON whitespace around b without allocating.
func trimSpace(b []byte) []byte {
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
	for len(b) > 0 && isSpace(b[0]) {
		b = b[1:]
	}
	for len(b) > 0 && isSpace(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return b
}
