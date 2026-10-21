package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// echoTestConfig is the smallest config with a public URL.
const echoTestConfig = `ssh_key: "test-key-material"
http:
  public_url: https://ssh.example.com
`

// testEchoStore is the real code store over a test database. It keeps the
// minted code and runs onGet before every read, so a test can play the
// browser's part between two polls.
type testEchoStore struct {
	*service.IdentityEchoCodeService
	code   string
	reads  int
	onGet  func(s *testEchoStore, id string)
	closed bool
}

// newTestEchoStore builds the store over a fresh database.
func newTestEchoStore(t *testing.T) *testEchoStore {
	t.Helper()
	return &testEchoStore{IdentityEchoCodeService: service.NewIdentityEchoCodeService(testdb.New(t))}
}

// Mint mints and keeps the code.
func (s *testEchoStore) Mint(ctx context.Context, req service.MintRequest) (*service.MintedEchoCode, error) {
	minted, err := s.IdentityEchoCodeService.Mint(ctx, req)
	if err == nil {
		s.code = minted.Code
	}
	return minted, err
}

// Get counts the read, lets the test act, then reads the row.
func (s *testEchoStore) Get(ctx context.Context, id string) (*model.IdentityEchoCode, error) {
	s.reads++
	if s.onGet != nil {
		s.onGet(s, id)
	}
	return s.IdentityEchoCodeService.Get(ctx, id)
}

// Close records the release.
func (s *testEchoStore) Close(context.Context) error {
	s.closed = true
	return nil
}

// echoRun is one run of `ssoosshd identity echo` under test.
type echoRun struct {
	stdout string
	err    error
}

