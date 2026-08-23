package signer

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/mnestor/ssoossh/server/certmsg"
)

// Handler consumes signing jobs off certmsg.SignQueueTopic and publishes
// results to certmsg.SignedTopic.
type Handler struct {
	keys        CAKeySource
	publisher   message.Publisher
	fipsEnabled bool
}

// NewHandler constructs a Handler signing with keys and replying via
// publisher. fipsEnabled is passed through to Sign as a second,
// independent FIPS-approval check on every job — defense in depth in case
// the main server process (which already checks this in
// CertRequestService.Approve) is ever compromised and jobs reach the sign
// queue directly.
func NewHandler(keys CAKeySource, publisher message.Publisher, fipsEnabled bool) *Handler {
	return &Handler{keys: keys, publisher: publisher, fipsEnabled: fipsEnabled}
}

// Register adds the sign-queue consumer to r.
//
// Deliberately a consumer handler with an explicit Publish rather than
// message.Router's publish-on-return AddHandler: the reply has to go out on
// the failure path too (a signing failure is still a result the waiting
// client needs), and doing it by hand keeps the publish-before-ack ordering
// visible — see handle.
func (h *Handler) Register(r *message.Router, subscriber message.Subscriber) {
	r.AddConsumerHandler("certrequest-signer", certmsg.SignQueueTopic, subscriber, h.handle)
}

// handle signs one job and publishes the outcome.
//
// Ack semantics: the reply is published *before* returning nil (ack), never
// after. A signing failure is a successfully handled message — a failure
// reply went out, so the client gets a terminal answer — and therefore acks;
// only a transport failure nacks, since only that leaves the job genuinely
// unhandled.
//
// This ordering is what a durable broker will need and costs nothing to get
// right now. Be
// clear-eyed about what it does *not* buy today: gochannel is in-memory and
// non-persistent (see server/pubsub.New), so a crash loses in-flight jobs
// outright regardless of ack timing. Real at-least-once redelivery arrives
// with JetStream (docs/dev/signer-split-deferred.md); until then, a
// request left stranded in "signing" is the sweep's job to clean up.
func (h *Handler) handle(msg *message.Message) error {
	var job certmsg.SigningJob
	if err := json.Unmarshal(msg.Payload, &job); err != nil {
		// Unparseable payload: there's no request ID to reply about, so
		// nobody can be told. Nacking would redeliver the same bad message
		// forever (gochannel retries immediately, with no backoff), so ack
		// and leave a log line as the only record.
		slog.Error("discarding unparseable signing job", "error", err)
		return nil
	}

	reply, err := Sign(msg.Context(), h.keys, job, h.fipsEnabled)
	if err != nil {
		slog.Error("failed to sign certificate",
			"request_id", job.RequestID,
			"type", job.Type,
			"error_code", errorCode(err),
			"error", err,
		)
		reply = certmsg.SignedReply{
			RequestID: job.RequestID,
			Type:      job.Type,
			Error:     err.Error(),
			ErrorCode: errorCode(err),
		}
	}

	payload, err := json.Marshal(reply)
	if err != nil {
		// Can't even describe the failure; retrying won't help.
		// excluded from coverage: certmsg.SignedReply is a plain struct of strings/ints, json.Marshal can't fail on it, see exclude-from-coverage.txt
		slog.Error("failed to encode signed reply", "request_id", job.RequestID, "error", err)
		return nil
	}

	if err := h.publisher.Publish(certmsg.SignedTopic, message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		// Transport failure — the job is genuinely unhandled, so nack and
		// let the router's retry middleware back off and try again.
		return fmt.Errorf("failed to publish signed reply: %w", err)
	}

	return nil
}
