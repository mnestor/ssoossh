package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// The admin socket carries one JSON request line and one JSON response
// line per connection. It is internal to this repo: the CLI and the
// daemon ship together, so it has no version.

// Admin operations.
const (
	// AdminStatus returns the daemon's status.
	AdminStatus = "status"
	// AdminInvalidate marks cache entries stale.
	AdminInvalidate = "invalidate"
	// AdminReload re-reads the host files, after an override edit.
	AdminReload = "reload"
)

// adminTimeout bounds one admin exchange on either side.
const adminTimeout = 5 * time.Second

// maxAdminRequest bounds an admin request line.
const maxAdminRequest = 4096

// AdminRequest is one admin call.
type AdminRequest struct {
	// Op is one of the Admin* operations.
	Op string `json:"op"`
	// Users, Groups and Name select what AdminInvalidate marks.
	Users  bool   `json:"users,omitempty"`
	Groups bool   `json:"groups,omitempty"`
	Name   string `json:"name,omitempty"`
}

// AdminResponse is the answer to one admin call.
type AdminResponse struct {
	// Error is set when the call failed.
	Error string `json:"error,omitempty"`
	// Status answers AdminStatus.
	Status *userdb.Status `json:"status,omitempty"`
	// Marked answers AdminInvalidate: how many entries were marked.
	Marked int `json:"marked"`
}

// ListenAdmin binds the admin socket at path, mode 0600, creating its
// directory 0700 when it does not exist (systemd's RuntimeDirectory= makes
// /run/ssoossh-nss itself).
func ListenAdmin(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create the admin socket directory: %w", err)
	}
	return Listen(path, 0o600)
}

// ServeAdmin accepts admin connections on l until ctx ends. The admin
// socket is root-only (0600 in a 0700 directory), so it has no connection
// cap of its own.
func (s *Server) ServeAdmin(ctx context.Context, l net.Listener) error {
	return s.acceptLoop(ctx, l, func(ctx context.Context, conn net.Conn) func() {
		return func() { s.handleAdmin(ctx, conn) }
	})
}

// handleAdmin serves one admin connection. Only root, or the uid the
// daemon runs as (a developer's or a test's), may use it: the socket's
// mode already says so, and the peer check holds if the mode is ever
// loosened.
func (s *Server) handleAdmin(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(adminTimeout)); err != nil {
		// not covered: setting a deadline fails only on a closed
		// connection.
		return
	}
	cred, err := s.peers.Credentials(conn)
	if err != nil || (cred.UID != 0 && cred.UID != s.euid) {
		s.log.Warn("refusing an admin connection", "uid", cred.UID, "error", err)
		return
	}

	var req AdminRequest
	line, err := bufio.NewReaderSize(conn, maxAdminRequest).ReadSlice('\n')
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	resp := AdminResponse{}
	if err != nil {
		resp.Error = "malformed admin request"
	} else {
		resp = s.admin(ctx, req)
	}
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		s.log.Debug("cannot answer an admin call", "error", err)
	}
}

// admin runs one admin operation.
func (s *Server) admin(ctx context.Context, req AdminRequest) AdminResponse {
	switch req.Op {
	case AdminStatus:
		st := s.daemon.Status(ctx)
		return AdminResponse{Status: &st}
	case AdminInvalidate:
		if !req.Users && !req.Groups {
			return AdminResponse{Error: "invalidate needs users, groups or both"}
		}
		n, err := s.daemon.Invalidate(ctx, cache.Selector{Users: req.Users, Groups: req.Groups, Name: req.Name})
		if err != nil {
			return AdminResponse{Error: err.Error(), Marked: n}
		}
		s.log.Info("cache invalidated", "users", req.Users, "groups", req.Groups, "name", req.Name, "marked", n)
		return AdminResponse{Marked: n}
	case AdminReload:
		s.daemon.ReloadPolicy()
		return AdminResponse{}
	default:
		return AdminResponse{Error: fmt.Sprintf("unknown admin operation %q", req.Op)}
	}
}

// ErrAdminUnavailable is an admin socket nobody answers on: the daemon is
// not running, or runs with another admin socket.
var ErrAdminUnavailable = errors.New("the daemon's admin socket is not answering")

// CallAdmin sends one admin request to the socket at path and returns the
// answer. An error the daemon reports comes back as an error too.
func CallAdmin(ctx context.Context, path string, req AdminRequest) (AdminResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return AdminResponse{}, fmt.Errorf("%w: %w", ErrAdminUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			// not covered: setting a deadline on a connection just dialled
			// cannot fail.
			return AdminResponse{}, fmt.Errorf("admin call: %w", err)
		}
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return AdminResponse{}, fmt.Errorf("admin call: %w", err)
	}
	var resp AdminResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return AdminResponse{}, fmt.Errorf("admin call: the daemon closed the connection without answering: %w", err)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