// runEchoCommand runs `ssoosshd -c <config> identity echo <args>` over
// store, polling every millisecond.
func runEchoCommand(ctx context.Context, t *testing.T, configYAML string, store echoCodeStore, now func() time.Time, args ...string) echoRun {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	echoCmd := &identityEchoCommand{
		open: func(*config.Config, io.Writer) (echoCodeStore, error) { return store, nil },
		poll: time.Millisecond,
		now:  now,
	}
	root := &RootCommand{commands: []simplecobra.Commander{&identityCommand{commands: []simplecobra.Commander{echoCmd}}}}
	exec, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("build the command tree: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root.CobraCommand().SetOut(&stdout)
	root.CobraCommand().SetErr(&stderr)

	_, runErr := exec.Execute(ctx, append([]string{"-c", path, "identity", "echo"}, args...))
	return echoRun{stdout: stdout.String(), err: runErr}
}

// browserUses redeems the code on the first read and records outcome on
// the second, as the server would.
func browserUses(t *testing.T, outcome string) func(s *testEchoStore, id string) {
	return func(s *testEchoStore, id string) {
		switch s.reads {
		case 1:
			if _, err := s.Redeem(context.Background(), s.code, "203.0.113.9"); err != nil {
				t.Errorf("Redeem() error = %v", err)
			}
		case 2:
			s.RecordOutcome(context.Background(), id, outcome)
		}
	}
}

func TestIdentityEchoCommand_ShouldPrintALinkCarryingTheCodeInTheFragment(t *testing.T) {
	t.Parallel()

	store := newTestEchoStore(t)
	store.onGet = browserUses(t, "")
	got := runEchoCommand(context.Background(), t, echoTestConfig, store, time.Now)

	if !strings.Contains(got.stdout, "https://ssh.example.com/identity/echo#code="+store.code+"\n") || !store.closed {
		t.Errorf("stdout lacks the link, or the store was left open:\n%s", got.stdout)
	}
}

func TestIdentityEchoCommand_ShouldReportHowTheLinkWasUsed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outcome   string
		wantErr   bool
		wantLines []string
	}{
		{
			name:      "should report the claims shown",
			wantLines: []string{"used      at ", " from 203.0.113.9\n", "outcome   the claims were shown in the browser; nothing was stored"},
		},
		{
			name:      "should print the error that came before any claims",
			outcome:   `failed: oauth2: "invalid_client"`,
			wantErr:   true,
			wantLines: []string{`error     failed: oauth2: "invalid_client"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newTestEchoStore(t)
			store.onGet = browserUses(t, tt.outcome)
			got := runEchoCommand(context.Background(), t, echoTestConfig, store, time.Now)

			if (got.err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %t; stdout:\n%s", got.err, tt.wantErr, got.stdout)
			}
			for _, line := range tt.wantLines {
				if !strings.Contains(got.stdout, line) {
					t.Errorf("stdout lacks %q:\n%s", line, got.stdout)
				}
			}
		})
	}
}

func TestIdentityEchoCommand_ShouldReportAnUnusedLinkAsExpired(t *testing.T) {
	t.Parallel()

	store := newTestEchoStore(t)
	later := func() time.Time { return time.Now().Add(time.Hour) }
	got := runEchoCommand(context.Background(), t, echoTestConfig, store, later, "--ttl", "1m")

	if got.err == nil || !strings.Contains(got.stdout, "expired ") || !strings.Contains(got.stdout, ", unused") {
		t.Errorf("error = %v, stdout:\n%s", got.err, got.stdout)
	}
}

func TestIdentityEchoCommand_ShouldReportAReplacedLink(t *testing.T) {
	t.Parallel()

	store := newTestEchoStore(t)
	store.onGet = func(s *testEchoStore, _ string) {
		if s.reads == 1 {
			if _, err := s.IdentityEchoCodeService.Mint(context.Background(), service.MintRequest{}); err != nil {
				t.Errorf("Mint() error = %v", err)
			}
		}
	}
	got := runEchoCommand(context.Background(), t, echoTestConfig, store, time.Now)

	if !errors.Is(got.err, service.ErrEchoCodeReplaced) || !strings.Contains(got.stdout, "replaced  a newer link replaced this one") {
		t.Errorf("error = %v, stdout:\n%s", got.err, got.stdout)
	}
}

func TestIdentityEchoCommand_ShouldRetireTheLinkWhenInterrupted(t *testing.T) {
	t.Parallel()

	store := newTestEchoStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	store.onGet = func(s *testEchoStore, _ string) {
		if s.reads == 1 {
			cancel()
		}
	}
	got := runEchoCommand(ctx, t, echoTestConfig, store, time.Now)

	_, redeemErr := store.Redeem(context.Background(), store.code, "203.0.113.9")
	if !strings.Contains(got.stdout, "cancelled the link no longer works") || redeemErr == nil {
		t.Errorf("redeem after interrupt = %v, stdout:\n%s", redeemErr, got.stdout)
	}
}

func TestIdentityEchoCommand_ShouldReportASignInThatNeverCameBack(t *testing.T) {
	t.Parallel()

	store := newTestEchoStore(t)
	var clock time.Time
	store.onGet = func(s *testEchoStore, _ string) {
		if s.reads == 1 {
			if _, err := s.Redeem(context.Background(), s.code, "203.0.113.9"); err != nil {
				t.Errorf("Redeem() error = %v", err)
			}
			clock = time.Now().Add(echoCompletionWindow + time.Minute)
		}
	}
	now := func() time.Time {
		if clock.IsZero() {
			return time.Now()
		}
		return clock
	}
	got := runEchoCommand(context.Background(), t, echoTestConfig, store, now)

	if got.err == nil || !strings.Contains(got.stdout, "did not come back within") {
		t.Errorf("error = %v, stdout:\n%s", got.err, got.stdout)
	}
}

func TestIdentityEchoCommand_ShouldRefuseWhatItCannotRunWith(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  string
		args    []string
		wantErr string
	}{
		{name: "should refuse a config without a public URL", config: `ssh_key: "test-key-material"` + "\n", wantErr: "http.public_url"},
		{name: "should refuse a ttl that is not positive", config: echoTestConfig, args: []string{"--ttl", "0s"}, wantErr: "--ttl"},
		{name: "should refuse arguments", config: echoTestConfig, args: []string{"now"}, wantErr: "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := runEchoCommand(context.Background(), t, tt.config, newTestEchoStore(t), time.Now, tt.args...)
			if got.err == nil || !strings.Contains(got.err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want one naming %q", got.err, tt.wantErr)
			}
		})
	}
}

func TestRequestedBy_ShouldNameTheAccountAndTheHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		detail map[string]any
		want   string
	}{
		{name: "should join the account and the host", detail: map[string]any{"os_user": "ssoossh", "host": "ssh-1"}, want: "ssoossh@ssh-1"},
		{name: "should name the sudo caller", detail: map[string]any{"os_user": "ssoossh", "sudo_user": "alice", "host": "ssh-1"}, want: "ssoossh (sudo from alice)@ssh-1"},
		{name: "should leave out an unknown host", detail: map[string]any{"os_user": "ssoossh"}, want: "ssoossh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := requestedBy(tt.detail); got != tt.want {
				t.Errorf("requestedBy() = %q, want %q", got, tt.want)
			}
		})
	}
}
