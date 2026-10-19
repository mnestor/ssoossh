package api

// Test methodology: an httptest TLS server with HTTP/2 enabled sits behind
// a blackhole.Proxy, and the client is pinned to the server's key, as
// ssoossh-nss runs it. Dropping the client-to-server half of the proxied
// connection reproduces the field failure: the client's frames are
// accepted by the socket and never reach the server, nothing is refused or
// reset, and the connection stays in the client's pool.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/blackhole"
)

// h2Fixture is an HTTP/2 NSS server behind a black-hole proxy.
type h2Fixture struct {
	server *httptest.Server
	proxy  *blackhole.Proxy
	// notH2 counts requests that arrived over anything but HTTP/2.
	notH2 atomic.Int32
}

// newH2Fixture starts the server and the proxy in front of it.
func newH2Fixture(t *testing.T) *h2Fixture {
	t.Helper()
	f := &h2Fixture{}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			f.notH2.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(userBody))
	}))
	f.server.EnableHTTP2 = true
	f.server.StartTLS()
	t.Cleanup(f.server.Close)

	proxy, err := blackhole.Start(f.server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("start the proxy: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	f.proxy = proxy
	return f
}

// client builds a pinned NSS client through the proxy with the given
// request timeout and HTTP/2 health checks (nil turns them off).
func (f *h2Fixture) client(t *testing.T, timeout time.Duration, h2 *http.HTTP2Config) *HTTPClient {
	t.Helper()
	c, err := NewClient(Config{
		ServerURL:      "https://" + f.proxy.Addr(),
		TLSSPKIPins:    []string{pinOf(f.server.Certificate())},
		KeyID:          testKeyID,
		KeySecret:      testKeySecret,
		RequestTimeout: timeout,
		DialTimeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	transport, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
	}
	transport.HTTP2 = h2
	return c
}

func TestNewClient_ShouldInstallHTTP2HealthChecksWhenBuilt(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{ServerURL: "https://ssh.example.test", DialTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	transport, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
	}
	if transport.HTTP2 == nil || transport.HTTP2.SendPingTimeout != 15*time.Second || transport.HTTP2.PingTimeout != 5*time.Second {
		t.Errorf("HTTP2 = %+v, want a ping after 15s idle and a 5s answer bound", transport.HTTP2)
	}
}

func TestNewTransport_ShouldLeaveTheEventsStreamWithoutPingsWhenBuilt(t *testing.T) {
	t.Parallel()

	if got := newTransport(nil, false).HTTP2; got != nil && got.SendPingTimeout != 0 {
		t.Errorf("HTTP2 = %+v, want no health check on the events stream", got)
	}
}

// TestHealthCheck_IdleBlackHoledConnection is the field failure: a lookup
// leaves an HTTP/2 connection in the pool, something on the path starts
// dropping its client-to-server traffic, and the connection sits idle
// before the next lookup.
func TestHealthCheck_IdleBlackHoledConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// h2 is the client's HTTP/2 config; nil is no health check, the
		// behaviour before the fix.
		h2 *http.HTTP2Config
		// wantClosed is whether the client should give up the
		// connection while it is idle.
		wantClosed bool
	}{
		{
			name:       "should close the connection and dial anew when the ping goes unanswered",
			h2:         &http.HTTP2Config{SendPingTimeout: 100 * time.Millisecond, PingTimeout: 100 * time.Millisecond},
			wantClosed: true,
		},
		{
			name:       "should time out on the reused connection when there is no health check",
			h2:         nil,
			wantClosed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newH2Fixture(t)
			c := f.client(t, 500*time.Millisecond, tt.h2)

			if _, err := c.GetNSSUserByName(context.Background(), "Mike.N"); err != nil {
				t.Fatalf("first lookup: %v", err)
			}
			f.proxy.Drop(blackhole.Upstream)

			// Idle long enough for several health checks.
			select {
			case <-f.proxy.ClientCloses():
				if !tt.wantClosed {
					t.Fatal("the client closed the connection with no health check to do it")
				}
			case <-time.After(time.Second):
				if tt.wantClosed {
					t.Fatal("the client kept the black-holed connection past its ping timeout")
				}
			}

			start := time.Now()
			_, err := c.GetNSSUserByName(context.Background(), "Mike.N")
			elapsed := time.Since(start)

			if tt.wantClosed {
				if err != nil {
					t.Fatalf("lookup after the black hole: %v", err)
				}
				if elapsed > 400*time.Millisecond {
					t.Errorf("lookup took %s, want a prompt answer on a fresh connection", elapsed)
				}
				if got := f.proxy.Accepted(); got != 2 {
					t.Errorf("proxy accepted %d connections, want 2", got)
				}
			} else {
				var transportErr *TransportError
				if !errors.As(err, &transportErr) || !strings.Contains(transportErr.Reason, "on a reused connection") {
					t.Fatalf("got %v, want a timeout naming the reused connection", err)
				}
				if got := f.proxy.Accepted(); got != 1 {
					t.Errorf("proxy accepted %d connections, want the one dead connection reused", got)
				}
			}
			if n := f.notH2.Load(); n != 0 {
				t.Errorf("%d requests arrived over HTTP/1, want HTTP/2 throughout", n)
			}
		})
	}
}

func TestCloseIdleConnections_ShouldMakeTheNextCallDialAnewWhenCalled(t *testing.T) {
	t.Parallel()

	f := newH2Fixture(t)
	c := f.client(t, 500*time.Millisecond, nil)
	if _, err := c.GetNSSUserByName(context.Background(), "Mike.N"); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	f.proxy.Drop(blackhole.Upstream)

	c.CloseIdleConnections()

	if _, err := c.GetNSSUserByName(context.Background(), "Mike.N"); err != nil {
		t.Fatalf("lookup after closing the pool: %v", err)
	}
	if got := f.proxy.Accepted(); got != 2 {
		t.Errorf("proxy accepted %d connections, want 2", got)
	}
}

func TestSend_ShouldNameTheCallersDeadlineWhenItIsSoonerThanTheTimeout(t *testing.T) {
	t.Parallel()

	ts := silentServer(t)
	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err = c.GetCA(ctx)

	var transportErr *TransportError
	if !errors.As(err, &transportErr) || !strings.HasPrefix(transportErr.Reason, "no response within ") || strings.Contains(transportErr.Reason, "2m0s") {
		t.Fatalf("got %v, want it to name the caller's 300ms deadline, not the 2m0s request timeout", err)
	}
}
