package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/client/config"
)

// TestRunConfig_ShouldPrintTheResolvedValuesNotTheConfiguredOnes is the whole
// point of the command: key type and FIPS steering both have defaults that
// depend on the environment, so echoing the file back would answer a
// different question than the one being asked.
func TestRunConfig_ShouldPrintTheResolvedValuesNotTheConfiguredOnes(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "should resolve an unset key type to its default",
			cfg:  &config.Config{Server: "https://ssh.example.com"},
			want: "ecdsa (384)",
		},
		{
			name: "should report the curve alongside an ecdsa key",
			cfg: &config.Config{
				Server: "https://ssh.example.com",
				SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeECDSA, Size: 521},
			},
			want: "ecdsa (521)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			root := &RootCommand{cfg: tt.cfg, ssh: &stubAgent{}}
			if err := runConfig(root, &out); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("got %q, want it to contain %q", out.String(), tt.want)
			}
		})
	}
}

// TestRunConfig_ShouldReportInsecureTLSPlainly matters because it is the one
// setting someone might leave on by accident after testing against a
// self-signed server.
func TestRunConfig_ShouldReportInsecureTLSPlainly(t *testing.T) {
	root := &RootCommand{
		cfg: &config.Config{Server: "https://ssh.example.com", SkipVerifySSL: true},
		ssh: &stubAgent{},
	}

	var out bytes.Buffer
	if err := runConfig(root, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "TLS verification       disabled") {
		t.Errorf("got %q, want TLS verification reported as disabled", out.String())
	}
}

func TestRunConfig_ShouldReportWhereKeysAreActuallyKept(t *testing.T) {
	root := &RootCommand{
		cfg: &config.Config{Server: "https://ssh.example.com"},
		ssh: &stubAgent{},
	}

	var out bytes.Buffer
	if err := runConfig(root, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// stubAgent reports itself as a live agent with the "stub" backend.
	if !strings.Contains(out.String(), "ssh-agent (stub)") {
		t.Errorf("got %q, want the storage actually in use", out.String())
	}
}

func TestRunConfig_ShouldFailOnAnUnusableKeyConfiguration(t *testing.T) {
	root := &RootCommand{
		cfg: &config.Config{SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeECDSA, Size: 111}},
		ssh: &stubAgent{},
	}

	var out bytes.Buffer
	if err := runConfig(root, &out); err == nil {
		t.Fatal("expected an invalid curve to be reported as an error")
	}
}

func TestCASummary(t *testing.T) {
	tests := []struct {
		name string
		ca   string
		want string
	}{
		{name: "should render nothing when unset", ca: "", want: ""},
		{
			name: "should keep the algorithm and shorten the key material",
			ca:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca@example",
			want: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA…",
		},
		{
			name: "should pass a short value through untouched",
			ca:   "ssh-ed25519 AAAA",
			want: "ssh-ed25519 AAAA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := caSummary(tt.ca); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
