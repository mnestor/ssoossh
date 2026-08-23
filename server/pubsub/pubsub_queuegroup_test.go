//go:build integration

package pubsub

import (
	"testing"

	natslib "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
)

// TestSubjectCalculator_ShouldDeriveQueueGroups verifies that the queue-group
// derivation for competing consumers works correctly.
//
// The subjectCalculator function maps topic names to NATS subjects and queue groups:
// - "certrequest.sign" → queue group "signer" (competing consumer)
// - "certrequest.signed" → queue group "signed-listeners" (competing consumer)
// - "certrequest.wait.*" → no queue group (fan-out to all subscribers)
func TestSubjectCalculator_ShouldDeriveQueueGroups(t *testing.T) {
	tests := []struct {
		name           string
		topic          string
		expectedGroup  string
		shouldHaveGroup bool
	}{
		{
			name:            "certrequest.sign uses signer queue group",
			topic:           "certrequest.sign",
			expectedGroup:   "signer",
			shouldHaveGroup: true,
		},
		{
			name:            "certrequest.signed uses signed-listeners queue group",
			topic:           "certrequest.signed",
			expectedGroup:   "signed-listeners",
			shouldHaveGroup: true,
		},
		{
			name:            "certrequest.wait topics have no queue group",
			topic:           "certrequest.wait.test-request-id",
			expectedGroup:   "",
			shouldHaveGroup: false,
		},
		{
			name:            "other topics have no queue group",
			topic:           "some.other.topic",
			expectedGroup:   "",
			shouldHaveGroup: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail := subjectCalculator("ssoossh", tt.topic)

			if detail == nil {
				t.Fatalf("subjectCalculator returned nil")
			}

			if detail.Primary != tt.topic {
				t.Errorf("Primary subject = %q, want %q", detail.Primary, tt.topic)
			}

			hasGroup := detail.QueueGroup != ""
			if hasGroup != tt.shouldHaveGroup {
				t.Errorf("has queue group = %v, want %v (group=%q)", hasGroup, tt.shouldHaveGroup, detail.QueueGroup)
			}

			if hasGroup && detail.QueueGroup != tt.expectedGroup {
				t.Errorf("QueueGroup = %q, want %q", detail.QueueGroup, tt.expectedGroup)
			}
		})
	}
}

// TestSubjectCalculator_ConsistentDerivation verifies that the queue-group
// derivation is consistent across multiple calls (deterministic).
func TestSubjectCalculator_ConsistentDerivation(t *testing.T) {
	topic := "certrequest.sign"
	prefix := "ssoossh"

	// Call multiple times and verify consistency
	results := make([]*natslib.SubjectDetail, 10)
	for i := 0; i < 10; i++ {
		results[i] = subjectCalculator(prefix, topic)
	}

	for i := 1; i < len(results); i++ {
		if results[i].QueueGroup != results[0].QueueGroup {
			t.Errorf("inconsistent queue group derivation: call %d got %q, expected %q",
				i, results[i].QueueGroup, results[0].QueueGroup)
		}
	}
}
