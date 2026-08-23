//go:build e2e

package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// The client has around thirty distinct terminal error strings whose entire
// job is telling a person what they got wrong. The suite asserted one of
// them. These are the rest -- the ones a user actually hits -- driven
// through the real binary, asserting both the exit status and the message,
// because a CLI's contract is both.

// The commands here are grouped by what has to be standing up for them to
// fail the way they should: nothing at all, a server that misbehaves, or a
// working server reached the wrong way.

func TestUserError_ShouldReportAnUnreachableServer(t *testing.T) {
	_, bin := harness.Binaries(t)

	res := harness.RunClient(t, bin, harness.ClientOptions{
		Args: []string{"ssh", "login", "--server", deadServer},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit against a dead server, got 0\nstdout:\n%s", res.Stdout)
	}
	// Naming the address matters: the most common cause is a typo in it or
	// a config file the user did not know was being read.
	if !strings.Contains(res.Stderr, "127.0.0.1:1") {
		t.Errorf("expected the error to name the address it could not reach, got:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "connection refused") {
		t.Errorf("expected the error to say the connection was refused, got:\n%s", res.Stderr)
	}
}

// An https server whose certificate nothing trusts. Two outcomes have to
// hold: refused by default, and reachable when the user has explicitly said
// insecure_skip_verify. Getting the first wrong is a security failure and
// the second is the documented escape hatch for a private CA.
func TestUserError_ShouldRefuseAnUntrustedTLSServerUnlessTold(t *testing.T) {
	srv := startUntrustedTLSServer(t)
	_, bin := harness.Binaries(t)

	t.Run("refused by default", func(t *testing.T) {
		res := harness.RunClient(t, bin, harness.ClientOptions{
			Args: []string{"ssh", "login", "--server", srv.URL},
		})

		if res.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit against an untrusted certificate, got 0\nstdout:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "certificate") {
			t.Errorf("expected the error to mention the certificate, got:\n%s", res.Stderr)
		}
	})

	t.Run("reached when insecure_skip_verify is set", func(t *testing.T) {
		res := harness.RunClient(t, bin, harness.ClientOptions{
			Args:     []string{"ssh", "login", "--server", srv.URL},
			UserYAML: "insecure_skip_verify: true\n",
		})

		// The stub answers /api/ca and then refuses the request, so this
		// still fails -- but it has to fail past TLS, not at it. Seeing the
		// certificate complaint here would mean the setting did nothing.
		if strings.Contains(res.Stderr, "x509") || strings.Contains(res.Stderr, "certificate signed by unknown authority") {
			t.Errorf("insecure_skip_verify did not take effect, got:\n%s", res.Stderr)
		}
	})
}

// A key_filename pointing at a directory cannot be written to, and the
// message has to say so rather than reporting some downstream symptom.
func TestUserError_ShouldRefuseAKeyFilenameThatIsADirectory(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		UserYAML: "use_agent: false\nkey_filename: " + dir + "\n",
		Unset:    []string{"SSH_AUTH_SOCK"},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a directory key path, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "directory") {
		t.Errorf("expected the error to say the path is a directory, got:\n%s", res.Stderr)
	}
}

// An empty key_filename with use_agent off leaves nowhere to put anything.
// The message names the setting to change, which is the whole of what the
// user needs.
func TestUserError_ShouldRefuseAnEmptyKeyFilename(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		UserYAML: "use_agent: false\nkey_filename: \"\"\n",
		Unset:    []string{"SSH_AUTH_SOCK"},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an empty key path, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "key_filename") {
		t.Errorf("expected the error to name the setting, got:\n%s", res.Stderr)
	}
}

// A key type the client does not know about has to be refused at config
// load, before a request is made, so the user finds out from the command
// they ran rather than from a failed connection later.
func TestUserError_ShouldRefuseAnUnknownKeyType(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "login", "--server", f.Server.BaseURL, "--key-type", "not-a-cipher"},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unknown key type, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "not-a-cipher") {
		t.Errorf("expected the error to name the offending type, got:\n%s", res.Stderr)
	}
}

// An RSA key below the minimum is a weak-key request, not a typo to
// tolerate.
func TestUserError_ShouldRefuseAnUndersizedRSAKey(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "login", "--server", f.Server.BaseURL, "--key-type", "rsa", "--key-size", "512"},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an undersized RSA key, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "2048") {
		t.Errorf("expected the error to name the minimum size, got:\n%s", res.Stderr)
	}
}

// An unknown subcommand should be a usage error, not a stack trace or a
// silent success.
func TestUserError_ShouldRefuseAnUnknownCommand(t *testing.T) {
	_, bin := harness.Binaries(t)

	res := harness.RunClient(t, bin, harness.ClientOptions{
		Args: []string{"ssh", "lognin", "--server", deadServer},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unknown command, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "lognin") {
		t.Errorf("expected the error to quote what was typed, got:\n%s", res.Stderr)
	}
}

// --config naming a file that is not there is a typo worth reporting, and
// unlike the search-path locations -- where absence is normal -- it has to
// be fatal: the user said explicitly which file to use, and continuing
// means every setting in it silently does not apply.
//
// A working server, deliberately: against a dead one this passes whatever
// the config handling does, because the CA fetch fails anyway. That is how
// the first version of this test passed while proving nothing.
func TestUserError_ShouldRefuseAMissingExplicitConfigFile(t *testing.T) {
	f := newFixture(t)
	missing := t.TempDir() + "/nope.yaml"

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config", "--config", missing, "--server", f.Server.BaseURL},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a missing --config file, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, missing) {
		t.Errorf("expected the error to name the file, got:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--config") {
		t.Errorf("expected the error to say the file came from --config, got:\n%s", res.Stderr)
	}
}

// The same for a file that is there but will not parse.
func TestUserError_ShouldRefuseAMalformedExplicitConfigFile(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:       []string{"ssh", "config", "--server", f.Server.BaseURL},
		ConfigYAML: "server: [unterminated\n",
		Env:        map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a malformed --config file, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "--config") {
		t.Errorf("expected the error to say the file came from --config, got:\n%s", res.Stderr)
	}
}

// startUntrustedTLSServer runs an https server with a self-signed
// certificate no client trusts, answering /api/ca so a client that gets
// past TLS fails somewhere later and the two outcomes stay
// distinguishable.
func startUntrustedTLSServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ca", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not available", http.StatusServiceUnavailable)
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// selfSignedCert builds a certificate for 127.0.0.1 signed by nothing.
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate a test key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ssoossh-e2e-untrusted"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create a test certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
