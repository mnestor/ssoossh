package varlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// Client is the calling side of one varlink connection. Varlink answers
// the calls on a connection in order, so a Client is not safe for
// concurrent use; open one per goroutine.
//
// An error reply comes back as *Error and leaves the connection usable.
// Any other error (a transport failure, a framing error, a context that
// ended mid-call) leaves it in an unknown state, and the caller closes it.
type Client struct {
	conn net.Conn
	enc  *Encoder
	dec  *Decoder
}

// Dial connects to the varlink service listening on the AF_UNIX socket at
// path. ctx bounds the connect only; each call takes its own.
func Dial(ctx context.Context, path string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial varlink socket %s: %w", path, err)
	}
	return NewClient(conn), nil
}

// NewClient wraps a connection that is already open, which is how tests
// and callers with a socket of their own use this package.
func NewClient(conn net.Conn) *Client {
	return &Client{conn: conn, enc: NewEncoder(conn), dec: NewDecoder(conn)}
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Call calls method with params and waits for its single reply, decoding
// the reply's parameters into out. params may be nil, a json.RawMessage or
// anything that marshals to a JSON object; out may be nil to discard the
// reply.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	return c.withContext(ctx, func() error {
		if err := c.send(method, params, false, false); err != nil {
			return err
		}
		reply, err := c.dec.ReadReply()
		if err != nil {
			return err
		}
		if reply.Error != "" {
			return &Error{Name: reply.Error, Parameters: reply.Parameters}
		}
		if reply.Continues {
			return fmt.Errorf("%w: the reply to %s continues although more was not asked", ErrInvalidMessage, method)
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(reply.Parameters, out); err != nil {
			return fmt.Errorf("decode %s reply: %w", method, err)
		}
		return nil
	})
}

// CallOneway calls method with params and asks for no reply. It returns
// once the call is written; whether the service acted on it is not
// knowable.
func (c *Client) CallOneway(ctx context.Context, method string, params any) error {
	return c.withContext(ctx, func() error {
		return c.send(method, params, false, true)
	})
}

// CallMore calls method with more set and hands each reply's parameters
// to each, in order, until a reply that does not continue. An error reply
// ends the stream and is returned as *Error. An error from each ends it
// too, with the rest of the stream unread, so the caller closes the
// connection after one.
func (c *Client) CallMore(ctx context.Context, method string, params any, each func(json.RawMessage) error) error {
	return c.withContext(ctx, func() error {
		if err := c.send(method, params, true, false); err != nil {
			return err
		}
		for {
			reply, err := c.dec.ReadReply()
			if err != nil {
				return err
			}
			if reply.Error != "" {
				return &Error{Name: reply.Error, Parameters: reply.Parameters}
			}
			if err := each(reply.Parameters); err != nil {
				return err
			}
			if !reply.Continues {
				return nil
			}
		}
	})
}

// send encodes params and writes one call.
func (c *Client) send(method string, params any, more, oneway bool) error {
	raw, err := encodeParameters(params)
	if err != nil {
		return err
	}
	return c.enc.WriteCall(Call{Method: method, Parameters: raw, More: more, Oneway: oneway})
}

// withContext runs one call's reads and writes under ctx: its deadline
// becomes the connection's, and its cancellation interrupts whatever read
// or write is in progress. Every call sets the deadline afresh, so one
// call's context never outlives it.
func (c *Client) withContext(ctx context.Context, call func() error) error {
	deadline, _ := ctx.Deadline() // the zero time, meaning none, when unset
	if err := c.conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set varlink connection deadline: %w", err)
	}

	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(fired)
		// A deadline long past makes a blocked read or write return at
		// once, which is how a cancelled context reaches a connection that
		// knows nothing about contexts. Closing does the same when the
		// deadline cannot be set, and a cancelled call leaves the
		// connection unusable anyway.
		if err := c.conn.SetDeadline(time.Unix(1, 0)); err != nil {
			_ = c.conn.Close()
		}
	})

	err := call()
	if !stop() {
		// The cancellation is running or has run. Wait for it, so its
		// deadline cannot land on the next call's connection.
		<-fired
	}
	return contextError(ctx, deadline, err)
}

// contextError makes an error that a call's context caused say so, so a
// caller can test it with errors.Is(err, context.DeadlineExceeded).
func contextError(ctx context.Context, deadline time.Time, err error) error {
	if err == nil || !errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	// The connection's deadline and ctx's timer fire independently, so the
	// read can fail a moment before ctx.Err() is set. A deadline that has
	// passed is the context's either way.
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("%w: %w", ctx.Err(), err)
	case !deadline.IsZero() && !time.Now().Before(deadline):
		return fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	default:
		return err
	}
}
