//go:build e2e || resilience || load

package harness

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Agent is a private ssh-agent started for one test — never the ambient
// SSH_AUTH_SOCK, since the logout assertion (removing exactly ssoossh's own
// certificates and leaving everything else) is meaningless against a shared
// one.
type Agent struct {
	// Socket is the path clients reach this agent at, i.e. what to set
	// SSH_AUTH_SOCK to.
	Socket string

	cmd *exec.Cmd
}

// StartAgent launches `ssh-agent -D -a <socket>` (foreground, bound to a
// harness-chosen socket rather than one it picks itself) and registers
// teardown via t.Cleanup.
func StartAgent(t *testing.T) *Agent {
	t.Helper()

	socket := filepath.Join(t.TempDir(), "agent.sock")

	cmd := exec.Command("ssh-agent", "-D", "-a", socket)
	var stderr lockedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start ssh-agent: %v", err)
	}

	a := &Agent{Socket: socket, cmd: cmd}
	t.Cleanup(func() {
		if a.cmd.Process != nil {
			_ = a.cmd.Process.Kill() //nolint:errcheck // best-effort teardown
			_ = a.cmd.Wait()         //nolint:errcheck // only reaped to release resources, exit status is irrelevant on teardown
		}
	})

	waitForSocket(t, socket, &stderr)

	return a
}

func waitForSocket(t *testing.T, path string, stderr *lockedBuffer) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("harness: ssh-agent socket %s did not appear before deadline\nstderr:\n%s", path, stderr.String())
}

// dial connects to the agent's socket and wraps it as an
// golang.org/x/crypto/ssh/agent client — dialed directly rather than
// through the product's own agent package, keeping the harness a true
// black-box client of the real ssh-agent protocol.
func (a *Agent) dial(t *testing.T) agent.ExtendedAgent {
	t.Helper()

	conn, err := net.Dial("unix", a.Socket)
	if err != nil {
		t.Fatalf("harness: failed to dial agent socket %s: %v", a.Socket, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return agent.NewClient(conn)
}

// Certificates returns every ssh.Certificate identity currently loaded in
// the agent, signed or not — callers filter by CA themselves.
func (a *Agent) Certificates(t *testing.T) []*ssh.Certificate {
	t.Helper()

	keys, err := a.dial(t).List()
	if err != nil {
		t.Fatalf("harness: failed to list agent identities: %v", err)
	}

	var certs []*ssh.Certificate
	for _, k := range keys {
		pub, err := ssh.ParsePublicKey(k.Marshal())
		if err != nil {
			continue
		}
		if cert, ok := pub.(*ssh.Certificate); ok {
			certs = append(certs, cert)
		}
	}
	return certs
}

// AllKeys returns every identity loaded in the agent, certificates and
// plain keys alike — for asserting that an unrelated key survives a
// `ssh logout`.
func (a *Agent) AllKeys(t *testing.T) []ssh.PublicKey {
	t.Helper()

	keys, err := a.dial(t).List()
	if err != nil {
		t.Fatalf("harness: failed to list agent identities: %v", err)
	}

	var pubs []ssh.PublicKey
	for _, k := range keys {
		pub, err := ssh.ParsePublicKey(k.Marshal())
		if err != nil {
			continue
		}
		pubs = append(pubs, pub)
	}
	return pubs
}

// AddUnrelatedKey loads a freshly generated plain key (no certificate) into
// the agent under comment, for tests asserting `ssh logout` leaves it alone.
func (a *Agent) AddUnrelatedKey(t *testing.T, comment string) ssh.PublicKey {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("harness: failed to generate an unrelated key: %v", err)
	}

	if err := a.dial(t).Add(agent.AddedKey{PrivateKey: priv, Comment: comment}); err != nil {
		t.Fatalf("harness: failed to add an unrelated key to the agent: %v", err)
	}

	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("harness: failed to derive public key: %v", err)
	}
	return pub
}

// ListAgentKeys returns all keys in the agent for asserting the probe key
// was not left behind after a preflight check.
func ListAgentKeys(t *testing.T, a *Agent) ([]ssh.PublicKey, error) {
	t.Helper()
	return a.AllKeys(t), nil
}

// StartBrokenAgent starts a socket server that accepts connections and
// properly handles the SSH agent protocol for List (returning an empty list),
// but fails on Add operations. This is used to test preflight fallback behavior
// when an agent is reachable but cannot store keys.
// Returns the socket path that should be used as SSH_AUTH_SOCK.
func StartBrokenAgent(t *testing.T) string {
	t.Helper()

	socket := filepath.Join(t.TempDir(), "broken-agent.sock")

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("harness: failed to listen on %s: %v", socket, err)
	}

	// Start the server in a goroutine.
	// It accepts connections and handles the SSH agent protocol.
	// For List(), it returns an empty key list (agent looks empty).
	// For Add(), it closes the connection to fail the operation.
	go func() {
		defer func() {
			_ = listener.Close() //nolint:errcheck
		}()

		for {
			conn, err := listener.Accept()
			if err != nil {
				return // Listener closed
			}
			// Handle the connection in a goroutine.
			go handleBrokenAgentConn(conn)
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close() //nolint:errcheck
	})

	return socket
}

// handleBrokenAgentConn handles one connection to the broken agent.
// It responds to SSH_AGENTC_REQUEST_IDENTITIES (11) with an empty list,
// and closes the connection on any Add or other operation.
func handleBrokenAgentConn(conn net.Conn) {
	defer func() {
		_ = conn.Close() //nolint:errcheck
	}()

	for {
		// Read message length (4 bytes, big-endian).
		var msgLen uint32
		if err := binary.Read(conn, binary.BigEndian, &msgLen); err != nil {
			if err != io.EOF {
				_ = err //nolint:errcheck
			}
			return // Connection closed or error
		}

		if msgLen > 256*1024 { // Safety limit
			return
		}

		// Read message type and payload.
		msg := make([]byte, msgLen)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return // Connection closed or error
		}

		if len(msg) == 0 {
			return // Invalid message
		}

		msgType := msg[0]

		// SSH_AGENTC_REQUEST_IDENTITIES is 11 (list keys request).
		if msgType == 11 {
			// Respond with SSH_AGENT_IDENTITIES_ANSWER (12) containing 0 keys.
			// Format: 1 byte for type + 4 bytes for number of keys (0).
			response := []byte{12, 0, 0, 0, 0} // Type 12, 0 keys (as uint32)
			if err := binary.Write(conn, binary.BigEndian, uint32(len(response))); err != nil {
				return
			}
			if _, err := conn.Write(response); err != nil {
				return
			}
			continue // Process next message
		}

		// For any Add request or other operation, close the connection
		// to simulate a broken agent. This will cause the client's Add
		// operation to fail.
		return
	}
}
