// Package server owns ssoosshd-nss's sockets: the userdb socket, from
// socket activation (LISTEN_FDS) or bound itself for development and
// tests, its accept loop with the timing rules, peer credentials and the
// sshd check, and the admin socket the CLI talks to. Part 3 of
// docs/proposals/nss-directory ("Userdb service", "Timing", "Admin
// interface and CLI"). It also holds the host checks status reports.
package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// Timing defaults (README U4, U5).
const (
	// DefaultReadTimeout is how long a connection may take to send a call,
	// the first one or the next one, before it is closed.
	DefaultReadTimeout = time.Second
	// writeGrace is how long past the answer deadline a reply may take to
	// write before the connection is given up, for a peer that stops
	// reading.
	writeGrace = time.Second
	// maxAnswerMargin is the most the answer deadline is shortened by, to
	// leave time to write the reply inside it.
	maxAnswerMargin = 100 * time.Millisecond
)

// Connection cap (socket_max_connections).
const (
	// maxRefusing bounds the connections over the cap being answered
	// ServiceNotAvailable at once. Past it a connection is closed
	// unanswered, which its caller sees as the same not-found, so the
	// refusals themselves cannot pile up.
	maxRefusing = 64
	// refuseTimeout is how long a connection over the cap is given to send
	// the call it is refused. A caller writes its call as soon as it
	// connects, so this is short: refusing is meant to be prompt.
	refuseTimeout = 250 * time.Millisecond
	// overCapLogInterval is how often refusals are logged, as one line
	// with a count, so a flood never writes a line per connection.
	overCapLogInterval = time.Minute
	// acceptBackoffMin and acceptBackoffMax bound the wait after Accept
	// runs out of descriptors or memory, as net/http waits.
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// Options configure a Server.
type Options struct {
	// Daemon answers the calls.
	Daemon *userdb.Daemon
	// Peers reads peer credentials.
	Peers PeerInspector
	// SSHDUnits are the units that make a uid 0 peer sshd.
	SSHDUnits []string
	// AnswerDeadline is socket_answer_deadline.
	AnswerDeadline time.Duration
	// ReadTimeout is DefaultReadTimeout when zero.
	ReadTimeout time.Duration
	// MaxConnections is socket_max_connections, its default when zero.
	MaxConnections int
	// IdleTimeout is socket_idle_timeout, its default when zero.
	IdleTimeout time.Duration
	// Now tells the time for the over-cap log's interval; time.Now when
	// nil.
	Now func() time.Time
	// Log receives connection-level events.
	Log *slog.Logger
}

// Server serves the userdb and admin sockets.
type Server struct {
	daemon      *userdb.Daemon
	peers       PeerInspector
	sshd        sshdCheck
	answerWait  time.Duration
	answerLimit time.Duration
	readTimeout time.Duration
	idleTimeout time.Duration
	slots       *connSlots
	refusing    chan struct{}
	overCap     *overCapLog
	log         *slog.Logger
	euid        uint32
	// since measures how long a connection waited for a call, for the
	// idle budget: time.Since, which a test replaces to spend the budget
	// in one wait (a call read only when the wait used up the budget is
	// otherwise a matter of wall-clock luck).
	since func(time.Time) time.Duration
}

// New returns a Server.
func New(opts Options) *Server {
	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}
	maxConns := opts.MaxConnections
	if maxConns <= 0 {
		maxConns = config.DefaultSocketMaxConnections
	}
	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = config.DefaultSocketIdleTimeout
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	slots := newConnSlots(maxConns)
	// Wait for upstream a little less than the deadline, so the reply is
	// written inside it.
	margin := min(maxAnswerMargin, opts.AnswerDeadline/10)
	return &Server{
		daemon:      opts.Daemon,
		peers:       opts.Peers,
		sshd:        sshdCheck{units: opts.SSHDUnits, peers: opts.Peers, log: opts.Log},
		answerWait:  opts.AnswerDeadline - margin,
		answerLimit: opts.AnswerDeadline,
		readTimeout: readTimeout,
		idleTimeout: idleTimeout,
		slots:       slots,
		refusing:    make(chan struct{}, maxRefusing),
		overCap:     &overCapLog{now: now, log: opts.Log, slots: slots},
		log:         opts.Log,
		euid:        uint32(os.Geteuid()), //nolint:gosec // G115: a uid is never negative.
		since:       time.Since,
	}
}

