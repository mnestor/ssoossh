package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

// TestRunInspect_ShouldDescribeWhatTheCertificateGrants covers the point of
// the command: it used to print the certificate blob, which tells a human
// nothing about what they are holding.
func TestRunInspect_ShouldDescribeWhatTheCertificateGrants(t *testing.T) {
	ours := newTestCA(t)
	cert := newTestCert(t, ours, "alice", 3*time.Hour)
	cert.Extensions = map[string]string{"permit-pty": "", "permit-agent-forwarding": ""}
	cert.CriticalOptions = map[string]string{"force-command": "/usr/bin/true"}

	ag := &stubAgent{identities: []xssh.PublicKey{cert}, cas: []xssh.PublicKey{ours.public}}

	var out bytes.Buffer
	if err := runInspect(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		"alice",
		"permit-pty",
		"permit-agent-forwarding",
		"force-command=/usr/bin/true",
		"from now",
		"user",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not mention %q:\n%s", want, out.String())
		}
	}
}

// TestRunInspect_ShouldListExtensionsInAStableOrder guards against map
// iteration order making the same command print differently between runs.
func TestRunInspect_ShouldListExtensionsInAStableOrder(t *testing.T) {
	ours := newTestCA(t)
	cert := newTestCert(t, ours, "alice", time.Hour)
	cert.Extensions = map[string]string{
		"permit-pty":              "",
		"permit-agent-forwarding": "",
		"permit-port-forwarding":  "",
	}
	ag := &stubAgent{identities: []xssh.PublicKey{cert}, cas: []xssh.PublicKey{ours.public}}

	var first bytes.Buffer
	if err := runInspect(&RootCommand{ssh: ag}, &first); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range 20 {
		var next bytes.Buffer
		if err := runInspect(&RootCommand{ssh: ag}, &next); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if next.String() != first.String() {
			t.Fatalf("output is not stable between runs:\n%s\nvs\n%s", first.String(), next.String())
		}
	}
}

func TestRunInspect_ShouldSayWhenNothingIsLoaded(t *testing.T) {
	ag := &stubAgent{}

	var out bytes.Buffer
	if err := runInspect(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No certificates") {
		t.Errorf("got %q, want it to say nothing is loaded", out.String())
	}
}

func TestRunInspect_ShouldRenderEmptyOptionSets(t *testing.T) {
	ours := newTestCA(t)
	cert := newTestCert(t, ours, "alice", time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{cert}, cas: []xssh.PublicKey{ours.public}}

	var out bytes.Buffer
	if err := runInspect(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "(none)") {
		t.Errorf("got %q, want empty option sets rendered rather than left blank", out.String())
	}
}

func TestCriticalOptionList(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    string
	}{
		{name: "should render nothing for an empty set", options: nil, want: ""},
		{
			name:    "should render a value when there is one",
			options: map[string]string{"force-command": "/bin/echo"},
			want:    "force-command=/bin/echo",
		},
		{
			name:    "should render a bare name when there is no value",
			options: map[string]string{"no-touch-required": ""},
			want:    "no-touch-required",
		},
		{
			name:    "should sort by name",
			options: map[string]string{"source-address": "10.0.0.0/8", "force-command": "/bin/echo"},
			want:    "force-command=/bin/echo, source-address=10.0.0.0/8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := criticalOptionList(tt.options); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// certTypeName renders the SSH certificate type constant as the word a
// human uses. The host and unknown arms had never run: ssoossh issues only
// user certificates, so nothing in the suite produced anything else -- which
// is exactly why the unknown arm is worth pinning. A server that started
// sending something unexpected should read as "unknown (3)" and not as a
// blank or a crash.
func TestCertTypeName_ShouldNameEachCertificateType(t *testing.T) {
	tests := []struct {
		name     string
		certType uint32
		want     string
	}{
		{name: "user", certType: xssh.UserCert, want: "user"},
		{name: "host", certType: xssh.HostCert, want: "host"},
		{name: "anything else", certType: 99, want: "unknown (99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := certTypeName(tt.certType); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// runInspect casts each listed identity to *ssh.Certificate, and its own
// comment calls the failure branch unreachable short of a backend bug. The
// backend bug was real: FileAgent.List(true) returned a bare public key
// where a certificate was promised (800d5e1). Reporting it beats printing
// nothing, because "nothing loaded" and "the backend handed me the wrong
// thing" send a reader in completely different directions.
func TestRunInspect_ShouldReportAnIdentityThatIsNotACertificate(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{
		identities:     []xssh.PublicKey{ours.public},
		cas:            []xssh.PublicKey{ours.public},
		listUnfiltered: true,
	}

	var out bytes.Buffer
	if err := runInspect(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "not a certificate") {
		t.Errorf("expected the non-certificate to be reported, got:\n%s", out.String())
	}
}

// A listing failure names the backend, since "which store could not be
// read" is the whole of what the reader needs next.
func TestRunInspect_ShouldReportAListingFailure(t *testing.T) {
	ag := &stubAgent{listErr: errors.New("socket is gone")}

	var out bytes.Buffer
	err := runInspect(&RootCommand{ssh: ag}, &out)
	if err == nil {
		t.Fatal("expected a listing failure to be reported")
	}
	if !strings.Contains(err.Error(), "socket is gone") {
		t.Errorf("got %q, want it to carry the underlying failure", err.Error())
	}
	if !strings.Contains(err.Error(), "stub") {
		t.Errorf("got %q, want it to name the backend", err.Error())
	}
}
