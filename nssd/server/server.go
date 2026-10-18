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
	"time"

	"github.com/mnestor/ssoossh/internal/varlink"
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
	log         *slog.Logger
	euid        uint32
}

// New returns a Server.
func New(opts Options) *Server {
	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}
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
		log:         opts.Log,
		euid:        uint32(os.Geteuid()), //nolint:gosec // G115: a uid is never negative.
	}
}

// Serve accepts userdb connections on l until ctx ends, then closes l and
// waits for the connections in progress. The accept loop never waits on
// upstream: each connection gets a goroutine of its own, and a lookup the
// upstream cap refuses is answered ServiceNotAvailable at once.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	return s.acceptLoop(ctx, l, s.handle)
}

// acceptLoop is the accept loop both sockets share.
func (s *Server) acceptLoop(ctx context.Context, l net.Listener, handle func(context.Context, net.Conn)) error {
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()

	var wg sync.WaitGroup
	defer wg.Wait()
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
			return err
		}
		wg.Go(func() { handle(ctx, conn) })
	}
}

// handle serves one userdb connection: several calls in sequence, each
// read within the read timeout and answered within the answer deadline,
// until EOF, idleness or a framing error.
func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	cred, err := s.peers.Credentials(conn)
	if err != nil {
		s.log.Warn("cannot read a userdb peer's credentials; closing", "error", err)
		return
	}
	peer := userdb.Peer{UID: cred.UID, SSHD: s.sshd.isSSHD(cred)}
	vc := varlink.NewServerConn(conn)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
			// not covered: setting a deadline fails only on a closed
			// connection.
			return
		}
		call, err := vc.ReadCall()
		if err != nil {
			s.logReadError(err, cred)
			return
		}
		if err := conn.SetWriteDeadline(time.Now().Add(s.answerLimit + writeGrace)); err != nil {
			// not covered: as above.
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, s.answerWait)
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
