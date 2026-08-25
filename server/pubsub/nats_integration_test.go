//go:build natsintegration

package pubsub

// The queue-group integration test: the one assertion the unit tests cannot
// make. TestSubjectCalculator_* proves the topic-to-queue-group *mapping*;
// nothing short of a live broker proves watermill-nats actually applies the
// queue group to the subscription. If that wiring is subtly wrong the unit
// tests stay green and production mints N certificates per approval - which
// is why this file exists and why it must genuinely execute
// (docs/dev/multi-instance-safety-plan.md, queue groups).
//
// Behind the natsintegration tag because it needs Docker:
//
//	go test -tags=natsintegration -count=1 ./server/pubsub/
//
// The broker runs with the same TLS posture production requires (tls,
// verify, client certs), with a throwaway PKI generated per run - so the
// config validator's mTLS requirement is satisfied rather than special-cased
// for tests. Certs travel into the container via docker cp rather than a
// bind mount, because the test may run inside a devcontainer whose /tmp the
// host docker daemon cannot see.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	natsgo "github.com/nats-io/nats.go"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
)

// testPKI is the throwaway CA plus one server and one client leaf, written
// as PEM files the way both nats-server and config.NATSConfig expect them.
type testPKI struct {
	dir string // ca.pem, server.pem, server-key.pem, client.pem, client-key.pem
}