// Serve accepts userdb connections on l until ctx ends, then closes l and
// waits for the connections in progress. The accept loop never waits on
// upstream or on a peer: each connection admitted under the cap gets a
// goroutine of its own, one over the cap is answered ServiceNotAvailable,
// and a lookup the upstream cap refuses is answered ServiceNotAvailable at
// once.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	return s.acceptLoop(ctx, l, s.admit)
}

// acceptLoop is the accept loop both sockets share. dispatch decides what
// becomes of each connection: the func it returns runs in a goroutine the
// loop waits for, and a nil one means dispatch has dealt with it.
func (s *Server) acceptLoop(ctx context.Context, l net.Listener, dispatch func(context.Context, net.Conn) func()) error {
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()

	var wg sync.WaitGroup
	defer wg.Wait()
	backoff := time.Duration(0)
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				// not covered: Accept on a Unix listener times out only
				// with a deadline set, and this loop sets none.
				continue
			}
			if !acceptRetryable(err) {
				return err
			}
			// Out of descriptors or memory: a crash here would stall
			// every lookup on the host behind the socket, so wait and
			// accept again, as net/http does.
			backoff = min(max(2*backoff, acceptBackoffMin), acceptBackoffMax)
			s.log.Warn("cannot accept a connection; retrying", "error", err, "retry_in", backoff.String())
			if !sleepContext(ctx, backoff) {
				return nil
			}
			continue
		}
		backoff = 0
		if run := dispatch(ctx, conn); run != nil {
			wg.Go(run)
		}
	}
}

// acceptRetryable reports whether an Accept error is a resource shortage
// that passes, rather than a listener that is gone.
func acceptRetryable(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.ENOMEM)
}

// sleepContext waits d or until ctx ends, and reports whether d passed.
func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// admit decides a userdb connection's fate in the accept loop: read the
// peer's credentials (SO_PEERCRED, which never blocks), then serve it
// under the cap, refuse it with ServiceNotAvailable, or, when too many
// refusals are already running, close it unanswered.
func (s *Server) admit(ctx context.Context, conn net.Conn) func() {
	cred, err := s.peers.Credentials(conn)
	if err != nil {
		s.log.Warn("cannot read a userdb peer's credentials; closing", "error", err)
		_ = conn.Close()
		return nil
	}
	if s.slots.acquire(cred.UID) {
		return func() {
			defer s.slots.release(cred.UID)
			s.handle(ctx, conn, cred)
		}
	}
	select {
	case s.refusing <- struct{}{}:
		s.overCap.note(cred.UID, true)
		return func() {
			defer func() { <-s.refusing }()
			s.refuse(conn)
		}
	default:
		s.overCap.note(cred.UID, false)
		_ = conn.Close()
		return nil
	}
}

// refuse answers the one call a connection over the cap sends with
// ServiceNotAvailable, as an upstream lookup over its own cap is answered,
// and closes it. The call is read first: closing a socket with a call
// still unread resets the connection, and the caller might never see the
// answer.
func (s *Server) refuse(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(refuseTimeout)); err != nil {
		// not covered: setting a deadline fails only on a closed
		// connection.
		return
	}
	vc := varlink.NewServerConn(conn)
	call, err := vc.ReadCall()
	if err != nil || call.Oneway {
		return
	}
	if err := vc.ReplyError(userdb.ErrorServiceNotAvailable, nil); err != nil {
		// The caller left; it sees the same not-found.
		s.log.Debug("cannot refuse a userdb peer", "error", err)
	}
}

