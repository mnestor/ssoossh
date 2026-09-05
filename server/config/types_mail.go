package config

import (
	"fmt"
	"net"
	"net/mail"
	"os"
	"strings"
	"time"
)

// TLS transport policies for the SMTP connection, in increasing order of
// insistence. The default is MailTLSOpportunistic: it protects the common
// remote-relay case without breaking the equally common local relay that
// offers no STARTTLS at all.
const (
	// MailTLSOff never negotiates STARTTLS. Reasonable only for a relay on
	// loopback or a trusted local socket network.
	MailTLSOff = "off"

	// MailTLSOpportunistic uses STARTTLS when the server advertises it and
	// continues in plaintext when it does not. The default.
	MailTLSOpportunistic = "opportunistic"

	// MailTLSRequired fails the delivery rather than sending in plaintext.
	// The right setting for any relay reached over a network you do not
	// own — opportunistic TLS is strippable by anyone on the path.
	MailTLSRequired = "required"

	// MailTLSImplicit dials TLS directly (SMTPS, conventionally port 465)
	// instead of upgrading an established plaintext connection.
	MailTLSImplicit = "implicit"
)

// SMTP authentication mechanisms. These map onto go-mail's SMTPAuthType in
// server/mail; they are spelled in lowercase here so the config file reads
// like the rest of ssoosshd's YAML rather than like a wire protocol.
const (
	MailAuthNone         = "none"
	MailAuthAuto         = "auto"
	MailAuthPlain        = "plain"
	MailAuthLogin        = "login"
	MailAuthCramMD5      = "cram-md5"
	MailAuthSCRAMSHA1    = "scram-sha-1"
	MailAuthSCRAMSHA256  = "scram-sha-256"
	MailAuthXOAuth2      = "xoauth2"
	defaultMailTimeout   = 15 * time.Second
	defaultMailSMTPPort  = 25
	maxMailSubjectPrefix = 64
)

// mailTLSPolicies and mailAuthMechanisms are the accepted spellings, used
// both to validate and to name the alternatives in the error message.
var (
	mailTLSPolicies = []string{MailTLSOff, MailTLSOpportunistic, MailTLSRequired, MailTLSImplicit}

	mailAuthMechanisms = []string{
		MailAuthNone, MailAuthAuto, MailAuthPlain, MailAuthLogin,
		MailAuthCramMD5, MailAuthSCRAMSHA1, MailAuthSCRAMSHA256, MailAuthXOAuth2,
	}
)

