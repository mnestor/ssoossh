// Command fakeupstream stands in for ssoosshd in the ssoossh-nss
// integration tests (test/nss) and in the SELinux test on a real host. It
// serves the four /api/nss lookups over TLS 1.3 with the Part 1 wire types
// from internal/apitypes, a fixed directory and a fleet key it insists on,
// and logs one line per request so a test can tell when the daemon went
// upstream.
//
// At start it makes a throwaway CA and a server certificate for
// 127.0.0.1, ::1, localhost and every -host, and writes into -out:
//
//	ca.pem    the CA, for nss.yaml's tls_ca_file
//	spki.txt  the server key's SPKI pin, for nss.yaml's tls_spki_pin
//	nss.key   the fleet key, in the daemon's two-line format, mode 0600
//
// Test code only: nothing here is shipped or reached by a production
// build.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// The fleet key the fake accepts and writes to nss.key.
const (
	keyID     = "nss_integration"
	keySecret = "ssoossh_nss_integrationsecret"
)

// directory is the fake's fixed directory. alice is active with a
// supplementary group, bob is disabled, and devs has alice as its member.
// alice is also in users, gid 60200, named like the local users group
// (gid 100 on Debian) to exercise the gid rule's name collision, and in
// oldstaff, gid 500, which min_id refuses.
type directory struct {
	users  []apitypes.NSSUserResponse
	groups []apitypes.NSSGroupResponse
}

// newDirectory returns the fixed directory. Ids sit at 60001 and up, far
// from any local account a test host or container has.
func newDirectory() directory {
	str := func(s string) *string { return &s }
	return directory{
		users: []apitypes.NSSUserResponse{
			{
				Name: "alice", UID: 60001, GID: 60001,
				Groups: []apitypes.NSSMembership{{GID: 60001, Name: "alice"}, {GID: 60100, Name: "devs"}, {GID: 60200, Name: "users"}, {GID: 500, Name: "oldstaff"}},
				Home:   str("/home/alice"), Shell: str("/bin/bash"),
				Gecos: "Alice Example", Status: apitypes.NSSStatusActive, KeyID: keyID,
			},
			{
				Name: "bob", UID: 60002, GID: 60002,
				Groups: []apitypes.NSSMembership{{GID: 60002, Name: "bob"}},
				Home:   str("/home/bob"), Shell: str("/bin/bash"),
				Gecos: "Bob Example", Status: apitypes.NSSStatusDisabled, KeyID: keyID,
			},
		},
		groups: []apitypes.NSSGroupResponse{
			{Name: "alice", GID: 60001, Members: []string{}},
			{Name: "bob", GID: 60002, Members: []string{}},
			{Name: "devs", GID: 60100, Members: []string{"alice"}},
			{Name: "users", GID: 60200, Members: []string{"alice"}},
			{Name: "oldstaff", GID: 500, Members: []string{"alice"}},
		},
	}
}

// hostList collects repeated -host flags.
type hostList []string

// String implements flag.Value.
func (h *hostList) String() string { return strings.Join(*h, ",") }

// Set implements flag.Value.
func (h *hostList) Set(v string) error { *h = append(*h, v); return nil }

// main parses the flags and serves until SIGINT or SIGTERM.
func main() {
	listen := flag.String("listen", "127.0.0.1:8443", "address to serve HTTPS on")
	out := flag.String("out", ".", "directory to write ca.pem, spki.txt and nss.key into")
	var hosts hostList
	flag.Var(&hosts, "host", "an extra DNS name or IP address for the server certificate (repeatable)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, *listen, *out, hosts); err != nil {
		log.Error("fakeupstream failed", "error", err)
		os.Exit(1)
	}
}

