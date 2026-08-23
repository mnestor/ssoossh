package signer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/certmsg"
)

// Announcer tells servers this signer's CA public key by announcing over
// pubsub on startup and periodically thereafter, and by re-announcing on
// demand when a CAKeyRequestTopic message arrives.
type Announcer struct {
	keys      CAKeySource
	publisher message.Publisher
	interval  time.Duration
}

// NewAnnouncer builds an Announcer that announces the CA public key from ks
// via publisher every interval. Run must be called to start the periodic
// announcements; Register must be called to subscribe to re-announce requests.
func NewAnnouncer(ks CAKeySource, pub message.Publisher, interval time.Duration) *Announcer {
	return &Announcer{
		keys:      ks,
		publisher: pub,
		interval:  interval,
	}
}

// Register subscribes the Announcer to CAKeyRequestTopic so it can re-announce
// when servers request it.
func (an *Announcer) Register(router *message.Router, sub message.Subscriber) {
	router.AddConsumerHandler("ca-key-announcer", certmsg.CAKeyRequestTopic, sub, an.handleRequest)
}

// handleRequest re-announces the CA public key when a request arrives.
//
// Ack semantics: the announcement is published before returning nil (ack).
// A publish failure nacks, so the server's request will retry. An
// announcement publish failure is a transport failure — the request itself
// was handled (the CA key was available, the message was well-formed), but
// the reply never made it out, so retrying is appropriate.
func (an *Announcer) handleRequest(msg *message.Message) error {
	if err := an.announce(msg.Context()); err != nil {
		// Publish failure — the request is genuinely unhandled, so nack and let
		// the router's retry middleware back off and try again.
		return fmt.Errorf("failed to publish CA key announcement: %w", err)
	}
	return nil
}

// announce publishes a single CA key announcement.
func (an *Announcer) announce(ctx context.Context) error {
	signer, err := an.keys.Signer(ctx)
	if err != nil {
		// CA key unavailable: log but continue. Run() catches this on startup;
		// handleRequest would nack on publish failure, but there's nothing to
		// publish yet. Log and skip this cycle; the next timer tick will retry.
		slog.Error("failed to get CA signer for announcement", "error", err)
		return nil
	}

	// Marshal the public key in authorized_keys format and trim whitespace to
	// produce a single line with no leading or trailing whitespace.
	pubKey := ssh.MarshalAuthorizedKey(signer.PublicKey())
	publicKeyStr := strings.TrimSpace(string(pubKey))

	announce := certmsg.CAKeyAnnounce{
		PublicKey:   publicKeyStr,
		AnnouncedAt: time.Now(),
	}

	payload, err := announce.Marshal()
	if err != nil {
		// Can't even describe the key; retrying won't help.
		//
		// not covered: certmsg.CAKeyAnnounce is a plain struct of strings
		// and times, so json.Marshal cannot fail on it.
		slog.Error("failed to encode CA key announcement", "error", err)
		return nil
	}

	return an.publisher.Publish(certmsg.CAKeyAnnounceTopic, message.NewMessage(watermill.NewUUID(), payload))
}

// Run announces the CA public key at startup, then every interval until ctx
// is canceled. Shaped to sit in bootstrap's serviceRunners alongside
// pubSub.Run. Returns nil when ctx is canceled (clean shutdown).
func (an *Announcer) Run(ctx context.Context) error {
	// Announce at startup, unless shutdown has already begun.
	//
	// The guard is not defensive tidiness. bootstrap runs this alongside
	// pubSub.Run in servicerunner, and pubSub.Run closes the watermill
	// Router the moment ctx is done. Closing the Router stops its handlers,
	// and stopping a handler closes its publisher — which for the gochannel
	// backend is the same object this announces through. So on an immediate
	// shutdown the startup announce can land on a closed pub/sub, and
	// reporting that as an error made servicerunner fail the whole server's
	// startup because it had been asked to stop.
	if ctx.Err() != nil {
		return nil
	}
	if err := an.announce(ctx); err != nil {
		// Re-checked rather than trusted from above: the close races this
		// publish, so cancellation can arrive between the guard and the
		// failure. A live context here means a real transport problem —
		// a signer that cannot announce is one no server can find — and
		// that still has to be reported.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	// Announce periodically until ctx is canceled
	ticker := time.NewTicker(an.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := an.announce(ctx); err != nil {
				// Log but continue — a transient publish failure shouldn't kill
				// the entire service runner.
				slog.Error("failed to publish CA key announcement in Run", "error", err)
			}
		}
	}
}
