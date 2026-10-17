package varlink

// Test methodology: encode into a bytes.Buffer and decode back, so every
// case exercises the real framing (one JSON object, one NUL) rather than a
// struct comparison. Error cases feed hand-written bytes to the decoder,
// since a correct encoder cannot produce them.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestCallRoundTrip encodes calls and decodes them back.
func TestCallRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call Call
		want Call
	}{
		{
			name: "should send {} when a call has no parameters",
			call: Call{Method: "org.varlink.service.GetInfo"},
			want: Call{Method: "org.varlink.service.GetInfo", Parameters: json.RawMessage(`{}`)},
		},
		{
			name: "should keep the parameters when a call has some",
			call: Call{Method: "io.systemd.UserDatabase.GetUserRecord", Parameters: json.RawMessage(`{"userName":"mike","service":"com.github.mnestor.ssoossh"}`)},
			want: Call{Method: "io.systemd.UserDatabase.GetUserRecord", Parameters: json.RawMessage(`{"userName":"mike","service":"com.github.mnestor.ssoossh"}`)},
		},
		{
			name: "should keep more when a call asks for a stream",
			call: Call{Method: "io.systemd.UserDatabase.GetMemberships", More: true},
			want: Call{Method: "io.systemd.UserDatabase.GetMemberships", Parameters: json.RawMessage(`{}`), More: true},
		},
		{
			name: "should keep oneway when a call asks for no reply",
			call: Call{Method: "com.github.mnestor.ssoossh.NSS.RefreshUser", Parameters: json.RawMessage(`{"userName":"mike"}`), Oneway: true},
			want: Call{Method: "com.github.mnestor.ssoossh.NSS.RefreshUser", Parameters: json.RawMessage(`{"userName":"mike"}`), Oneway: true},
		},
		{
			name: "should not escape html characters when a parameter carries them",
			call: Call{Method: "a.b.C", Parameters: json.RawMessage(`{"realName":"<Mike & co>"}`)},
			want: Call{Method: "a.b.C", Parameters: json.RawMessage(`{"realName":"<Mike & co>"}`)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := NewEncoder(&buf).WriteCall(tt.call); err != nil {
				t.Fatalf("WriteCall() error = %v", err)
			}
			got, err := NewDecoder(&buf).ReadCall()
			if err != nil {
				t.Fatalf("ReadCall() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v (parameters %s), want %+v (parameters %s)", got, got.Parameters, tt.want, tt.want.Parameters)
			}
		})
	}
}

// TestReplyRoundTrip encodes replies and decodes them back.
func TestReplyRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		reply Reply
		want  Reply
	}{
		{
			name:  "should send {} when a reply has no parameters",
			reply: Reply{},
			want:  Reply{Parameters: json.RawMessage(`{}`)},
		},
		{
			name:  "should keep the parameters when a reply has some",
			reply: Reply{Parameters: json.RawMessage(`{"record":{"userName":"mike","uid":1234}}`)},
			want:  Reply{Parameters: json.RawMessage(`{"record":{"userName":"mike","uid":1234}}`)},
		},
		{
			name:  "should keep the error name when a reply is an error",
			reply: Reply{Error: "io.systemd.UserDatabase.NoRecordFound"},
			want:  Reply{Error: "io.systemd.UserDatabase.NoRecordFound", Parameters: json.RawMessage(`{}`)},
		},
		{
			name:  "should keep error parameters when an error carries them",
			reply: Reply{Error: ErrorInvalidParameter, Parameters: json.RawMessage(`{"parameter":"fuzzyNames"}`)},
			want:  Reply{Error: ErrorInvalidParameter, Parameters: json.RawMessage(`{"parameter":"fuzzyNames"}`)},
		},
		{
			name:  "should keep continues when a reply is mid-stream",
			reply: Reply{Parameters: json.RawMessage(`{"groupName":"sudo"}`), Continues: true},
			want:  Reply{Parameters: json.RawMessage(`{"groupName":"sudo"}`), Continues: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := NewEncoder(&buf).WriteReply(tt.reply); err != nil {
				t.Fatalf("WriteReply() error = %v", err)
			}
			got, err := NewDecoder(&buf).ReadReply()
			if err != nil {
				t.Fatalf("ReadReply() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v (parameters %s), want %+v (parameters %s)", got, got.Parameters, tt.want, tt.want.Parameters)
			}
		})
	}
}