// run writes the trust files, then serves the directory until ctx ends.
func run(ctx context.Context, log *slog.Logger, listen, out string, hosts []string) error {
	cert, caPEM, err := newCertificates(hosts)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(cert.Leaf.RawSubjectPublicKeyInfo)
	files := []struct {
		name string
		body []byte
	}{
		{"ca.pem", caPEM},
		{"spki.txt", []byte(base64.StdEncoding.EncodeToString(sum[:]) + "\n")},
		{"nss.key", []byte(keyID + "\n" + keySecret + "\n")},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(out, f.name), f.body, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	srv := &http.Server{
		Handler:           newHandler(newDirectory(), log),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		// ctx is already done; the shutdown gets a bound of its own.
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Warn("shut down", "error", err)
		}
	}()
	log.Info("fakeupstream serving", "addr", ln.Addr().String(), "out", out)
	if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// handler serves the fake's directory.
type handler struct {
	dir directory
	log *slog.Logger
}

// newHandler serves the four lookups behind the fleet key check, in the
// order the real server checks them: the key first, then the path.
func newHandler(dir directory, log *slog.Logger) http.Handler {
	h := handler{dir: dir, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/nss/user/name/{name}", h.user(func(u apitypes.NSSUserResponse, v string) bool { return u.Name == v }, "name"))
	mux.HandleFunc("GET /api/nss/user/uid/{uid}", h.user(func(u apitypes.NSSUserResponse, v string) bool {
		return strconv.FormatUint(uint64(u.UID), 10) == v
	}, "uid"))
	mux.HandleFunc("GET /api/nss/group/name/{name}", h.group(func(g apitypes.NSSGroupResponse, v string) bool { return g.Name == v }, "name"))
	mux.HandleFunc("GET /api/nss/group/gid/{gid}", h.group(func(g apitypes.NSSGroupResponse, v string) bool {
		return strconv.FormatUint(uint64(g.GID), 10) == v
	}, "gid"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Info("request", "method", r.Method, "path", r.URL.Path, "key_id", r.Header.Get(apitypes.HeaderNSSKeyID))
		if r.Header.Get(apitypes.HeaderNSSKeyID) != keyID || r.Header.Get(apitypes.HeaderNSSKeySecret) != keySecret {
			h.send(w, http.StatusUnauthorized, apitypes.Envelope[any]{Error: "unauthorized", ErrorCode: apitypes.ErrorCodeNSSUnauthorized})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// user answers the first user match reports for the path value key, or
// the authoritative miss.
func (h handler) user(match func(apitypes.NSSUserResponse, string) bool, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, u := range h.dir.users {
			if match(u, r.PathValue(key)) {
				h.send(w, http.StatusOK, apitypes.Envelope[apitypes.NSSUserResponse]{Data: u})
				return
			}
		}
		h.notFound(w)
	}
}

// group answers the first group match reports for the path value key, or
// the authoritative miss.
func (h handler) group(match func(apitypes.NSSGroupResponse, string) bool, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, g := range h.dir.groups {
			if match(g, r.PathValue(key)) {
				h.send(w, http.StatusOK, apitypes.Envelope[apitypes.NSSGroupResponse]{Data: g})
				return
			}
		}
		h.notFound(w)
	}
}

// notFound is the authoritative miss the daemon may negative-cache.
func (h handler) notFound(w http.ResponseWriter) {
	h.send(w, http.StatusNotFound, apitypes.Envelope[any]{Error: "not found", ErrorCode: apitypes.ErrorCodeNSSNotFound})
}

// send writes body as JSON with status.
func (h handler) send(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.log.Warn("write a response", "error", err)
	}
}

// newCertificates makes a CA and a server certificate it signed, for
// 127.0.0.1, ::1, localhost and hosts, and returns the server's key pair
// and the CA in PEM.
func newCertificates(hosts []string) (tls.Certificate, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		// not covered: crypto/rand does not fail on a supported platform.
		return tls.Certificate{}, nil, fmt.Errorf("generate the CA key: %w", err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ssoossh-nss integration CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("create the CA certificate: %w", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("parse the CA certificate: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		// not covered: as above.
		return tls.Certificate{}, nil, fmt.Errorf("generate the server key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "fakeupstream"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("create the server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("parse the server certificate: %w", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key, Leaf: leaf}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}