// MailConfig configures outbound email notifications: whether they are sent
// at all, who they come from, where the templates live, and which relay
// carries them.
//
// Disabled is the default. A server that has never been told about a relay
// should not try to reach one, and notifications are an addition to the
// certificate flows rather than a part of them — every path that emits one
// works identically when this is off.
type MailConfig struct {
	// Enabled turns outbound mail on. With it false the notification
	// handler is not registered at all, so no event is queued and nothing
	// is rendered.
	Enabled bool `mapstructure:"enabled" default:"false"`

	// From is the envelope and header sender, e.g.
	// "ssoossh@example.com" or "ssoossh <noreply@example.com>". Required
	// when Enabled.
	From string `mapstructure:"from" default:""`

	// ReplyTo is an optional Reply-To header. Worth setting to a monitored
	// address: these messages report credential activity, and the first
	// thing a recipient does with an unexpected one is try to reply.
	ReplyTo string `mapstructure:"reply_to" example:"\"ssh-admins@example.com\""`

	// SubjectPrefix is prepended to every rendered subject, e.g.
	// "[ssoossh] ". Empty adds nothing. Templates can also write their own
	// subject in full; this exists so an operator can tag every message
	// without editing any template.
	SubjectPrefix string `mapstructure:"subject_prefix" default:""`

	// TemplateDir is an optional directory of local template overrides. A
	// file there replaces the embedded template of the same name; anything
	// absent falls back to the embedded one, so overriding a single
	// message does not mean vendoring the whole set. See
	// docs/operations/email-notifications.md.
	TemplateDir string `mapstructure:"template_dir" example:"\"/etc/ssoossh/mail-templates\""`

	// ExpiryReminderLead is how far ahead of an enrollment code's expiry the
	// service_enrollment_expiring reminders start. Zero disables the sweep
	// that sends them, and the job is not registered at all.
	//
	// Inside the window the reminder repeats weekly until the code is within
	// its final week, then daily until it expires. A 30-day lead therefore
	// sends at 30, 23, 16 and 9 days out and then every day from 7 days out;
	// the default week sends the daily ones alone.
	//
	// A week by default: long enough that someone can schedule the
	// re-enrollment rather than drop what they are doing, short enough that
	// the first message still describes a real deadline.
	//
	// Each send is claimed in the database so every instance can run the
	// sweep without any of them duplicating it. Shortening this means an
	// enrollment past the new window gets no reminder until it re-enters
	// it; lengthening it picks enrollments up at the next sweep.
	ExpiryReminderLead time.Duration `mapstructure:"expiry_reminder_lead" default:"168h"`

	// ExpiredAttemptWindow rate-limits the service_enrollment_expired_attempt
	// notification to at most one per enrollment per window. Zero disables
	// that notification entirely.
	//
	// A window rather than a one-shot because the thing it reports is a
	// retry loop: a cron job holding an expired code fails on its own
	// schedule indefinitely, and the recipient needs to keep hearing that it
	// is still happening without hearing it every five minutes. A day by
	// default, so the useful message is "this is still happening today".
	ExpiredAttemptWindow time.Duration `mapstructure:"expired_attempt_window" default:"24h"`

	// SMTP is the relay connection. See SMTPConfig.
	SMTP SMTPConfig `mapstructure:"smtp"`

	// Logging routes mail delivery logs, same shape as every other
	// component's logging block.
	Logging GenericLogging `mapstructure:"logging" default_level:"info"`
}

// SMTPConfig is the relay ssoosshd hands messages to. It covers both
// deployment shapes named in the feature request: a local relay
// (host localhost, no TLS, no auth — the mail system on the box takes it
// from there) and a remote submission service (host, port 587 or 465,
// TLS, and credentials).
type SMTPConfig struct {
	// Host is the relay hostname or address. Required when mail is enabled.
	Host string `mapstructure:"host" default:"localhost"`

	// Port is the relay port: conventionally 25 for a local relay, 587 for
	// submission with STARTTLS, 465 for implicit TLS.
	Port int `mapstructure:"port" default:"25"`

	// TLS is the transport policy — one of off, opportunistic (default),
	// required, or implicit.
	TLS string `mapstructure:"tls" default:"opportunistic"`

	// ServerName overrides the name the relay's certificate is verified
	// against. Needed only when Host is an address rather than the name on
	// the certificate.
	ServerName string `mapstructure:"server_name" example:"\"smtp.example.com\""`

	// CAFile is a PEM bundle to verify the relay's certificate against,
	// for a relay using a private CA. Empty uses the system trust store.
	CAFile string `mapstructure:"ca_file" example:"\"/etc/ssl/certs/internal-ca.pem\""`

	// InsecureSkipVerify disables verification of the relay's certificate.
	// It turns TLS into obfuscation — an attacker who can redirect the
	// connection can also present their own certificate — so it exists for
	// a lab with a self-signed relay and is warned about at startup.
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify" default:"false"`

	// Auth selects the SASL mechanism — one of none (default), auto,
	// plain, login, cram-md5, scram-sha-1, scram-sha-256, or xoauth2.
	// "auto" negotiates the strongest mechanism the relay advertises.
	Auth string `mapstructure:"auth" default:"none"`

	// Username is the SASL username. Required for every mechanism but none.
	Username string `mapstructure:"username" example:"\"ssoossh@example.com\""`

	// Password is the SASL password, or the OAuth2 token for xoauth2.
	// Prefer PasswordFile: this value is a secret sitting in a config file.
	Password string `mapstructure:"password" example:"\"\"" secret:"true"`

	// PasswordFile reads the password from a file instead, so the secret
	// can be a mounted file or a systemd credential rather than config
	// text. Takes precedence over Password when both are set. Read once at
	// startup; trailing whitespace is trimmed, since an editor's newline is
	// never part of the password.
	PasswordFile string `mapstructure:"password_file" example:"\"/run/secrets/ssoossh-smtp-password\""`

	// HELO is the name announced in the EHLO/HELO greeting. Empty lets
	// go-mail use the local hostname, which is right unless the relay
	// checks it against something specific.
	HELO string `mapstructure:"helo" example:"\"ssoossh.example.com\""`

	// Timeout bounds a single delivery's connect-and-send.
	// It bounds only the background sender, never a request: nothing a
	// browser or an unattended job waits on is behind this.
	Timeout time.Duration `mapstructure:"timeout" default:"15s"`

	// resolvedPassword is the password after PasswordFile has been read,
	// populated by Validate. Unexported so it cannot arrive from YAML and
	// cannot be serialized into the auditor-visible effective config.
	resolvedPassword string
}