// handle serves one userdb connection: several calls in sequence, each
// read within the read timeout and answered within the answer deadline,
// until EOF, a framing error, or the connection has waited for calls for
// socket_idle_timeout in all. The idle budget only runs while no call is
// in progress, so a call being answered (a slow upstream fetch, a
// GetMemberships stream) is never cut by it, and only runs down: sending
// a call now and then does not keep a connection open for ever.
func (s *Server) handle(ctx context.Context, conn net.Conn, cred Credentials) {
	defer func() { _ = conn.Close() }()

	peer := userdb.Peer{UID: cred.UID, SSHD: s.sshd.isSSHD(cred)}
	vc := varlink.NewServerConn(conn)
	idleLeft := s.idleTimeout

	for {
		if ctx.Err() != nil {
			// Shutting down: the last call on this connection was
			// answered, and no new one starts here. The peer's next call
			// goes to the socket, which systemd holds for the next daemon.
			return
		}
		wait := min(s.readTimeout, idleLeft)
		if wait <= 0 {
			s.log.Debug("closing a userdb connection that spent socket_idle_timeout waiting for calls",
				"pid", cred.PID, "uid", cred.UID)
			return
		}
		waitStart := time.Now()
		if err := conn.SetReadDeadline(waitStart.Add(wait)); err != nil {
			// not covered: setting a deadline fails only on a closed
			// connection.
			return
		}
		call, err := vc.ReadCall()
		idleLeft -= s.since(waitStart)
		if err != nil {
			s.logReadError(err, cred)
			return
		}
		if err := conn.SetWriteDeadline(time.Now().Add(s.answerLimit + writeGrace)); err != nil {
			// not covered: as above.
			return
		}
		// A call already read is answered even when shutdown begins
		// during it, still within the answer deadline: cancelling it with
		// the server would turn a lookup in flight across a restart into
		// a not-found for the caller.
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.answerWait)
		err = s.daemon.Handle(callCtx, call, peer, vc)
		cancel()
		if err != nil {
			s.log.Debug("cannot answer a userdb peer; closing", "pid", cred.PID, "error", err)
			return
		}
	}
}

// logReadError logs why a connection's read ended. EOF and idleness are
// the normal ends of a connection and are not logged.
func (s *Server) logReadError(err error, cred Credentials) {
	var ne net.Error
	if errors.Is(err, io.EOF) || (errors.As(err, &ne) && ne.Timeout()) {
		return
	}
	s.log.Debug("closing a userdb connection", "pid", cred.PID, "uid", cred.UID, "error", err)
}

// connSlots is the userdb connection cap: a counting semaphore of limit
// slots, a quarter of which only uid 0 peers may take. sshd, logind, sudo
// and su (setuid, so uid 0 at connect) and systemd-userdbd's workers
// connect as uid 0, so a flood from an unprivileged user can fill only
// the rest, and logins and sudo keep resolving directory users.
type connSlots struct {
	mu           sync.Mutex
	limit        int
	unprivLimit  int
	total        int
	unprivileged int
}

// newConnSlots returns a cap of limit connections, limit/4 kept for uid 0.
func newConnSlots(limit int) *connSlots {
	return &connSlots{limit: limit, unprivLimit: limit - limit/4}
}

// acquire takes a slot for a peer of uid, and reports whether one was
// free.
func (c *connSlots) acquire(uid uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.total >= c.limit || (uid != 0 && c.unprivileged >= c.unprivLimit) {
		return false
	}
	c.total++
	if uid != 0 {
		c.unprivileged++
	}
	return true
}

// release gives back the slot a peer of uid took.
func (c *connSlots) release(uid uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total--
	if uid != 0 {
		c.unprivileged--
	}
}

// overCapLog logs refused connections at most once per
// overCapLogInterval: the first refusal at once, then a line with the
// count of those since the previous line.
type overCapLog struct {
	now   func() time.Time
	log   *slog.Logger
	slots *connSlots

	mu         sync.Mutex
	last       time.Time
	refused    int
	unanswered int
}

// note counts one refusal of a peer of uid; answered is false for one
// closed without an answer because too many refusals were running.
func (o *overCapLog) note(uid uint32, answered bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refused++
	if !answered {
		o.unanswered++
	}
	now := o.now()
	if !o.last.IsZero() && now.Sub(o.last) < overCapLogInterval {
		return
	}
	o.log.Warn("userdb connections over socket_max_connections refused; their lookups were answered ServiceNotAvailable, which callers see as not found",
		"refused", o.refused, "unanswered", o.unanswered, "last_uid", uid,
		"max_connections", o.slots.limit, "reserved_for_uid_0", o.slots.limit-o.slots.unprivLimit)
	o.last, o.refused, o.unanswered = now, 0, 0
}
