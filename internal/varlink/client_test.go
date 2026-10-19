package varlink

// Test methodology: a real service built from ServerConn answers a real
// Client, over net.Pipe for the portable cases and over an AF_UNIX socket
// in a temporary directory for the one that proves Dial works. Nothing is
// mocked: both halves of the package talk to each other.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// testService answers the methods the tests call. It returns when the
// peer closes or a read fails.
//
//   - test.Echo replies with its own parameters.
//   - test.Stream replies count times, every reply but the last continuing.
//   - test.StreamFail sends one continuing reply, then an error.
//   - test.Fail replies with an error carrying parameters.
//   - test.Silent never replies.
//   - test.Continues wrongly continues a call made without more.
//   - test.Note is one-way: it reports its parameters on notes and sends
//     nothing back.
func testService(conn io.ReadWriter, notes chan<- json.RawMessage) {
	srv := NewServerConn(conn)
	for {
		call, err := srv.ReadCall()
		if err != nil {
			return
		}
		switch call.Method {
		case "test.Echo":
			_ = srv.Reply(call.Parameters)
		case "test.Stream":
			var p struct {
				Count int `json:"count"`
			}
			_ = json.Unmarshal(call.Parameters, &p)
			for i := range p.Count - 1 {
				_ = srv.ReplyContinues(map[string]int{"n": i})
			}
			_ = srv.Reply(map[string]int{"n": p.Count - 1})
		case "test.StreamFail":
			_ = srv.ReplyContinues(map[string]int{"n": 0})
			_ = srv.ReplyError("test.Broken", nil)
		case "test.Fail":
			_ = srv.ReplyError("test.Failed", map[string]string{"why": "because"})
		case "test.Silent":
		case "test.Continues":
			_ = srv.ReplyContinues(nil)
		case "test.Note":
			notes <- call.Parameters
		default:
			_ = srv.ReplyError(ErrorMethodNotFound, map[string]string{"method": call.Method})
		}
	}
}

// pipeClient starts testService on one end of a net.Pipe and returns a
// Client on the other.
func pipeClient(t *testing.T) (*Client, <-chan json.RawMessage) {
	t.Helper()
	clientEnd, serverEnd := net.Pipe()
	notes := make(chan json.RawMessage, 4)
	go testService(serverEnd, notes)
	t.Cleanup(func() {
		_ = clientEnd.Close()
		_ = serverEnd.Close()
	})
	return NewClient(clientEnd), notes
}

// collect returns a CallMore callback that appends each reply's n.
func collect(into *[]int) func(json.RawMessage) error {
	return func(params json.RawMessage) error {
		var p struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		*into = append(*into, p.N)
		return nil
	}
}

