package varlink

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// FuzzDecode feeds arbitrary bytes to the decoder, as a call stream and as
// a reply stream. The decoder reads what a local user wrote to a 0666
// socket, so it must never panic, must only fail in the ways it documents,
// and must decode to something that survives a round trip.
//
// The seeds are the four failures the framing has to survive: a missing
// NUL, an oversize message, invalid JSON and a stream truncated partway
// through its second message.
//
// The decoder under test has a limit of fuzzLimit rather than
// MaxMessageSize. The oversize logic is the same either way, and a 64 KiB
// seed makes every new input a 64 KiB one, which stalls the fuzzer in
// minimisation. TestDecoderErrors covers the real limit.
func FuzzDecode(f *testing.F) {
	const fuzzLimit = 256

	f.Add([]byte(`{"method":"io.systemd.UserDatabase.GetUserRecord","parameters":{"userName":"mike","service":"com.github.mnestor.ssoossh"}}` + "\x00"))
	f.Add([]byte(`{"parameters":{"record":{}},"continues":true}` + "\x00" + `{"error":"io.systemd.UserDatabase.NoRecordFound"}` + "\x00"))
	f.Add([]byte(`{"method":"a.b.C"}`))
	f.Add([]byte(`{"method":"a.b.C","parameters":{"p":"` + strings.Repeat("x", fuzzLimit) + `"}}` + "\x00"))
	f.Add([]byte("{\"method\":\x00"))
	f.Add([]byte(`{"method":"a.b.C"}` + "\x00" + `{"method":"a.b`))
	f.Add([]byte("\x00\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		decodeAll(t, newDecoder(bytes.NewReader(data), fuzzLimit), data, func(d *Decoder) (any, error) { return d.ReadCall() })
		decodeAll(t, newDecoder(bytes.NewReader(data), fuzzLimit), data, func(d *Decoder) (any, error) { return d.ReadReply() })
	})
}

// decodeAll reads dec, which reads data, until the first error, checking
// each message decoded and the error that ends the stream.
func decodeAll(t *testing.T, dec *Decoder, data []byte, read func(*Decoder) (any, error)) {
	t.Helper()

	// One message needs at least its NUL, so data cannot hold more.
	for range len(data) + 1 {
		msg, err := read(dec)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, ErrTruncated) &&
				!errors.Is(err, ErrMessageTooLarge) && !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("undocumented decode error %v for %q", err, data)
			}
			return
		}
		assertRoundTrip(t, msg)
	}
	t.Fatalf("decoded more messages than %q can hold", data)
}

// assertRoundTrip encodes a decoded message, decodes the result and checks
// that a second pass changes nothing. Equality is checked from the second
// pass because the first may normalise (whitespace, \u escapes, invalid
// UTF-8), and a message that only fits before normalising may grow past the
// limit, which is a refusal rather than a failure.
func assertRoundTrip(t *testing.T, msg any) {
	t.Helper()

	first, err := reencode(msg)
	if errors.Is(err, ErrMessageTooLarge) {
		return
	}
	if err != nil {
		t.Fatalf("a decoded message %+v does not encode: %v", msg, err)
	}
	decoded, err := redecode(msg, first)
	if err != nil {
		t.Fatalf("an encoded message %q does not decode: %v", first, err)
	}
	second, err := reencode(decoded)
	if err != nil {
		t.Fatalf("a re-decoded message %+v does not encode: %v", decoded, err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("round trip is not stable:\nfirst  %q\nsecond %q", first, second)
	}
	again, err := redecode(msg, second)
	if err != nil || !reflect.DeepEqual(decoded, again) {
		t.Fatalf("round trip changed the message: %+v then %+v (%v)", decoded, again, err)
	}
}

// reencode writes a decoded call or reply back to bytes.
func reencode(msg any) ([]byte, error) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	var err error
	switch m := msg.(type) {
	case Call:
		err = enc.WriteCall(m)
	case Reply:
		err = enc.WriteReply(m)
	}
	return buf.Bytes(), err
}

// redecode reads data back as the same kind of message as like.
func redecode(like any, data []byte) (any, error) {
	dec := NewDecoder(bytes.NewReader(data))
	if _, ok := like.(Call); ok {
		return dec.ReadCall()
	}
	return dec.ReadReply()
}
