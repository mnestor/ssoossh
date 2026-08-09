package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gin-contrib/sse"
)

// readCertificateEvent reads the Server-Sent Events stream from r —
// exactly one event, since ssoosshd's certificate-request endpoints send
// one and close (see server/controller/certrequests.go's streamOutcome) —
// and decodes it into a CertificateResult. Uses the same
// github.com/gin-contrib/sse the server encodes with, rather than
// hand-rolling a parser: it already reads r to EOF and returns every event
// found, which is exactly "wait for the stream to end" for this
// single-event case.
func readCertificateEvent(r io.Reader) (*CertificateResult, error) {
	events, err := sse.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate request stream: %w", err)
	}
	if len(events) == 0 {
		return nil, errors.New("certificate request stream closed without an event")
	}

	// Only the last event matters if the server ever sends more than one
	// (e.g. a future informational event before the final outcome).
	event := events[len(events)-1]

	var payload struct {
		Certificate string `json:"certificate"`
	}
	if data, ok := event.Data.(string); ok && data != "" {
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to decode certificate request event %q: %w", event.Event, err)
		}
	}

	return &CertificateResult{Status: event.Event, Certificate: payload.Certificate}, nil
}