// TestClientOverUnixSocket runs several calls of every kind on one AF_UNIX
// connection, in order, the way ssoossh-nss and nss-systemd use one.
func TestClientOverUnixSocket(t *testing.T) {
	// Short and outside t.TempDir(): macOS caps a socket path at 104 bytes,
	// and a test's temporary directory there can be longer than that.
	dir, err := os.MkdirTemp("", "vl")
	if err != nil {
		t.Fatalf("make socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")

	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix sockets are unavailable here: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	notes := make(chan json.RawMessage, 4)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		testService(conn, notes)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Dial(ctx, path)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	t.Run("should decode the reply when a call succeeds", func(t *testing.T) {
		var out struct {
			UserName string `json:"userName"`
		}
		if err := client.Call(ctx, "test.Echo", map[string]string{"userName": "mike"}, &out); err != nil {
			t.Fatalf("Call() error = %v", err)
		}
		if out.UserName != "mike" {
			t.Errorf("got %q, want %q", out.UserName, "mike")
		}
	})

	t.Run("should discard the reply when the caller passes no destination", func(t *testing.T) {
		if err := client.Call(ctx, "test.Echo", nil, nil); err != nil {
			t.Fatalf("Call() error = %v", err)
		}
	})

	t.Run("should yield every reply when a call streams", func(t *testing.T) {
		var got []int
		if err := client.CallMore(ctx, "test.Stream", map[string]int{"count": 3}, collect(&got)); err != nil {
			t.Fatalf("CallMore() error = %v", err)
		}
		if want := []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("should return the error when a stream ends in one", func(t *testing.T) {
		var got []int
		err := client.CallMore(ctx, "test.StreamFail", nil, collect(&got))
		var vErr *Error
		if !errors.As(err, &vErr) || vErr.Name != "test.Broken" {
			t.Fatalf("got %v, want the test.Broken error reply", err)
		}
		if want := []int{0}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v before the error, want %v", got, want)
		}
	})

	t.Run("should return the error name and parameters when a call fails", func(t *testing.T) {
		err := client.Call(ctx, "test.Fail", nil, nil)
		var vErr *Error
		if !errors.As(err, &vErr) {
			t.Fatalf("got %v, want an *Error", err)
		}
		if vErr.Name != "test.Failed" || string(vErr.Parameters) != `{"why":"because"}` {
			t.Errorf("got %s %s, want test.Failed {\"why\":\"because\"}", vErr.Name, vErr.Parameters)
		}
	})

	t.Run("should deliver a oneway call and keep the connection in step", func(t *testing.T) {
		if err := client.CallOneway(ctx, "test.Note", map[string]string{"userName": "mike"}); err != nil {
			t.Fatalf("CallOneway() error = %v", err)
		}
		// A reply to the oneway call, had one been sent, would be read
		// here as Echo's answer.
		var out map[string]string
		if err := client.Call(ctx, "test.Echo", map[string]string{"after": "note"}, &out); err != nil {
			t.Fatalf("Call() after a oneway call error = %v", err)
		}
		if out["after"] != "note" {
			t.Errorf("got %v, want the echo of the call after the note", out)
		}
		if got := <-notes; string(got) != `{"userName":"mike"}` {
			t.Errorf("the service received %s", got)
		}
	})
}

// TestDialShouldFailWhenNothingListens covers a service that is not
// running.
func TestDialShouldFailWhenNothingListens(t *testing.T) {
	t.Parallel()

	_, err := Dial(context.Background(), filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("Dial() succeeded with nothing listening")
	}
}

// TestClientShouldEndTheCallWhenTheContextDoes covers the two ways a
// caller's context ends a call that the service never answers.
func TestClientShouldEndTheCallWhenTheContextDoes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     func() (context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "should return DeadlineExceeded when the deadline passes",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "should return Canceled when the caller cancels",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(50*time.Millisecond, cancel)
				return ctx, cancel
			},
			wantErr: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, _ := pipeClient(t)
			ctx, cancel := tt.ctx()
			defer cancel()

			err := client.Call(ctx, "test.Silent", nil, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// lateContext is a context whose deadline has passed but whose Err is
// still nil: the moment between a connection's deadline firing and the
// context's own timer, made to last.
type lateContext struct {
	context.Context
}

// Deadline implements context.Context with a deadline long past.
func (lateContext) Deadline() (time.Time, bool) { return time.Unix(1, 0), true }

// TestClientShouldBlameTheDeadlineWhenTheContextHasNotNoticedYet covers
// the race bind resolves: the read times out before ctx.Err() is set.
func TestClientShouldBlameTheDeadlineWhenTheContextHasNotNoticedYet(t *testing.T) {
	t.Parallel()

	client, _ := pipeClient(t)
	err := client.Call(lateContext{context.Background()}, "test.Silent", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want context.DeadlineExceeded", err)
	}
}

// TestContextError covers how a call's error is attributed to its context.
func TestContextError(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	future := time.Now().Add(time.Hour)
	past := time.Unix(1, 0)

	tests := []struct {
		name     string
		ctx      context.Context
		deadline time.Time
		err      error
		want     error
	}{
		{name: "should pass nil through when the call succeeded", ctx: context.Background(), err: nil, want: nil},
		{name: "should leave an error alone when it is not a timeout", ctx: cancelled, err: io.EOF, want: io.EOF},
		{name: "should blame the context when it was cancelled", ctx: cancelled, err: os.ErrDeadlineExceeded, want: context.Canceled},
		{name: "should blame the deadline when it has passed", ctx: context.Background(), deadline: past, err: os.ErrDeadlineExceeded, want: context.DeadlineExceeded},
		{name: "should leave a timeout alone when the context has no part in it", ctx: context.Background(), deadline: future, err: os.ErrDeadlineExceeded, want: os.ErrDeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := contextError(tt.ctx, tt.deadline, tt.err)
			if tt.want == nil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClientShouldFailACallWhenTheConnectionIsClosed covers a call on a
// connection the caller already closed: the deadline cannot be set, so
// nothing is sent.
func TestClientShouldFailACallWhenTheConnectionIsClosed(t *testing.T) {
	t.Parallel()

	client, _ := pipeClient(t)
	_ = client.Close()

	if err := client.Call(context.Background(), "test.Echo", nil, nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("got %v, want the closed connection's error", err)
	}
}

// stubbornConn accepts its first deadline and refuses every later one,
// standing in for a connection that breaks while a call is blocked on it.
type stubbornConn struct {
	net.Conn
	deadlines atomic.Int32
}

// SetDeadline implements net.Conn.
func (c *stubbornConn) SetDeadline(t time.Time) error {
	if c.deadlines.Add(1) > 1 {
		return errors.New("deadline refused")
	}
	return c.Conn.SetDeadline(t)
}

// TestClientShouldCloseTheConnectionWhenCancellationCannotSetADeadline
// covers the fallback: a cancelled call still returns when the connection
// refuses the deadline that would interrupt it.
func TestClientShouldCloseTheConnectionWhenCancellationCannotSetADeadline(t *testing.T) {
	t.Parallel()

	clientEnd, serverEnd := net.Pipe()
	notes := make(chan json.RawMessage, 1)
	go testService(serverEnd, notes)
	t.Cleanup(func() { _ = serverEnd.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	defer cancel()

	err := NewClient(&stubbornConn{Conn: clientEnd}).Call(ctx, "test.Silent", nil, nil)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("got %v, want the call ended by closing the connection", err)
	}
}

// TestClientCallErrors covers the failures a call reports without a
// service error reply.
func TestClientCallErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		call    func(*Client) error
		wantErr func(error) bool
	}{
		{
			name: "should reject a continuing reply when more was not asked",
			call: func(c *Client) error { return c.Call(context.Background(), "test.Continues", nil, nil) },
			wantErr: func(err error) bool {
				return errors.Is(err, ErrInvalidMessage)
			},
		},
		{
			name: "should report a reply when it does not fit the caller's type",
			call: func(c *Client) error {
				var out struct {
					UserName int `json:"userName"`
				}
				return c.Call(context.Background(), "test.Echo", map[string]string{"userName": "mike"}, &out)
			},
			wantErr: func(err error) bool {
				var typeErr *json.UnmarshalTypeError
				return errors.As(err, &typeErr)
			},
		},
		{
			name: "should refuse parameters when they are not an object",
			call: func(c *Client) error { return c.Call(context.Background(), "test.Echo", []int{1}, nil) },
			wantErr: func(err error) bool {
				return errors.Is(err, ErrInvalidMessage)
			},
		},
		{
			name: "should refuse a call when it has no method",
			call: func(c *Client) error { return c.CallOneway(context.Background(), "", nil) },
			wantErr: func(err error) bool {
				return errors.Is(err, ErrInvalidMessage)
			},
		},
		{
			name: "should refuse a stream when its parameters are not an object",
			call: func(c *Client) error {
				return c.CallMore(context.Background(), "test.Stream", "x", func(json.RawMessage) error { return nil })
			},
			wantErr: func(err error) bool {
				return errors.Is(err, ErrInvalidMessage)
			},
		},
		{
			name: "should stop a stream when the callback fails",
			call: func(c *Client) error {
				return c.CallMore(context.Background(), "test.Stream", map[string]int{"count": 3}, func(json.RawMessage) error {
					return io.ErrShortBuffer
				})
			},
			wantErr: func(err error) bool {
				return errors.Is(err, io.ErrShortBuffer)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, _ := pipeClient(t)
			if err := tt.call(client); !tt.wantErr(err) {
				t.Errorf("got error %v, not the kind expected", err)
			}
		})
	}
}

// TestClientShouldReportAConnectionTheServiceClosed covers a service that
// goes away between the call and the reply.
func TestClientShouldReportAConnectionTheServiceClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*Client) error
	}{
		{name: "should fail a call when the service closes", call: func(c *Client) error {
			return c.Call(context.Background(), "test.Echo", nil, nil)
		}},
		{name: "should fail a stream when the service closes", call: func(c *Client) error {
			return c.CallMore(context.Background(), "test.Stream", nil, func(json.RawMessage) error { return nil })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clientEnd, serverEnd := net.Pipe()
			t.Cleanup(func() { _ = clientEnd.Close() })
			go func() {
				_, _ = NewServerConn(serverEnd).ReadCall()
				_ = serverEnd.Close()
			}()

			if err := tt.call(NewClient(clientEnd)); err == nil {
				t.Error("got no error from a connection the service closed")
			}
		})
	}
}

// TestServerConnShouldRefuseBadReplies covers what a service cannot send.
func TestServerConnShouldRefuseBadReplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		reply func(*ServerConn) error
	}{
		{name: "should refuse an error reply when it has no name", reply: func(s *ServerConn) error { return s.ReplyError("", nil) }},
		{name: "should refuse error parameters when they are not an object", reply: func(s *ServerConn) error { return s.ReplyError("test.Failed", 5) }},
		{name: "should refuse reply parameters when they are not an object", reply: func(s *ServerConn) error { return s.Reply("x") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rw struct {
				io.Reader
				io.Writer
			}
			rw.Reader = eofReader{}
			rw.Writer = io.Discard
			if err := tt.reply(NewServerConn(rw)); !errors.Is(err, ErrInvalidMessage) {
				t.Errorf("got %v, want ErrInvalidMessage", err)
			}
		})
	}
}

// eofReader is a connection with nothing more to read.
type eofReader struct{}

// Read implements io.Reader.
func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }
