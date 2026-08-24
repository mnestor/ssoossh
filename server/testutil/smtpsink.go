// Package testutil holds test helpers shared across server subpackages
// (not imported by non-test code).
package testutil

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// SMTPSinkOptions configures what the sink advertises and demands.
type SMTPSinkOptions struct {
	// STARTTLS advertises STARTTLS and upgrades the connection when the
	// client asks. The certificate is self-signed, so a client verifying
	// it needs the sink's RootCAs.
	STARTTLS bool

	// ImplicitTLS wraps the listener in TLS instead, for the SMTPS case.
	ImplicitTLS bool

	// Username and Password, when set, advertise AUTH PLAIN and LOGIN and
	// reject any credentials that do not match.
	Username string
	Password string

	// RejectData makes the sink refuse the message body with a permanent
	// error, for exercising the delivery-failure path.
	RejectData bool
}

// SMTPMessage is one message the sink accepted.
type SMTPMessage struct {
	From string
	To   []string
	Data string
}

// Header returns the first value of the named header in the message data,
// unfolded onto one line. Returns "" when the header is absent.
func (m SMTPMessage) Header(name string) string {
	prefix := strings.ToLower(name) + ":"
	lines := strings.Split(strings.ReplaceAll(m.Data, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.ToLower(line), prefix) {
			continue
		}
		value := strings.TrimSpace(line[len(prefix):])
		// Continuation lines of a folded header begin with whitespace.
		for _, next := range lines[i+1:] {
			if next == "" || (next[0] != ' ' && next[0] != '\t') {
				break
			}
			value += " " + strings.TrimSpace(next)
		}
		return value
	}
	return ""
}

// SMTPSink is an in-process SMTP server that accepts mail and remembers it.
// It speaks enough of RFC 5321 for go-mail: EHLO, the optional STARTTLS and
// AUTH extensions, MAIL/RCPT/DATA, and the NOOP and RSET go-mail issues
// around a send.
//
// It exists so mail delivery can be tested against a real socket and a real
// SMTP conversation rather than a stubbed Sender — the parts most likely to
// be wrong (TLS policy, authentication, what actually lands in the headers)
// are exactly the parts a stub cannot check.
type SMTPSink struct {
	// Addr is the host:port the sink is listening on.
	Addr string
	// Host and Port are Addr split, for configuration that wants them apart.
	Host string
	Port int
	// RootCAs verifies the sink's self-signed certificate.
	RootCAs *x509.CertPool
	// CertPEM is the same certificate in PEM form, for configuration that
	// takes a ca_file path rather than a pool.
	CertPEM []byte

	listener net.Listener
	opts     SMTPSinkOptions

	mu       sync.Mutex
	messages []SMTPMessage
	authOK   bool
}

// NewSMTPSink starts a sink on a loopback port, closed via t.Cleanup.
func NewSMTPSink(t *testing.T, opts SMTPSinkOptions) *SMTPSink {
	t.Helper()

	s := &SMTPSink{opts: opts}

	var tlsConfig *tls.Config
	if opts.STARTTLS || opts.ImplicitTLS {
		tlsConfig, s.RootCAs, s.CertPEM = selfSignedTLS(t)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("smtp sink: listen: %v", err)
	}
	if opts.ImplicitTLS {
		listener = tls.NewListener(listener, tlsConfig)
	}
	s.listener = listener
	s.Addr = listener.Addr().String()
	host, portStr, err := net.SplitHostPort(s.Addr)
	if err != nil {
		t.Fatalf("smtp sink: split listen address %q: %v", s.Addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("smtp sink: parse listen port %q: %v", portStr, err)
	}
	s.Host, s.Port = host, port

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, tlsConfig)
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	return s
}

// Messages returns the messages accepted so far.
func (s *SMTPSink) Messages() []SMTPMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SMTPMessage, len(s.messages))
	copy(out, s.messages)
	return out
}

// Authenticated reports whether a client successfully authenticated.
func (s *SMTPSink) Authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authOK
}

// WaitForMessages blocks until at least n messages have been accepted, or
// the timeout elapses. Delivery is asynchronous by design, so a test that
// triggers a notification has to wait for one rather than assert
// immediately.
func (s *SMTPSink) WaitForMessages(t *testing.T, n int, timeout time.Duration) []SMTPMessage {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if msgs := s.Messages(); len(msgs) >= n {
			return msgs
		}
		if time.Now().After(deadline) {
			t.Fatalf("smtp sink: waited %s for %d message(s), got %d", timeout, n, len(s.Messages()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// session is one client connection mid-conversation: the transport, the
// buffered reader/writer over it, and the envelope being assembled.
//
// Extracted from serve because STARTTLS replaces all three at once — the
// connection is wrapped, and the old buffered reader must be discarded
// with it or buffered plaintext would be replayed over the TLS channel.
type session struct {
	sink     *SMTPSink
	conn     net.Conn
	rw       *bufio.ReadWriter
	envelope SMTPMessage
}

// reply writes one CRLF-terminated response line.
func (s *session) reply(format string, args ...any) {
	_, _ = fmt.Fprintf(s.rw, format+"\r\n", args...)
	_ = s.rw.Flush()
}

// readLine reads one CRLF-terminated command line, without the terminator.
func (s *session) readLine() (string, error) {
	line, err := s.rw.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// rebind points the session at conn and starts a fresh buffered pair over
// it, discarding whatever the previous reader had buffered.
//
// A deadline that will not set means the connection is already gone, so
// the error is returned rather than ignored: the caller closes out instead
// of talking to a socket that cannot answer.
func (s *session) rebind(conn net.Conn) error {
	if err := conn.SetDeadline(time.Now().Add(sinkDeadline)); err != nil {
		return err
	}
	s.conn = conn
	s.rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	return nil
}

// sinkDeadline bounds one connection. Generous: a test that stalls should
// fail on its own timeout with its own message, not on this.
const sinkDeadline = 30 * time.Second

// serve handles one client connection.
func (s *SMTPSink) serve(conn net.Conn, tlsConfig *tls.Config) {
	defer func() { _ = conn.Close() }()

	sess := &session{sink: s}
	if err := sess.rebind(conn); err != nil {
		return
	}
	sess.reply("220 smtpsink ESMTP ready")

	for {
		line, err := sess.readLine()
		if err != nil {
			return
		}
		verb, rest, _ := strings.Cut(line, " ")
		if done := sess.dispatch(strings.ToUpper(verb), rest, tlsConfig); done {
			return
		}
	}
}

// dispatch handles one command, reporting whether the connection is
// finished.
func (s *session) dispatch(verb, rest string, tlsConfig *tls.Config) (done bool) {
	switch verb {
	case "EHLO":
		s.greetExtended(tlsConfig)
	case "HELO":
		s.reply("250 smtpsink")
	case "STARTTLS":
		return s.startTLS(tlsConfig)
	case "AUTH":
		s.sink.handleAuth(s, rest)
	case "MAIL":
		s.envelope = SMTPMessage{From: addressIn(rest)}
		s.reply("250 sender ok")
	case "RCPT":
		s.envelope.To = append(s.envelope.To, addressIn(rest))
		s.reply("250 recipient ok")
	case "DATA":
		return s.acceptData()
	case "RSET":
		s.envelope = SMTPMessage{}
		s.reply("250 reset")
	case "NOOP":
		s.reply("250 ok")
	case "QUIT":
		s.reply("221 bye")
		return true
	default:
		s.reply("500 unrecognized command")
	}
	return false
}

// greetExtended answers EHLO with the extensions this sink was configured
// to advertise.
func (s *session) greetExtended(tlsConfig *tls.Config) {
	s.reply("250-smtpsink")
	if s.sink.opts.STARTTLS && tlsConfig != nil {
		if _, alreadyTLS := s.conn.(*tls.Conn); !alreadyTLS {
			s.reply("250-STARTTLS")
		}
	}
	if s.sink.opts.Username != "" {
		s.reply("250-AUTH PLAIN LOGIN")
	}
	s.reply("250 8BITMIME")
}

// startTLS upgrades the connection, reporting whether the session ended.
func (s *session) startTLS(tlsConfig *tls.Config) (done bool) {
	if !s.sink.opts.STARTTLS || tlsConfig == nil {
		s.reply("502 STARTTLS not supported")
		return false
	}

	s.reply("220 ready to start TLS")
	tlsConn := tls.Server(s.conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		return true
	}
	if err := s.rebind(tlsConn); err != nil {
		return true
	}
	return false
}

// acceptData reads the message body and records it, reporting whether the
// session ended.
func (s *session) acceptData() (done bool) {
	if s.sink.opts.RejectData {
		s.reply("554 message rejected")
		return false
	}

	s.reply("354 end with <CRLF>.<CRLF>")
	body, err := readDotStuffed(s.rw)
	if err != nil {
		return true
	}

	s.envelope.Data = body
	s.sink.mu.Lock()
	s.sink.messages = append(s.sink.messages, s.envelope)
	s.sink.mu.Unlock()
	s.envelope = SMTPMessage{}
	s.reply("250 queued")
	return false
}

// handleAuth implements AUTH PLAIN and AUTH LOGIN against the configured
// credentials.
func (s *SMTPSink) handleAuth(sess *session, rest string) {
	if s.opts.Username == "" {
		sess.reply("503 authentication not enabled")
		return
	}

	mechanism, initial, _ := strings.Cut(rest, " ")

	var user, pass string
	switch strings.ToUpper(mechanism) {
	case "PLAIN":
		var ok bool
		user, pass, ok = sess.readPlainCredentials(initial)
		if !ok {
			return
		}
	case "LOGIN":
		var ok bool
		user, pass, ok = sess.readLoginCredentials()
		if !ok {
			return
		}
	default:
		sess.reply("504 unsupported mechanism")
		return
	}

	if user != s.opts.Username || pass != s.opts.Password {
		sess.reply("535 bad credentials")
		return
	}
	s.mu.Lock()
	s.authOK = true
	s.mu.Unlock()
	sess.reply("235 authenticated")
}

// readPlainCredentials decodes an AUTH PLAIN payload, prompting for it when
// the client did not send it inline.
func (s *session) readPlainCredentials(initial string) (user, pass string, ok bool) {
	if initial == "" {
		s.reply("334 ")
		line, err := s.readLine()
		if err != nil {
			return "", "", false
		}
		initial = line
	}

	decoded, err := base64.StdEncoding.DecodeString(initial)
	if err != nil {
		s.reply("535 bad credentials")
		return "", "", false
	}

	// authzid\0authcid\0password
	parts := strings.Split(string(decoded), "\x00")
	if len(parts) != 3 {
		s.reply("535 bad credentials")
		return "", "", false
	}
	return parts[1], parts[2], true
}

// readLoginCredentials runs the two-prompt AUTH LOGIN exchange.
func (s *session) readLoginCredentials() (user, pass string, ok bool) {
	s.reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
	userB64, err := s.readLine()
	if err != nil {
		return "", "", false
	}
	s.reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
	passB64, err := s.readLine()
	if err != nil {
		return "", "", false
	}

	userBytes, userErr := base64.StdEncoding.DecodeString(userB64)
	passBytes, passErr := base64.StdEncoding.DecodeString(passB64)
	if userErr != nil || passErr != nil {
		s.reply("535 bad credentials")
		return "", "", false
	}
	return string(userBytes), string(passBytes), true
}

// readDotStuffed reads a DATA body up to the lone-dot terminator, undoing
// the leading-dot escaping the sender applied.
func readDotStuffed(rw *bufio.ReadWriter) (string, error) {
	var body strings.Builder
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return body.String(), nil
			}
			return "", err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			return body.String(), nil
		}
		if strings.HasPrefix(trimmed, "..") {
			trimmed = trimmed[1:]
		}
		body.WriteString(trimmed)
		body.WriteString("\n")
	}
}

// addressIn pulls the address out of a "FROM:<a@b>" or "TO:<a@b>" argument.
func addressIn(arg string) string {
	start := strings.Index(arg, "<")
	end := strings.Index(arg, ">")
	if start >= 0 && end > start {
		return arg[start+1 : end]
	}
	_, after, found := strings.Cut(arg, ":")
	if found {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(arg)
}

// CAFile writes the sink's certificate to a PEM file and returns its path,
// for configuration that verifies against a bundle on disk.
func (s *SMTPSink) CAFile(t *testing.T) string {
	t.Helper()

	if len(s.CertPEM) == 0 {
		t.Fatal("smtp sink: no certificate (the sink was started without TLS)")
	}
	path := filepath.Join(t.TempDir(), "smtpsink-ca.pem")
	if err := os.WriteFile(path, s.CertPEM, 0o600); err != nil {
		t.Fatalf("smtp sink: write ca file: %v", err)
	}
	return path
}

// selfSignedTLS builds a throwaway certificate for 127.0.0.1 and localhost,
// plus the pool and the PEM bytes that verify it.
func selfSignedTLS(t *testing.T) (*tls.Config, *x509.CertPool, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("smtp sink: generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "smtpsink"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost", "smtpsink"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("smtp sink: create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("smtp sink: parse certificate: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		MinVersion:   tls.VersionTLS12,
	}, pool, pemBytes
}
