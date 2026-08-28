package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// validMail is the smallest configuration that should pass, so each case
// below can change exactly one thing and attribute the failure to it.
func validMail() MailConfig {
	return MailConfig{
		Enabled: true,
		From:    "ssoossh@example.com",
		SMTP: SMTPConfig{
			Host: "smtp.example.com",
			Port: 587,
			TLS:  MailTLSOpportunistic,
			Auth: MailAuthNone,
		},
	}
}

func TestMailConfig_shouldSkipValidationWhenDisabled(t *testing.T) {
	// A deployment that never turns mail on should not have to hold a
	// valid SMTP host, and an operator half-filling the section while
	// disabled must not be able to stop the server booting.
	c := MailConfig{Enabled: false, SMTP: SMTPConfig{Port: -1}}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate on a disabled section: %v", err)
	}
}

func TestMailConfig_shouldAcceptAMinimalRelayConfiguration(t *testing.T) {
	c := validMail()
	if err := c.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestMailConfig_shouldDefaultThePolicyFieldsWhenLeftEmpty(t *testing.T) {
	// Empty is what an operator who only set host and from actually has.
	// Opportunistic TLS and no auth is the local-relay case, which is the
	// documented default.
	c := MailConfig{Enabled: true, From: "a@example.com", SMTP: SMTPConfig{Host: "localhost", Port: 25}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.SMTP.TLS != MailTLSOpportunistic {
		t.Errorf("TLS = %q, want %q", c.SMTP.TLS, MailTLSOpportunistic)
	}
	if c.SMTP.Auth != MailAuthNone {
		t.Errorf("Auth = %q, want %q", c.SMTP.Auth, MailAuthNone)
	}
}

// Zero is what an unset key unmarshals to, not a port an operator chose,
// so it takes the conventional relay port rather than failing the server.
func TestMailConfig_shouldDefaultAnUnsetPort(t *testing.T) {
	c := validMail()
	c.SMTP.Port = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.SMTP.Port != 25 {
		t.Errorf("Port = %d, want 25", c.SMTP.Port)
	}
}

func TestMailConfig_shouldRejectBadValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*MailConfig)
		wantErr string
	}{
		{
			name:    "no from address",
			mutate:  func(c *MailConfig) { c.From = "" },
			wantErr: "mail.from",
		},
		{
			name:    "unparseable from address",
			mutate:  func(c *MailConfig) { c.From = "not an address" },
			wantErr: "mail.from",
		},
		{
			name:    "unparseable reply_to address",
			mutate:  func(c *MailConfig) { c.ReplyTo = "also not an address" },
			wantErr: "mail.reply_to",
		},
		{
			name:    "no smtp host",
			mutate:  func(c *MailConfig) { c.SMTP.Host = "" },
			wantErr: "mail.smtp.host",
		},
		{
			name:    "port below range",
			mutate:  func(c *MailConfig) { c.SMTP.Port = -1 },
			wantErr: "mail.smtp.port",
		},
		{
			name:    "port above range",
			mutate:  func(c *MailConfig) { c.SMTP.Port = 70000 },
			wantErr: "mail.smtp.port",
		},
		{
			name:    "unknown tls policy",
			mutate:  func(c *MailConfig) { c.SMTP.TLS = "sometimes" },
			wantErr: "mail.smtp.tls",
		},
		{
			name:    "unknown auth mechanism",
			mutate:  func(c *MailConfig) { c.SMTP.Auth = "secret-handshake" },
			wantErr: "mail.smtp.auth",
		},
		{
			name: "auth without a username",
			mutate: func(c *MailConfig) {
				c.SMTP.Auth = MailAuthPlain
				c.SMTP.Password = "s3cret"
			},
			wantErr: "mail.smtp.username",
		},
		{
			name: "auth without a password",
			mutate: func(c *MailConfig) {
				c.SMTP.Auth = MailAuthPlain
				c.SMTP.Username = "relay-user"
			},
			wantErr: "mail.smtp.password",
		},
		{
			name: "password file that does not exist",
			mutate: func(c *MailConfig) {
				c.SMTP.Auth = MailAuthPlain
				c.SMTP.Username = "relay-user"
				c.SMTP.PasswordFile = filepath.Join(t.TempDir(), "absent")
			},
			wantErr: "mail.smtp.password_file",
		},
		{
			name: "ca file that does not exist",
			mutate: func(c *MailConfig) {
				c.SMTP.CAFile = filepath.Join(t.TempDir(), "absent.pem")
			},
			wantErr: "mail.smtp.ca_file",
		},
		{
			name:    "negative timeout",
			mutate:  func(c *MailConfig) { c.SMTP.Timeout = -time.Second },
			wantErr: "mail.smtp.timeout",
		},
		{
			name: "template dir that is not a directory",
			mutate: func(c *MailConfig) {
				file := filepath.Join(t.TempDir(), "not-a-dir")
				if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
				c.TemplateDir = file
			},
			wantErr: "mail.template_dir",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validMail()
			tt.mutate(&c)

			err := c.Validate()
			if err == nil {
				t.Fatal("Validate accepted the configuration")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not name %q", err, tt.wantErr)
			}
		})
	}
}