// TestContinuesStreamRoundTrip writes a whole more stream into one buffer
// and reads it back, the case where several messages share one read.
func TestContinuesStreamRoundTrip(t *testing.T) {
	t.Parallel()

	stream := []Reply{
		{Parameters: json.RawMessage(`{"userName":"mike","groupName":"mike"}`), Continues: true},
		{Parameters: json.RawMessage(`{"userName":"mike","groupName":"sudo"}`), Continues: true},
		{Parameters: json.RawMessage(`{"userName":"mike","groupName":"wheel"}`)},
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for _, r := range stream {
		if err := enc.WriteReply(r); err != nil {
			t.Fatalf("WriteReply() error = %v", err)
		}
	}

	dec := NewDecoder(&buf)
	var got []Reply
	for {
		r, err := dec.ReadReply()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadReply() error = %v", err)
		}
		got = append(got, r)
	}

	if !reflect.DeepEqual(got, stream) {
		t.Errorf("got %+v, want %+v", got, stream)
	}
}

// messageOfSize returns a call whose JSON is exactly n bytes long.
func messageOfSize(t *testing.T, n int) []byte {
	t.Helper()
	const prefix = `{"method":"a.b.C","parameters":{"p":"`
	const suffix = `"}}`
	pad := n - len(prefix) - len(suffix)
	if pad < 0 {
		t.Fatalf("cannot build a %d-byte message", n)
	}
	return []byte(prefix + strings.Repeat("x", pad) + suffix)
}

// TestDecoderShouldAcceptAMessageOfExactlyTheLimit pins the boundary: the
// limit counts the JSON, not the NUL.
func TestDecoderShouldAcceptAMessageOfExactlyTheLimit(t *testing.T) {
	t.Parallel()

	msg := append(messageOfSize(t, MaxMessageSize), 0)
	if _, err := NewDecoder(bytes.NewReader(msg)).ReadCall(); err != nil {
		t.Errorf("ReadCall() error = %v, want a %d-byte message accepted", err, MaxMessageSize)
	}
}

