package blackhole

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// echoServer answers every line it reads with the same line, and reports
// the first lines it read on received.
func echoServer(t *testing.T) (addr string, received chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	received = make(chan string, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					select {
					case received <- line:
					default:
					}
					if _, err := io.WriteString(conn, line); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String(), received
}

// startProxy starts a proxy to target, closed with the test.
func startProxy(t *testing.T, target string) *Proxy {
	t.Helper()
	p, err := Start(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// dial connects through p.
func dial(t *testing.T, p *Proxy) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// roundTrip writes line and reads the echo, failing on a timeout.
func roundTrip(conn net.Conn, line string, wait time.Duration) (string, error) {
	_ = conn.SetDeadline(time.Now().Add(wait))
	if _, err := io.WriteString(conn, line); err != nil {
		return "", err
	}
	return bufio.NewReader(conn).ReadString('\n')
}

func TestProxyShouldForwardBothWaysWhenNothingIsDropped(t *testing.T) {
	t.Parallel()
	addr, _ := echoServer(t)
	p := startProxy(t, addr)
	got, err := roundTrip(dial(t, p), "hello\n", time.Second)
	if err != nil || got != "hello\n" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestProxyShouldDropTheEstablishedConnectionOnlyWhenADirectionIsDropped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		drop         Direction
		wantReceived bool
	}{
		{name: "should keep the server from seeing anything when upstream is dropped", drop: Upstream, wantReceived: false},
		{name: "should keep the answer from the client when downstream is dropped", drop: Downstream, wantReceived: true},
		{name: "should silence both halves when both are dropped", drop: Both, wantReceived: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addr, received := echoServer(t)
			p := startProxy(t, addr)
			old := dial(t, p)
			if _, err := roundTrip(old, "before\n", time.Second); err != nil {
				t.Fatal(err)
			}
			<-received

			p.Drop(tt.drop)

			if _, err := roundTrip(old, "after\n", 200*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timeout") {
				t.Fatalf("got %v, want the old connection to go silent", err)
			}
			select {
			case <-received:
				if !tt.wantReceived {
					t.Error("the server received traffic from the dropped direction")
				}
			case <-time.After(100 * time.Millisecond):
				if tt.wantReceived {
					t.Error("the server did not receive the client's traffic")
				}
			}
			// A new connection passes.
			if got, err := roundTrip(dial(t, p), "fresh\n", time.Second); err != nil || got != "fresh\n" {
				t.Fatalf("new connection got %q, %v", got, err)
			}
			if p.Accepted() != 2 {
				t.Errorf("accepted %d, want 2", p.Accepted())
			}
		})
	}
}

func TestProxyShouldReportAClientHangingUpWhenItCloses(t *testing.T) {
	t.Parallel()
	addr, _ := echoServer(t)
	p := startProxy(t, addr)
	conn := dial(t, p)
	if _, err := roundTrip(conn, "hi\n", time.Second); err != nil {
		t.Fatal(err)
	}
	p.Drop(Upstream)
	_ = conn.Close()
	select {
	case <-p.ClientCloses():
	case <-time.After(time.Second):
		t.Fatal("no client close reported")
	}
}

func TestProxyShouldNotReportAClientCloseWhenTheProxyClosesItself(t *testing.T) {
	t.Parallel()
	addr, _ := echoServer(t)
	p, err := Start(addr)
	if err != nil {
		t.Fatal(err)
	}
	conn := dial(t, p)
	if _, err := roundTrip(conn, "hi\n", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.ClientCloses():
		t.Fatal("the proxy's own close was reported as the client's")
	default:
	}
}

func TestProxyShouldCloseTheClientWhenTheTargetIsGone(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := ln.Addr().String()
	_ = ln.Close()
	p := startProxy(t, target)
	conn := dial(t, p)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("got %v, want the connection closed", err)
	}
}

func TestProxyShouldCloseTheLinkWhenTheServerHangsUp(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()
	p := startProxy(t, ln.Addr().String())
	conn := dial(t, p)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("got %v, want the connection closed", err)
	}
}

func TestStartShouldFailWhenItCannotListen(t *testing.T) {
	t.Parallel()
	// An address that cannot be parsed is the portable way to fail Listen.
	if _, err := startOn("tcp", "256.0.0.1:0", "x"); err == nil {
		t.Fatal("want an error")
	}
}

func TestProxyShouldHoldOnlySoManyClosesWhenNoneAreRead(t *testing.T) {
	t.Parallel()
	addr, _ := echoServer(t)
	p := startProxy(t, addr)
	for range maxPendingCloses + 1 {
		conn := dial(t, p)
		if _, err := roundTrip(conn, "hi\n", time.Second); err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(p.ClientCloses()) < maxPendingCloses && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Give the last close time to be dropped rather than block.
	time.Sleep(50 * time.Millisecond)
	if got := len(p.ClientCloses()); got != maxPendingCloses {
		t.Errorf("holding %d closes, want %d", got, maxPendingCloses)
	}
}