// newTestPKI builds the PKI in dir. One CA signs both leaves: the server
// cert carries 127.0.0.1 and localhost SANs (the broker is dialed by
// loopback), the client cert is what pubsub.New presents for mTLS.
func newTestPKI(t *testing.T, dir string) *testPKI {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ssoossh-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	leaf := func(name string, serial int64, isServer bool) (certPEM, keyPEM []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate %s key: %v", name, err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: name},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}
		if isServer {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			tmpl.DNSNames = []string{"localhost"}
		} else {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatalf("create %s cert: %v", name, err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatalf("marshal %s key: %v", name, err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	}

	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	srvCert, srvKey := leaf("nats-server", 2, true)
	write("server.pem", srvCert)
	write("server-key.pem", srvKey)
	cliCert, cliKey := leaf("ssoossh-node", 3, false)
	write("client.pem", cliCert)
	write("client-key.pem", cliKey)

	return &testPKI{dir: dir}
}

// startNATS runs a TLS-verifying nats-server container on the given port
// and returns its loopback URL. Fails the test on any docker error; skips
// only when the docker daemon itself is absent, following
// test/e2e/harness/postgres.go's convention for missing infrastructure.
//
// Networking is the subtle part. When this test itself runs inside a
// container that talks to the host's docker daemon (the devcontainer
// case), a published port binds on the HOST's loopback and the default
// bridge is a different network - both unreachable from here. So when a
// containerized environment is detected the broker joins this process's
// own network namespace (--network container:<self>), which puts it on our
// own 127.0.0.1 - also exactly what the server cert's SAN says. On a bare
// host the ordinary published-port path is used instead.
//
// The port is allocated, not passed in. A shared network namespace cannot
// remap ports, so every broker in a run needs a distinct one; this used to
// be satisfied by each test passing a different constant (42421-42423),
// which held within a run and failed across two, because a container port
// binds host state rather than worktree state. Two runs on one machine
// collided deterministically.
func startNATS(t *testing.T, pki *testPKI) string {
	t.Helper()

	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}

	port := freeNATSPort(t)

	args := []string{"create"}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		self, err := os.Hostname()
		if err != nil {
			t.Fatalf("hostname: %v", err)
		}
		args = append(args, "--network", "container:"+self)
	} else {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port))
	}
	args = append(args, "nats:2-alpine",
		"--port", fmt.Sprintf("%d", port),
		"--tls",
		"--tlscert", "/certs/server.pem",
		"--tlskey", "/certs/server-key.pem",
		"--tlsverify",
		"--tlscacert", "/certs/ca.pem",
	)

	// create (not run) so the certs can be docker-cp'd in before start.
	// The container ID comes from stdout alone: on a cache miss docker
	// create writes pull progress to stderr while still exiting 0, and a
	// CombinedOutput capture would corrupt the ID with that noise.
	create := exec.Command("docker", args...)
	var createErr strings.Builder
	create.Stderr = &createErr
	out, err := create.Output()
	if err != nil {
		t.Fatalf("docker create: %v\n%s", err, createErr.String())
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })

	//nolint:gosec // both arguments are test-local: pki.dir is t.TempDir() and
	// id is the container ID docker create just printed.
	if out, err := exec.Command("docker", "cp", pki.dir+"/.", id+":/certs").CombinedOutput(); err != nil {
		t.Fatalf("docker cp certs: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "start", id).CombinedOutput(); err != nil {
		t.Fatalf("docker start: %v\n%s", err, out)
	}

	url := fmt.Sprintf("nats://127.0.0.1:%d", port)

	// Readiness: a real mTLS connect, retried. Polling the port alone is
	// not enough - the broker accepts TCP before TLS is serving.
	deadline := time.Now().Add(30 * time.Second)
	for {
		nc, err := natsgo.Connect(url,
			natsgo.ClientCert(filepath.Join(pki.dir, "client.pem"), filepath.Join(pki.dir, "client-key.pem")),
			natsgo.RootCAs(filepath.Join(pki.dir, "ca.pem")),
		)
		if err == nil {
			nc.Close()
			return url
		}
		if time.Now().After(deadline) {
			t.Fatalf("nats-server never became ready at %s: %v", url, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// newNATSPubSub builds a PubSub through the production constructor - the
// same path ssoosshd takes - so the test exercises the real queue-group
// wiring rather than a test-local reimplementation.
func newNATSPubSub(t *testing.T, url string, pki *testPKI) *PubSub {
	t.Helper()
	cfg := &config.PubSubConfig{
		Backend: config.PubSubBackendNATS,
		NATS: config.NATSConfig{
			URL:      url,
			CertFile: filepath.Join(pki.dir, "client.pem"),
			KeyFile:  filepath.Join(pki.dir, "client-key.pem"),
			CAFile:   filepath.Join(pki.dir, "ca.pem"),
		},
	}
	ps, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatalf("pubsub.New(nats): %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(context.Background()) })
	return ps
}

// drain counts messages arriving on ch into total until ctx ends, acking
// each - two of these against the same queue-grouped topic are the
// "competing consumers" half of every assertion below.
func drain(ctx context.Context, ch <-chan *message.Message, total *atomic.Int64) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			total.Add(1)
			msg.Ack()
		}
	}
}

func TestNATSQueueGroups_ShouldDeliverEachSignJobToExactlyOneSubscriber(t *testing.T) {
	pki := newTestPKI(t, t.TempDir())
	url := startNATS(t, pki)

	instanceA := newNATSPubSub(t, url, pki)
	instanceB := newNATSPubSub(t, url, pki)
	publisher := newNATSPubSub(t, url, pki)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	subA, err := instanceA.Subscriber.Subscribe(ctx, certmsg.SignQueueTopic)
	if err != nil {
		t.Fatalf("instance A subscribe: %v", err)
	}
	subB, err := instanceB.Subscriber.Subscribe(ctx, certmsg.SignQueueTopic)
	if err != nil {
		t.Fatalf("instance B subscribe: %v", err)
	}

	var gotA, gotB atomic.Int64
	go drain(ctx, subA, &gotA)
	go drain(ctx, subB, &gotB)

	// Let both subscriptions register with the broker before publishing:
	// core NATS delivers nothing to a subscriber that arrives late, so a
	// racing publish would undercount and fail the test for the wrong
	// reason.
	time.Sleep(time.Second)

	const n = 20
	for i := range n {
		msg := message.NewMessage(fmt.Sprintf("sign-%d", i), []byte(`{}`))
		if err := publisher.Publisher.Publish(certmsg.SignQueueTopic, msg); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Wait until the count settles rather than a fixed sleep.
	deadline := time.Now().Add(10 * time.Second)
	for gotA.Load()+gotB.Load() < n && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	// Grace period to catch duplicates that would arrive after the count
	// first reaches n - the failure mode this test exists for.
	time.Sleep(2 * time.Second)

	total := gotA.Load() + gotB.Load()
	if total != n {
		t.Fatalf("queue group did not deliver exactly once: %d published, %d delivered (A=%d B=%d); >%d means the queue group is not applied and one approval would mint one certificate per instance",
			n, total, gotA.Load(), gotB.Load(), n)
	}
	t.Logf("sign queue: %d published, A=%d B=%d, total=%d (exactly once)", n, gotA.Load(), gotB.Load(), total)
}

func TestNATSQueueGroups_ShouldDeliverEachSignedReplyToExactlyOneListener(t *testing.T) {
	pki := newTestPKI(t, t.TempDir())
	url := startNATS(t, pki)

	listenerA := newNATSPubSub(t, url, pki)
	listenerB := newNATSPubSub(t, url, pki)
	signer := newNATSPubSub(t, url, pki)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	subA, err := listenerA.Subscriber.Subscribe(ctx, certmsg.SignedTopic)
	if err != nil {
		t.Fatalf("listener A subscribe: %v", err)
	}
	subB, err := listenerB.Subscriber.Subscribe(ctx, certmsg.SignedTopic)
	if err != nil {
		t.Fatalf("listener B subscribe: %v", err)
	}

	var gotA, gotB atomic.Int64
	go drain(ctx, subA, &gotA)
	go drain(ctx, subB, &gotB)
	time.Sleep(time.Second)

	const n = 20
	for i := range n {
		msg := message.NewMessage(fmt.Sprintf("signed-%d", i), []byte(`{}`))
		if err := signer.Publisher.Publish(certmsg.SignedTopic, msg); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for gotA.Load()+gotB.Load() < n && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)

	total := gotA.Load() + gotB.Load()
	if total != n {
		t.Fatalf("signed-reply queue group did not deliver exactly once: %d published, %d delivered (A=%d B=%d); >%d means one signature would produce one audit row per instance",
			n, total, gotA.Load(), gotB.Load(), n)
	}
	t.Logf("signed topic: %d published, A=%d B=%d, total=%d (exactly once)", n, gotA.Load(), gotB.Load(), total)
}

func TestNATSWaitTopic_ShouldFanOutToTheSubscribedInstance(t *testing.T) {
	pki := newTestPKI(t, t.TempDir())
	url := startNATS(t, pki)

	waiter := newNATSPubSub(t, url, pki)
	resolver := newNATSPubSub(t, url, pki)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	topic := certmsg.WaitTopic("req-a1b2c3")
	sub, err := waiter.Subscriber.Subscribe(ctx, topic)
	if err != nil {
		t.Fatalf("wait subscribe: %v", err)
	}
	time.Sleep(time.Second)

	if err := resolver.Publisher.Publish(topic, message.NewMessage("wake-1", []byte(`{"status":"approved"}`))); err != nil {
		t.Fatalf("publish wake: %v", err)
	}

	select {
	case msg := <-sub:
		msg.Ack()
		// Received: the empty queue group gives ordinary delivery, so the
		// instance holding the SSE stream is woken by a resolve that
		// happened on another instance - the multi-instance delivery path.
	case <-time.After(10 * time.Second):
		t.Fatal("wake never delivered: the wait topic must use plain fan-out, not queue semantics - a queue group here could route the wake to an instance with no waiting client")
	}
}

// claimedNATSPorts records every port freeNATSPort has handed out in this
// process, so a port released back to the kernel is never offered twice
// before the container that wants it has bound it.
var claimedNATSPorts sync.Map

// freeNATSPort allocates a loopback port for a broker container.
//
// Same close-then-rebind window as the e2e harness's freePort, and the same
// reason it cannot be closed: a container port mapping cannot be handed an
// open listener. Within a process no port is ever offered twice; across two
// concurrent runs the residual race is the kernel handing the same ephemeral
// port to both at once, which is unlikely rather than the certainty a
// hardcoded constant guaranteed.
func freeNATSPort(t *testing.T) int {
	t.Helper()

	const attempts = 20
	for range attempts {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("allocating a port: %v", err)
		}
		addr, ok := ln.Addr().(*net.TCPAddr)
		if !ok {
			_ = ln.Close()
			t.Fatalf("expected a *net.TCPAddr, got %T", ln.Addr())
		}
		port := addr.Port
		if err := ln.Close(); err != nil {
			t.Fatalf("releasing the probe listener: %v", err)
		}
		if _, taken := claimedNATSPorts.LoadOrStore(port, struct{}{}); !taken {
			return port
		}
	}

	t.Fatalf("could not find an unclaimed ephemeral port in %d attempts", attempts)
	return 0
}
