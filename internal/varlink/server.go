package varlink

import (
	"fmt"
	"io"
)

// ServerConn is the serving side of one varlink connection: read a call,
// answer it, repeat. It owns no deadlines and no goroutines; the service
// sets read and write deadlines on its own connection and decides when to
// close it.
//
// Answering a oneway call is the service's mistake to avoid: ReadCall
// reports Oneway, and a reply sent anyway would be read by the peer as the
// answer to its next call.
type ServerConn struct {
	enc *Encoder
	dec *Decoder
}

// NewServerConn wraps an accepted connection, or any reader and writer
// pair such as a test's buffer.
func NewServerConn(rw io.ReadWriter) *ServerConn {
	return &ServerConn{enc: NewEncoder(rw), dec: NewDecoder(rw)}
}

// ReadCall reads the next call. io.EOF means the peer closed between
// calls; any other error means the connection should be closed.
func (s *ServerConn) ReadCall() (Call, error) {
	return s.dec.ReadCall()
}

// Reply answers a call with its only reply, or the last reply of a more
// stream. params may be nil, a json.RawMessage or anything that marshals
// to a JSON object.
func (s *ServerConn) Reply(params any) error {
	return s.reply(params, false)
}

// ReplyContinues sends one reply of a more stream that has more to come.
// The stream ends with Reply or ReplyError.
func (s *ServerConn) ReplyContinues(params any) error {
	return s.reply(params, true)
}

// ReplyError answers a call with the named error, which also ends a more
// stream. params may be nil when the error carries none. An empty name is
// refused: it would go out as a success reply.
func (s *ServerConn) ReplyError(name string, params any) error {
	if name == "" {
		return fmt.Errorf("%w: an error reply needs an error name", ErrInvalidMessage)
	}
	raw, err := encodeParameters(params)
	if err != nil {
		return err
	}
	return s.enc.WriteReply(Reply{Error: name, Parameters: raw})
}

// reply encodes params and writes one success reply.
func (s *ServerConn) reply(params any, continues bool) error {
	raw, err := encodeParameters(params)
	if err != nil {
		return err
	}
	return s.enc.WriteReply(Reply{Parameters: raw, Continues: continues})
}