func TestMailConfig_shouldReadThePasswordFromItsFile(t *testing.T) {
	// A password file exists so the secret stays out of the config file and
	// out of the effective-config view auditors can read.
	path := filepath.Join(t.TempDir(), "smtp-password")
	if err := os.WriteFile(path, []byte("  file-secret\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := validMail()
	c.SMTP.Auth = MailAuthPlain
	c.SMTP.Username = "relay-user"
	c.SMTP.PasswordFile = path

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := c.SMTP.ResolvedPassword(); got != "file-secret" {
		t.Errorf("ResolvedPassword = %q, want %q", got, "file-secret")
	}
}

func TestSMTPConfig_shouldPreferThePasswordFileOverTheInlinePassword(t *testing.T) {
	// Both set is a migration in progress. Trusting the file is the safer
	// resolution: it is the one the operator can rotate without a restart
	// of anything that reads the config file.
	path := filepath.Join(t.TempDir(), "smtp-password")
	if err := os.WriteFile(path, []byte("from-file"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := validMail()
	c.SMTP.Auth = MailAuthPlain
	c.SMTP.Username = "relay-user"
	c.SMTP.Password = "inline"
	c.SMTP.PasswordFile = path

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := c.SMTP.ResolvedPassword(); got != "from-file" {
		t.Errorf("ResolvedPassword = %q, want %q", got, "from-file")
	}
}

// The suggestion is advice, not a rule: an operator who genuinely wants a
// plaintext unauthenticated relay gets one. What they must not get is
// silence, so the advice is surfaced as warnings rather than as an error.
func TestMailConfig_shouldWarnAboutAnUnprotectedRemoteRelay(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*MailConfig)
		wantWarn bool
	}{
		{
			name:     "remote host without tls",
			mutate:   func(c *MailConfig) { c.SMTP.TLS = MailTLSOff },
			wantWarn: true,
		},
		{
			name:     "remote host without auth",
			mutate:   func(c *MailConfig) { c.SMTP.Auth = MailAuthNone },
			wantWarn: true,
		},
		{
			name: "loopback relay without tls or auth",
			mutate: func(c *MailConfig) {
				c.SMTP.Host = "127.0.0.1"
				c.SMTP.TLS = MailTLSOff
				c.SMTP.Auth = MailAuthNone
			},
			wantWarn: false,
		},
		{
			name: "localhost relay without tls or auth",
			mutate: func(c *MailConfig) {
				c.SMTP.Host = "localhost"
				c.SMTP.TLS = MailTLSOff
				c.SMTP.Auth = MailAuthNone
			},
			wantWarn: false,
		},
		{
			name: "remote host with tls and auth",
			mutate: func(c *MailConfig) {
				c.SMTP.TLS = MailTLSRequired
				c.SMTP.Auth = MailAuthPlain
				c.SMTP.Username = "relay-user"
				c.SMTP.Password = "s3cret"
			},
			wantWarn: false,
		},
		{
			name:     "tls verification disabled",
			mutate:   func(c *MailConfig) { c.SMTP.InsecureSkipVerify = true },
			wantWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validMail()
			tt.mutate(&c)
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}

			warnings := c.Warnings()
			if tt.wantWarn && len(warnings) == 0 {
				t.Error("expected a warning, got none")
			}
			if !tt.wantWarn && len(warnings) != 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// The warnings are split into constant prose plus slog attributes rather
// than pre-formatted with %q so that a text log does not have to quote the
// values inside the message, where every handler that treats the message as
// a string attribute escapes them again. See logging.GetHandler.
func TestMailConfig_shouldKeepWarningValuesInAttrsNotTheMessage(t *testing.T) {
	c := validMail()
	c.SMTP.Host = "10.0.10.1"
	c.SMTP.TLS = MailTLSOff
	c.SMTP.Auth = MailAuthNone
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	warnings := c.Warnings()
	if len(warnings) == 0 {
		t.Fatal("expected warnings for a plaintext unauthenticated remote relay, got none")
	}

	for _, w := range warnings {
		if strings.Contains(w.Msg, `"`) {
			t.Errorf("warning message carries a quoted value, which logs back escaped: %s", w.Msg)
		}
	}
}

// Every warning names the relay it is about, so an operator running more
// than one server can tell whose configuration is being complained about
// from the log line alone.
func TestMailConfig_shouldAttachTheRelayHostToEveryWarning(t *testing.T) {
	c := validMail()
	c.SMTP.Host = "10.0.10.1"
	c.SMTP.TLS = MailTLSOff
	c.SMTP.Auth = MailAuthNone
	c.SMTP.InsecureSkipVerify = true
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for _, w := range c.Warnings() {
		if !slices.Contains(w.Attrs, any("mail.smtp.host")) {
			t.Errorf("warning %q has no mail.smtp.host attribute: %v", w.Msg, w.Attrs)
		}
	}
}

func TestMailConfig_shouldNotWarnWhenDisabled(t *testing.T) {
	c := validMail()
	c.Enabled = false
	c.SMTP.TLS = MailTLSOff
	if got := c.Warnings(); len(got) != 0 {
		t.Errorf("disabled mail produced warnings: %v", got)
	}
}
