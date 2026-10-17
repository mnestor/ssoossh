package varlink

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Encoder writes NUL-terminated varlink messages to a writer.
type Encoder struct {
	w io.Writer
}

// NewEncoder returns an Encoder writing to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// WriteCall writes one call. A call with no method is refused before
// anything is written.
func (e *Encoder) WriteCall(c Call) error {
	if c.Method == "" {
		return fmt.Errorf("%w: a call needs a method", ErrInvalidMessage)
	}
	params, err := normalizeParameters(c.Parameters)
	if err != nil {
		return err
	}
	c.Parameters = params
	return e.write(c)
}

// WriteReply writes one reply.
func (e *Encoder) WriteReply(r Reply) error {
	params, err := normalizeParameters(r.Parameters)
	if err != nil {
		return err
	}
	r.Parameters = params
	return e.write(r)
}

// write encodes v and writes it with its NUL in a single Write, so a
// message is never interleaved with another writer's on a shared stream.
// HTML escaping is off: it would grow "<" to six bytes for no reader's
// benefit and push a message that fits over MaxMessageSize.
func (e *Encoder) write(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMessage, err)
	}
	// json.Encoder ends every value with a newline; the NUL replaces it.
	msg := buf.Bytes()
	msg = msg[:len(msg)-1]
	if len(msg) > MaxMessageSize {
		return ErrMessageTooLarge
	}
	msg = append(msg, 0)
	if _, err := e.w.Write(msg); err != nil {
		return fmt.Errorf("write varlink message: %w", err)
	}
	return nil
}

// Decoder reads NUL-terminated varlink messages from a reader. It buffers,
// so one Decoder has to serve a connection for its whole life.
type Decoder struct {
	r   *bufio.Reader
	buf []byte

	// limit is MaxMessageSize except under FuzzDecode, which lowers it so
	// an oversize input is small enough to mutate quickly.
	limit int
}

// NewDecoder returns a Decoder reading from r.
func NewDecoder(r io.Reader) *Decoder {
	return newDecoder(r, MaxMessageSize)
}

// newDecoder returns a Decoder reading from r with a message limit of its
// own.
func newDecoder(r io.Reader, limit int) *Decoder {
	return &Decoder{r: bufio.NewReader(r), limit: limit}
}

// ReadCall reads one call. io.EOF means the peer closed cleanly between
// messages; any other error means the connection should be closed.
func (d *Decoder) ReadCall() (Call, error) {
	var c Call
	if err := d.decode(&c); err != nil {
		return Call{}, err
	}
	if c.Method == "" {
		return Call{}, fmt.Errorf("%w: a call needs a method", ErrInvalidMessage)
	}
	params, err := normalizeParameters(c.Parameters)
	if err != nil {
		return Call{}, err
	}
	c.Parameters = params
	return c, nil
}

// ReadReply reads one reply, with the same error contract as ReadCall.
func (d *Decoder) ReadReply() (Reply, error) {
	var r Reply
	if err := d.decode(&r); err != nil {
		return Reply{}, err
	}
	params, err := normalizeParameters(r.Parameters)
	if err != nil {
		return Reply{}, err
	}
	r.Parameters = params
	return r, nil
}

// decode reads the next message and unmarshals it into v, which must be
// an object. Unknown fields are ignored, as the protocol requires.
func (d *Decoder) decode(v any) error {
	msg, err := d.next()
	if err != nil {
		return err
	}
	if trimmed := trimSpace(msg); len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: not a JSON object", ErrInvalidMessage)
	}
	if err := json.Unmarshal(msg, v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMessage, err)
	}
	return nil
}

// next returns the next message without its NUL. The slice is reused by
// the following call. A stream that ends between messages returns io.EOF;
// one that ends inside a message returns ErrTruncated. Reading stops as
// soon as a message passes MaxMessageSize, so a peer that never sends a
// NUL costs at most one buffer beyond the limit.
func (d *Decoder) next() ([]byte, error) {
	d.buf = d.buf[:0]
	for {
		chunk, err := d.r.ReadSlice(0)
		d.buf = append(d.buf, chunk...)

		size := len(d.buf)
		if err == nil {
			size-- // the NUL is not part of the message
		}
		if size > d.limit {
			return nil, ErrMessageTooLarge
		}

		switch {
		case err == nil:
			return d.buf[:size], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(d.buf) == 0 {
				return nil, io.EOF
			}
			return nil, ErrTruncated
		default:
			return nil, fmt.Errorf("read varlink message: %w", err)
		}
	}
}
