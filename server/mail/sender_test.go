package mail

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/testutil"
)

// sinkConfig points a mail config at a running sink, with the plaintext,
// unauthenticated defaults a local relay would use.
func sinkConfig(sink *testutil.SMTPSink) *config.MailConfig {
	return &config.MailConfig{
		Enabled: true,
		From:    "ssoossh <ssoossh@example.com>",
		SMTP: config.SMTPConfig{
			Host:    sink.Host,
			Port:    sink.Port,
			TLS:     config.MailTLSOff,
			Auth:    config.MailAuthNone,
			Timeout: 5 * time.Second,
		},
	}
}

// newSender validates cfg the way startup does and builds the sender, so a
// test cannot accidentally exercise a configuration the server would have
// refused to boot with.
func newSender(t *testing.T, cfg *config.MailConfig) *SMTPSender {
	t.Helper()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("config.Validate: %v", err)
	}
	sender, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	return sender
}

func sampleMessage() Outgoing {
	return Outgoing{
		To: "alice@example.com",
		Rendered: Rendered{
			Subject: "Service enrollment created for deploy-bot",
			Text:    "plain text body",
			HTML:    "<p>html body</p>",
		},
	}
}

func TestSMTPSender_shouldDeliverToALocalRelay(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})
	sender := newSender(t, sinkConfig(sink))

	if err := sender.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	messages := sink.Messages()
	if len(messages) != 1 {
		t.Fatalf("sink received %d messages, want 1", len(messages))
	}
	msg := messages[0]

	if msg.From != "ssoossh@example.com" {
		t.Errorf("envelope from = %q", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "alice@example.com" {
		t.Errorf("envelope to = %v", msg.To)
	}
	if got := msg.Header("Subject"); !strings.Contains(got, "deploy-bot") {
		t.Errorf("Subject header = %q", got)
	}
}

// Both bodies must travel: a text-only reader has to get the whole content,
// not a placeholder telling them to view it somewhere else.
func TestSMTPSender_shouldSendBothBodiesAsAlternatives(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})
	sender := newSender(t, sinkConfig(sink))

	if err := sender.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	data := sink.Messages()[0].Data
	if !strings.Contains(data, "multipart/alternative") {
		t.Error("message is not multipart/alternative")
	}
	if !strings.Contains(data, "text/plain") {
		t.Error("message has no text/plain part")
	}
	if !strings.Contains(data, "text/html") {
		t.Error("message has no text/html part")
	}
}

func TestSMTPSender_shouldSetReplyToWhenConfigured(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})
	cfg := sinkConfig(sink)
	cfg.ReplyTo = "ssh-admins@example.com"

	if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := sink.Messages()[0].Header("Reply-To"); !strings.Contains(got, "ssh-admins@example.com") {
		t.Errorf("Reply-To = %q", got)
	}
}

func TestSMTPSender_shouldOmitReplyToWhenNotConfigured(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})

	if err := newSender(t, sinkConfig(sink)).Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := sink.Messages()[0].Header("Reply-To"); got != "" {
		t.Errorf("Reply-To = %q, want it absent", got)
	}
}

// Authentication is only meaningful over an encrypted connection, which is
// also the only shape go-mail will send PLAIN credentials over.
func TestSMTPSender_shouldAuthenticateOverSTARTTLS(t *testing.T) {
	tests := []struct {
		name string
		auth string
	}{
		{"plain", config.MailAuthPlain},
		{"login", config.MailAuthLogin},
		{"auto", config.MailAuthAuto},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{
				STARTTLS: true,
				Username: "relay-user",
				Password: "s3cret",
			})

			cfg := sinkConfig(sink)
			cfg.SMTP.TLS = config.MailTLSRequired
			cfg.SMTP.CAFile = sink.CAFile(t)
			cfg.SMTP.ServerName = "localhost"
			cfg.SMTP.Auth = tt.auth
			cfg.SMTP.Username = "relay-user"
			cfg.SMTP.Password = "s3cret"

			if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if !sink.Authenticated() {
				t.Error("the relay was never authenticated to")
			}
			if len(sink.Messages()) != 1 {
				t.Errorf("sink received %d messages, want 1", len(sink.Messages()))
			}
		})
	}
}

func TestSMTPSender_shouldDeliverOverImplicitTLS(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{ImplicitTLS: true})

	cfg := sinkConfig(sink)
	cfg.SMTP.TLS = config.MailTLSImplicit
	cfg.SMTP.CAFile = sink.CAFile(t)
	cfg.SMTP.ServerName = "localhost"

	if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(sink.Messages()) != 1 {
		t.Errorf("sink received %d messages, want 1", len(sink.Messages()))
	}
}

// The whole point of "required" is that it fails rather than falling back.
func TestSMTPSender_shouldFailWhenTLSIsRequiredAndTheRelayHasNone(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})

	cfg := sinkConfig(sink)
	cfg.SMTP.TLS = config.MailTLSRequired

	err := newSender(t, cfg).Send(context.Background(), sampleMessage())
	if err == nil {
		t.Fatal("Send succeeded against a relay offering no STARTTLS")
	}
}

