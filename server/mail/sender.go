package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/mail"
	"os"

	gomail "github.com/wneessen/go-mail"

	"github.com/mnestor/ssoossh/server/config"
)

// Outgoing is a rendered message plus its recipient.
type Outgoing struct {
	// To is the recipient address, read from the users table at send time.
	To string
	Rendered
}

// Sender delivers a rendered notification. An interface so the delivery
// handler can be tested without a relay, and so a future transport (a
// webhook, a chat integration) has somewhere to attach.
type Sender interface {
	Send(ctx context.Context, msg Outgoing) error
}

// SMTPSender delivers over SMTP using github.com/wneessen/go-mail.
//
// It holds no connection between sends. A notification server sends a
// handful of messages a day in bursts of one, so a pooled connection would
// spend its life idle and then be found dead by the relay's timeout
// anyway; dialing per delivery is both simpler and more reliable here.
type SMTPSender struct {
	// host, from, and replyTo are copied out of the config rather than the
	// config being held: config.MailConfig embeds a logging block with a
	// sync.Once in it, and a struct carrying a lock must not be copied
	// around per delivery.
	host    string
	from    string
	replyTo string
	options []gomail.Option
}

// NewSMTPSender builds a sender from validated configuration. cfg must
// already have been through config.MailConfig.Validate, which is what fills
// in the defaults and resolves the password file.
func NewSMTPSender(cfg *config.MailConfig) (*SMTPSender, error) {
	options := []gomail.Option{
		gomail.WithPort(cfg.SMTP.Port),
		gomail.WithTimeout(cfg.SMTP.Timeout),
	}

	// WithTLSPolicy rather than WithTLSPortPolicy: the latter overrides the
	// configured port based on the policy, which would quietly ignore an
	// operator who wrote port 2525 for their local relay.
	switch cfg.SMTP.TLS {
	case config.MailTLSOff:
		options = append(options, gomail.WithTLSPolicy(gomail.NoTLS))
	case config.MailTLSOpportunistic:
		options = append(options, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	case config.MailTLSRequired:
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case config.MailTLSImplicit:
		options = append(options, gomail.WithSSL())
	default:
		// not covered: config.MailConfig.Validate rejects any other value
		// before a sender is built.
		return nil, fmt.Errorf("unknown mail.smtp.tls policy %q", cfg.SMTP.TLS)
	}

	if cfg.SMTP.TLS != config.MailTLSOff {
		tlsConfig, err := buildTLSConfig(&cfg.SMTP)
		if err != nil {
			return nil, err
		}
		options = append(options, gomail.WithTLSConfig(tlsConfig))
	}

	authOptions, err := authOptions(&cfg.SMTP)
	if err != nil {
		return nil, err
	}
	options = append(options, authOptions...)

	if cfg.SMTP.HELO != "" {
		options = append(options, gomail.WithHELO(cfg.SMTP.HELO))
	}

	// Build one client now so a configuration the library rejects is a
	// startup failure rather than a notification that never arrives. The
	// client itself is rebuilt per send, since it carries connection state.
	if _, err := gomail.NewClient(cfg.SMTP.Host, options...); err != nil {
		return nil, fmt.Errorf("failed to configure the SMTP client: %w", err)
	}

	return &SMTPSender{
		host:    cfg.SMTP.Host,
		from:    cfg.From,
		replyTo: cfg.ReplyTo,
		options: options,
	}, nil
}

// Send delivers one message. Called only from the notification handler's
// background goroutine — never from a request — so the time this takes is
// invisible to the browser approving a request and to the unattended job
// redeeming a code.
func (s *SMTPSender) Send(ctx context.Context, msg Outgoing) error {
	// Check the recipient before dialing: an address that cannot be used
	// is a permanent failure, and finding that out after a connection and
	// a TLS handshake wastes both.
	if _, err := mail.ParseAddress(msg.To); err != nil {
		return fmt.Errorf("unusable recipient address %q: %w", msg.To, err)
	}

	message := gomail.NewMsg()
	if err := message.From(s.from); err != nil {
		// not covered: config.MailConfig.Validate parsed From with
		// net/mail before the server finished starting.
		return fmt.Errorf("invalid mail.from %q: %w", s.from, err)
	}
	if err := message.To(msg.To); err != nil {
		// not covered: ParseAddress above already accepted this address.
		return fmt.Errorf("invalid recipient %q: %w", msg.To, err)
	}
	if s.replyTo != "" {
		if err := message.ReplyTo(s.replyTo); err != nil {
			// not covered: Validate parsed ReplyTo at startup.
			return fmt.Errorf("invalid mail.reply_to %q: %w", s.replyTo, err)
		}
	}

	message.Subject(msg.Subject)
	message.SetDate()
	message.SetMessageID()

	// Text first, HTML as the alternative: that ordering is what makes a
	// text-only reader see the plain body rather than a fallback notice.
	message.SetBodyString(gomail.TypeTextPlain, msg.Text)
	message.AddAlternativeString(gomail.TypeTextHTML, msg.HTML)

	client, err := gomail.NewClient(s.host, s.options...)
	if err != nil {
		// not covered: NewSMTPSender built a client from these same
		// options at startup.
		return fmt.Errorf("failed to build the SMTP client: %w", err)
	}

	if err := client.DialAndSendWithContext(ctx, message); err != nil {
		return fmt.Errorf("failed to deliver notification to %q: %w", msg.To, err)
	}
	return nil
}

// buildTLSConfig assembles the TLS settings for the relay connection.
func buildTLSConfig(smtp *config.SMTPConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: smtp.ServerName,
		// Opt-in only, and warned about at startup — see
		// config.MailConfig.Warnings.
		InsecureSkipVerify: smtp.InsecureSkipVerify,
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = smtp.Host
	}

	if smtp.CAFile != "" {
		pem, err := os.ReadFile(smtp.CAFile)
		if err != nil {
			// not covered: config.SMTPConfig.validate stats this file at
			// startup, so an unreadable path fails the server first.
			return nil, fmt.Errorf("failed to read mail.smtp.ca_file %q: %w", smtp.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("mail.smtp.ca_file %q contains no certificates", smtp.CAFile)
		}
		tlsConfig.RootCAs = pool
	}

	return tlsConfig, nil
}

// authOptions maps the configured mechanism onto go-mail's, and supplies
// the credentials for it.
func authOptions(smtp *config.SMTPConfig) ([]gomail.Option, error) {
	if smtp.Auth == config.MailAuthNone {
		return nil, nil
	}

	var authType gomail.SMTPAuthType
	switch smtp.Auth {
	case config.MailAuthAuto:
		authType = gomail.SMTPAuthAutoDiscover
	case config.MailAuthPlain:
		authType = gomail.SMTPAuthPlain
	case config.MailAuthLogin:
		authType = gomail.SMTPAuthLogin
	case config.MailAuthCramMD5:
		authType = gomail.SMTPAuthCramMD5
	case config.MailAuthSCRAMSHA1:
		authType = gomail.SMTPAuthSCRAMSHA1
	case config.MailAuthSCRAMSHA256:
		authType = gomail.SMTPAuthSCRAMSHA256
	case config.MailAuthXOAuth2:
		authType = gomail.SMTPAuthXOAUTH2
	default:
		// not covered: config.SMTPConfig.validate rejects any other value
		// before a sender is built.
		return nil, fmt.Errorf("unknown mail.smtp.auth mechanism %q", smtp.Auth)
	}

	return []gomail.Option{
		gomail.WithSMTPAuth(authType),
		gomail.WithUsername(smtp.Username),
		gomail.WithPassword(smtp.ResolvedPassword()),
	}, nil
}