// TestDecoderErrors feeds the decoder bytes no correct encoder produces.
func TestDecoderErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   []byte
		reply   bool
		wantErr error
	}{
		{name: "should report EOF when the stream is empty", input: nil, wantErr: io.EOF},
		{name: "should report truncation when the NUL is missing", input: []byte(`{"method":"a.b.C"}`), wantErr: ErrTruncated},
		{name: "should report truncation when the stream stops mid-message", input: []byte(`{"meth`), wantErr: ErrTruncated},
		{name: "should report an oversize message when it passes the limit", input: append(messageOfSize(t, MaxMessageSize+1), 0), wantErr: ErrMessageTooLarge},
		{name: "should report an oversize message when no NUL ever arrives", input: bytes.Repeat([]byte("x"), MaxMessageSize+4096), wantErr: ErrMessageTooLarge},
		{name: "should reject a message when it is not JSON", input: []byte("{not json}\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a message when it is empty", input: []byte("\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a message when it is an array", input: []byte("[]\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a message when it is null", input: []byte("null\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a call when it has no method", input: []byte(`{"parameters":{}}` + "\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a call when the method is not a string", input: []byte(`{"method":5}` + "\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a call when the parameters are not an object", input: []byte(`{"method":"a.b.C","parameters":[1]}` + "\x00"), wantErr: ErrInvalidMessage},
		{name: "should reject a reply when the parameters are not an object", input: []byte(`{"parameters":"x"}` + "\x00"), reply: true, wantErr: ErrInvalidMessage},
		{name: "should reject a reply when it is not JSON", input: []byte("{\x00"), reply: true, wantErr: ErrInvalidMessage},
		{name: "should reject a message when JSON follows the object", input: []byte(`{"method":"a.b.C"} {}` + "\x00"), wantErr: ErrInvalidMessage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dec := NewDecoder(bytes.NewReader(tt.input))
			var err error
			if tt.reply {
				_, err = dec.ReadReply()
			} else {
				_, err = dec.ReadCall()
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestDecoderShouldIgnoreUnknownFields covers the protocol's
// forward-compatibility rule: a field this package does not know, such as
// "upgrade" or a future one, is skipped rather than refused.
func TestDecoderShouldIgnoreUnknownFields(t *testing.T) {
	t.Parallel()

	input := []byte(`{"method":"a.b.C","upgrade":true,"future":{"x":1},"parameters":null}` + "\x00")
	got, err := NewDecoder(bytes.NewReader(input)).ReadCall()
	if err != nil {
		t.Fatalf("ReadCall() error = %v", err)
	}
	want := Call{Method: "a.b.C", Parameters: json.RawMessage(`{}`)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestDecoderShouldAcceptWhitespaceAroundTheObject covers a peer that
// pretty-prints: JSON allows whitespace around a value, and so does the
// framing.
func TestDecoderShouldAcceptWhitespaceAroundTheObject(t *testing.T) {
	t.Parallel()

	input := []byte(" \n{\"method\":\"a.b.C\",\"parameters\": {\"x\": 1} }\r\n\t\x00")
	got, err := NewDecoder(bytes.NewReader(input)).ReadCall()
	if err != nil {
		t.Fatalf("ReadCall() error = %v", err)
	}
	if got.Method != "a.b.C" || string(got.Parameters) != `{"x": 1}` {
		t.Errorf("got %+v (parameters %s)", got, got.Parameters)
	}
}

// failingWriter fails every write, standing in for a closed connection.
type failingWriter struct{}

// Write implements io.Writer.
func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestEncoderErrors covers what the encoder refuses to put on the wire.
func TestEncoderErrors(t *testing.T) {
	t.Parallel()

	tooLarge := json.RawMessage(`{"p":"` + strings.Repeat("x", MaxMessageSize) + `"}`)

	tests := []struct {
		name    string
		write   func(*Encoder) error
		wantErr error
	}{
		{
			name:    "should refuse a call when it has no method",
			write:   func(e *Encoder) error { return e.WriteCall(Call{}) },
			wantErr: ErrInvalidMessage,
		},
		{
			name:    "should refuse a call when its parameters are not an object",
			write:   func(e *Encoder) error { return e.WriteCall(Call{Method: "a.b.C", Parameters: json.RawMessage(`[]`)}) },
			wantErr: ErrInvalidMessage,
		},
		{
			name:    "should refuse a call when its parameters are not valid JSON",
			write:   func(e *Encoder) error { return e.WriteCall(Call{Method: "a.b.C", Parameters: json.RawMessage(`{bad`)}) },
			wantErr: ErrInvalidMessage,
		},
		{
			name:    "should refuse a reply when its parameters are not an object",
			write:   func(e *Encoder) error { return e.WriteReply(Reply{Parameters: json.RawMessage(`7`)}) },
			wantErr: ErrInvalidMessage,
		},
		{
			name:    "should refuse a call when it is over the limit",
			write:   func(e *Encoder) error { return e.WriteCall(Call{Method: "a.b.C", Parameters: tooLarge}) },
			wantErr: ErrMessageTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := tt.write(NewEncoder(&buf))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
			if buf.Len() != 0 {
				t.Errorf("a refused message wrote %d bytes", buf.Len())
			}
		})
	}
}

// TestEncoderShouldReportAWriteFailure covers a connection that is gone.
func TestEncoderShouldReportAWriteFailure(t *testing.T) {
	t.Parallel()

	err := NewEncoder(failingWriter{}).WriteReply(Reply{})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("got %v, want the writer's error", err)
	}
}

// failingReader fails every read with something other than EOF.
type failingReader struct{}

// Read implements io.Reader.
func (failingReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestDecoderShouldReportAReadFailure covers a connection that fails
// rather than closing.
func TestDecoderShouldReportAReadFailure(t *testing.T) {
	t.Parallel()

	_, err := NewDecoder(failingReader{}).ReadCall()
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("got %v, want the reader's error", err)
	}
}

// TestEncodeParameters covers the forms a caller may pass parameters in.
func TestEncodeParameters(t *testing.T) {
	t.Parallel()

	isInvalid := func(err error) bool { return errors.Is(err, ErrInvalidMessage) }
	isUnsupported := func(err error) bool {
		var unsupported *json.UnsupportedTypeError
		return errors.As(err, &unsupported)
	}

	tests := []struct {
		name    string
		params  any
		want    string
		wantErr func(error) bool
	}{
		{name: "should give {} when there are no parameters", params: nil, want: `{}`},
		{name: "should pass raw JSON through when the caller encoded it", params: json.RawMessage(`{"a":1}`), want: `{"a":1}`},
		{name: "should marshal a struct when the caller passes one", params: struct {
			UserName string `json:"userName"`
		}{UserName: "mike"}, want: `{"userName":"mike"}`},
		{name: "should refuse a value when it does not marshal to an object", params: []string{"x"}, wantErr: isInvalid},
		{name: "should refuse a value when it cannot be marshalled", params: map[string]any{"c": make(chan int)}, wantErr: isUnsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := encodeParameters(tt.params)
			if tt.wantErr != nil {
				if !tt.wantErr(err) {
					t.Errorf("got error %v, not the kind expected", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("encodeParameters() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// TestErrorShouldNameTheVarlinkError covers what an error reply prints.
func TestErrorShouldNameTheVarlinkError(t *testing.T) {
	t.Parallel()

	err := &Error{Name: "io.systemd.UserDatabase.NoRecordFound"}
	if got, want := err.Error(), "varlink error io.systemd.UserDatabase.NoRecordFound"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