// Opportunistic is the default, and its job is to keep working against the
// local relay that offers nothing.
func TestSMTPSender_shouldFallBackToPlaintextWhenTLSIsOpportunistic(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})

	cfg := sinkConfig(sink)
	cfg.SMTP.TLS = config.MailTLSOpportunistic

	if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(sink.Messages()) != 1 {
		t.Errorf("sink received %d messages, want 1", len(sink.Messages()))
	}
}

func TestSMTPSender_shouldFailWhenTheRelayVerificationFails(t *testing.T) {
	// No CA file, so the sink's self-signed certificate is untrusted.
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{STARTTLS: true})

	cfg := sinkConfig(sink)
	cfg.SMTP.TLS = config.MailTLSRequired
	cfg.SMTP.ServerName = "localhost"

	if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err == nil {
		t.Fatal("Send accepted an unverifiable relay certificate")
	}
}

func TestSMTPSender_shouldSkipVerificationWhenExplicitlyAsked(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{STARTTLS: true})

	cfg := sinkConfig(sink)
	cfg.SMTP.TLS = config.MailTLSRequired
	cfg.SMTP.InsecureSkipVerify = true

	if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSMTPSender_shouldReportARejectedMessage(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{RejectData: true})

	err := newSender(t, sinkConfig(sink)).Send(context.Background(), sampleMessage())
	if err == nil {
		t.Fatal("Send reported success for a rejected message")
	}
}

func TestSMTPSender_shouldRejectAnUnusableRecipient(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})
	sender := newSender(t, sinkConfig(sink))

	for _, to := range []string{"", "not an address"} {
		msg := sampleMessage()
		msg.To = to
		if err := sender.Send(context.Background(), msg); err == nil {
			t.Errorf("Send accepted the recipient %q", to)
		}
	}
	if len(sink.Messages()) != 0 {
		t.Error("a message reached the relay despite an unusable recipient")
	}
}

// A relay that accepts the connection and then says nothing is the shape a
// hung or overloaded mail server takes. The timeout is what stops one
// delivery attempt from occupying the sender forever.
func TestSMTPSender_shouldGiveUpOnAnUnresponsiveRelay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Accept and never greet.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	if _, err := fmtSscan(portStr, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := &config.MailConfig{
		Enabled: true,
		From:    "ssoossh@example.com",
		SMTP: config.SMTPConfig{
			Host:    host,
			Port:    port,
			TLS:     config.MailTLSOff,
			Auth:    config.MailAuthNone,
			Timeout: 250 * time.Millisecond,
		},
	}

	start := time.Now()
	if err := newSender(t, cfg).Send(context.Background(), sampleMessage()); err == nil {
		t.Fatal("Send succeeded against an unresponsive relay")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Send took %s to give up, want the configured timeout to bound it", elapsed)
	}
}

// A canceled context has to stop a delivery in progress, so shutdown does
// not wait on a relay that is not answering.
func TestSMTPSender_shouldHonorACanceledContext(t *testing.T) {
	sink := testutil.NewSMTPSink(t, testutil.SMTPSinkOptions{})
	sender := newSender(t, sinkConfig(sink))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sender.Send(ctx, sampleMessage()); err == nil {
		t.Fatal("Send ignored a canceled context")
	}
}

// fmtSscan is fmt.Sscan behind a name the test can reference without
// importing fmt for one call site.
func fmtSscan(s string, target *int) (int, error) {
	return fmt.Sscan(s, target)
}

// Every mechanism the configuration accepts has to map onto one go-mail
// accepts, or a valid config fails at startup with a library error rather
// than at validation with a useful one.
func TestNewSMTPSender_shouldAcceptEveryConfigurableAuthMechanism(t *testing.T) {
	mechanisms := []string{
		config.MailAuthNone,
		config.MailAuthAuto,
		config.MailAuthPlain,
		config.MailAuthLogin,
		config.MailAuthCramMD5,
		config.MailAuthSCRAMSHA1,
		config.MailAuthSCRAMSHA256,
		config.MailAuthXOAuth2,
	}

	for _, mechanism := range mechanisms {
		t.Run(mechanism, func(t *testing.T) {
			cfg := &config.MailConfig{
				Enabled: true,
				From:    "ssoossh@example.com",
				SMTP: config.SMTPConfig{
					Host: "smtp.example.com",
					Port: 587,
					TLS:  config.MailTLSRequired,
					Auth: mechanism,
				},
			}
			if mechanism != config.MailAuthNone {
				cfg.SMTP.Username = "relay-user"
				cfg.SMTP.Password = "s3cret"
			}

			if err := cfg.Validate(); err != nil {
				t.Fatalf("config.Validate: %v", err)
			}
			if _, err := NewSMTPSender(cfg); err != nil {
				t.Errorf("NewSMTPSender: %v", err)
			}
		})
	}
}

// A ca_file that exists but holds no certificate would otherwise produce an
// empty trust pool, which fails every handshake with a confusing message.
func TestNewSMTPSender_shouldRejectACAFileWithNoCertificates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(path, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := &config.MailConfig{
		Enabled: true,
		From:    "ssoossh@example.com",
		SMTP: config.SMTPConfig{
			Host:   "smtp.example.com",
			Port:   587,
			TLS:    config.MailTLSRequired,
			Auth:   config.MailAuthNone,
			CAFile: path,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config.Validate: %v", err)
	}

	if _, err := NewSMTPSender(cfg); err == nil {
		t.Error("NewSMTPSender accepted a ca_file holding no certificates")
	}
}