// ResolvedPassword returns the SASL password after PasswordFile resolution.
// Valid only after Validate has run.
func (s *SMTPConfig) ResolvedPassword() string { return s.resolvedPassword }

// Validate checks the mail configuration and fills in defaults, so a
// misconfigured relay stops the server at startup rather than being
// discovered when the first notification silently fails to arrive. A
// disabled section is not validated at all: half-written settings under
// `enabled: false` are a deployment in progress, not an error.
func (c *MailConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.From == "" {
		return fmt.Errorf("mail.from is required when mail.enabled is true")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("mail.from %q is not a valid address: %w", c.From, err)
	}
	if c.ReplyTo != "" {
		if _, err := mail.ParseAddress(c.ReplyTo); err != nil {
			return fmt.Errorf("mail.reply_to %q is not a valid address: %w", c.ReplyTo, err)
		}
	}
	if len(c.SubjectPrefix) > maxMailSubjectPrefix {
		return fmt.Errorf("mail.subject_prefix is %d characters, over the %d limit", len(c.SubjectPrefix), maxMailSubjectPrefix)
	}
	if c.TemplateDir != "" {
		info, err := os.Stat(c.TemplateDir)
		if err != nil {
			return fmt.Errorf("mail.template_dir %q: %w", c.TemplateDir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("mail.template_dir %q is not a directory", c.TemplateDir)
		}
	}

	// Negative rejected rather than clamped: zero already means "off" for
	// both, so a negative is a typo whose intent cannot be guessed.
	if c.ExpiryReminderLead < 0 {
		return fmt.Errorf("mail.expiry_reminder_lead %s is negative: use 0 to disable the reminder", c.ExpiryReminderLead)
	}
	if c.ExpiredAttemptWindow < 0 {
		return fmt.Errorf("mail.expired_attempt_window %s is negative: use 0 to disable the notification", c.ExpiredAttemptWindow)
	}

	return c.SMTP.validate()
}

// validate checks the relay settings and fills in defaults.
func (s *SMTPConfig) validate() error {
	if s.Host == "" {
		return fmt.Errorf("mail.smtp.host is required when mail.enabled is true")
	}
	if s.Port == 0 {
		s.Port = defaultMailSMTPPort
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("mail.smtp.port %d is outside 1-65535", s.Port)
	}

	if s.TLS == "" {
		s.TLS = MailTLSOpportunistic
	}
	if !contains(mailTLSPolicies, s.TLS) {
		return fmt.Errorf("mail.smtp.tls %q is not one of %s", s.TLS, strings.Join(mailTLSPolicies, ", "))
	}

	if s.Auth == "" {
		s.Auth = MailAuthNone
	}
	if !contains(mailAuthMechanisms, s.Auth) {
		return fmt.Errorf("mail.smtp.auth %q is not one of %s", s.Auth, strings.Join(mailAuthMechanisms, ", "))
	}

	if s.CAFile != "" && s.TLS != MailTLSOff {
		if _, err := os.Stat(s.CAFile); err != nil {
			return fmt.Errorf("mail.smtp.ca_file %q: %w", s.CAFile, err)
		}
	}

	if s.Timeout < 0 {
		return fmt.Errorf("mail.smtp.timeout %s is negative", s.Timeout)
	}
	if s.Timeout == 0 {
		s.Timeout = defaultMailTimeout
	}

	return s.resolveCredentials()
}

// resolveCredentials reads PasswordFile if set and checks that the selected
// mechanism has what it needs. Done at startup rather than at send time so
// an unreadable secret file is a boot failure, not a notification that
// never arrives.
func (s *SMTPConfig) resolveCredentials() error {
	if s.Auth == MailAuthNone {
		return nil
	}

	if s.Username == "" {
		return fmt.Errorf("mail.smtp.username is required for mail.smtp.auth %q", s.Auth)
	}

	s.resolvedPassword = s.Password
	if s.PasswordFile != "" {
		data, err := os.ReadFile(s.PasswordFile)
		if err != nil {
			return fmt.Errorf("mail.smtp.password_file %q: %w", s.PasswordFile, err)
		}
		s.resolvedPassword = strings.TrimSpace(string(data))
	}

	if s.resolvedPassword == "" {
		return fmt.Errorf("mail.smtp.password or mail.smtp.password_file is required for mail.smtp.auth %q", s.Auth)
	}
	return nil
}

// Warning is one piece of startup advice, split the way slog wants it: Msg
// is prose that never varies, and every value it would otherwise have
// interpolated sits in Attrs as alternating key/value pairs ready to splat
// into slog.Warn.
//
// Kept apart rather than pre-formatted with fmt.Sprintf so the settings and
// hostnames stay machine-readable under log_json, and so a text log does
// not have to quote them inside the message: a quoted value inside a msg
// comes back out backslash-escaped by any handler that treats the message
// as a string attribute. See logging.GetHandler.
type Warning struct {
	// Msg is the constant prose, safe to group by across occurrences.
	Msg string
	// Attrs holds the varying values as slog key/value pairs.
	Attrs []any
}

// Warnings returns advice about a configuration that is valid but weaker
// than it should be. These are warnings rather than errors on purpose: TLS
// and authentication are suggested, not mandatory, because the local-relay
// deployment legitimately needs neither. Logged once at startup by
// bootstrap so a plaintext relay is a choice someone made rather than one
// they drifted into.
func (c *MailConfig) Warnings() []Warning {
	if !c.Enabled {
		return nil
	}

	var warnings []Warning

	// The exemption is loopback specifically. A relay on the same host is
	// reached over a path no one else is on, so plaintext there protects
	// nothing that isn't already lost; anywhere else the traffic crosses a
	// network, and these messages name accounts, addresses, and key IDs.
	if !isLoopbackHost(c.SMTP.Host) {
		if c.SMTP.TLS == MailTLSOff {
			warnings = append(warnings, Warning{
				Msg:   "relaying mail to a non-local host without TLS: notification content and any SMTP credentials cross the network in plaintext; set mail.smtp.tls to required or implicit",
				Attrs: []any{"mail.smtp.tls", MailTLSOff, "mail.smtp.host", c.SMTP.Host},
			})
		}
		if c.SMTP.TLS == MailTLSOpportunistic {
			warnings = append(warnings, Warning{
				Msg:   "relaying mail to a non-local host with opportunistic TLS: STARTTLS can be stripped by anyone on the path, so this is not a guarantee of encryption; set mail.smtp.tls to required",
				Attrs: []any{"mail.smtp.tls", MailTLSOpportunistic, "mail.smtp.host", c.SMTP.Host},
			})
		}
		if c.SMTP.Auth == MailAuthNone {
			warnings = append(warnings, Warning{
				Msg:   "relaying mail to a non-local host unauthenticated: the relay is accepting mail from this server without credentials, which is usually a misconfiguration on one side or the other",
				Attrs: []any{"mail.smtp.auth", MailAuthNone, "mail.smtp.host", c.SMTP.Host},
			})
		}
	}

	if c.SMTP.InsecureSkipVerify {
		warnings = append(warnings, Warning{
			Msg:   "mail.smtp.insecure_skip_verify is true: the relay's certificate is not checked, so TLS here obscures the traffic without authenticating who it is going to",
			Attrs: []any{"mail.smtp.host", c.SMTP.Host},
		})
	}

	return warnings
}

// isLoopbackHost reports whether host names this machine, the one case
// where an unencrypted, unauthenticated relay is a reasonable default
// rather than an oversight.
func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// contains reports whether needle is in haystack.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
