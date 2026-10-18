// Package blackhole is a TCP proxy for tests that can silently drop the
// traffic of connections already established through it, the way a
// stateful firewall or NAT that has lost a flow's state does: nothing is
// refused or reset, the bytes are simply never delivered. Connections made
// after the drop pass normally. Import it only from _test.go files.
package blackhole

import (
	"net"
	"sync"
	"sync/atomic"
)

// Direction names one or both halves of a proxied connection.
type Direction int32

const (
	// Upstream is the client to server half.
	Upstream Direction = 1 << iota
	// Downstream is the server to client half.
	Downstream
	// Both is both halves.
	Both = Upstream | Downstream
)

// maxPendingCloses is how many client closes ClientCloses holds unread.
const maxPendingCloses = 64

// Proxy forwards every connection accepted on its address to a target.
type Proxy struct {
	ln     net.Listener
	target string

	mu       sync.Mutex
	links    []*link
	accepted int

	// clientCloses gets one value each time a client ends its side of a
	// connection, so a test can wait for a client to give one up. Closes
	// beyond maxPendingCloses unread ones are not delivered.
	clientCloses chan struct{}

	wg sync.WaitGroup
}

// link is one proxied connection.
type link struct {
	client, server net.Conn
	// dropped holds the Direction bits whose traffic is discarded.
	dropped atomic.Int32
	// closing is set once the proxy itself closes the link, so a read
	// error it causes is not mistaken for the client hanging up.
	closing atomic.Bool
	once    sync.Once
}

// Start listens on a loopback port and forwards to target (host:port).
func Start(target string) (*Proxy, error) {
	return startOn("tcp", "127.0.0.1:0", target)
}

// startOn is Start on a chosen listen address.
func startOn(network, address, target string) (*Proxy, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	p := &Proxy{ln: ln, target: target, clientCloses: make(chan struct{}, maxPendingCloses)}
	p.wg.Add(1)
	go p.accept()
	return p, nil
}

// Addr is the proxy's host:port.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// Accepted is how many client connections the proxy has accepted.
func (p *Proxy) Accepted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted
}

// ClientCloses delivers one value per connection whose client hung up,
// holding up to maxPendingCloses unread.
func (p *Proxy) ClientCloses() <-chan struct{} { return p.clientCloses }

// Drop starts discarding d's traffic on every connection open now. Later
// connections are not affected.
func (p *Proxy) Drop(d Direction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.links {
		l.dropped.Store(l.dropped.Load() | int32(d))
	}
}

// Close stops accepting, closes every connection and waits for the
// forwarding goroutines.
func (p *Proxy) Close() error {
	err := p.ln.Close()
	p.mu.Lock()
	links := append([]*link(nil), p.links...)
	p.mu.Unlock()
	for _, l := range links {
		l.close()
	}
	p.wg.Wait()
	return err
}

// accept runs until the listener closes.
func (p *Proxy) accept() {
	defer p.wg.Done()
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.target)
		if err != nil {
			// The target is gone: refuse this client the way it would be.
			_ = client.Close()
			continue
		}
		l := &link{client: client, server: server}
		p.mu.Lock()
		p.links = append(p.links, l)
		p.accepted++
		p.mu.Unlock()
		p.wg.Add(2)
		go p.pump(l, Upstream, server, client)
		go p.pump(l, Downstream, client, server)
	}
}

// pump copies src to dst until either ends, discarding what it reads while
// dir is dropped. It keeps reading while dropped, so a sender sees its
// writes accepted and never answered, as across a real black hole.
func (p *Proxy) pump(l *link, dir Direction, dst, src net.Conn) {
	defer p.wg.Done()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && Direction(l.dropped.Load())&dir == 0 {
			// A failed write means dst is gone, and the pump reading dst
			// then fails too and closes the link, ending this read.
			_, _ = dst.Write(buf[:n]) //nolint:errcheck // see above
		}
		if err != nil {
			// A read the proxy's own close caused is not the client
			// hanging up.
			if dir == Upstream && !l.closing.Load() {
				select {
				case p.clientCloses <- struct{}{}:
				default:
				}
			}
			l.close()
			return
		}
	}
}

// close ends both halves once.
func (l *link) close() {
	l.once.Do(func() {
		l.closing.Store(true)
		_ = l.client.Close()
		_ = l.server.Close()
	})
}
