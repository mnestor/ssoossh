package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	"github.com/mnestor/ssoossh/server/mail"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/test/testdb"
)

// TestInlinePublisher_ShouldDeliverBeforeNotifyReturns is the property the
// command line relies on: the admin a sync disabled has been emailed by
// the time Notify returns, with no router running.
func TestInlinePublisher_ShouldDeliverBeforeNotifyReturns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		failures int
		wantSent int
	}{
		{name: "should send the mail when the relay takes it", wantSent: 1},
		{name: "should retry the send when the relay refuses it at first", failures: 2, wantSent: 1},
		{name: "should give up on the send when the relay keeps refusing it", failures: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			user := model.User{ID: uuid.NewString(), Subject: "sub-alice", Username: "alice", Email: "alice@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := db.Create(&user).Error; err != nil {
				t.Fatalf("create user: %v", err)
			}
			sender := &flakySender{failures: tt.failures}
			svc := NewNotificationService(db, NewNotificationHandler(db, mustRenderer(t), sender).InlinePublisher(), true, true, false)

			svc.Notify(context.Background(), notify.KindAdminAutoDisabled, user.ID, sampleAdminAutoDisabled())

			if got := len(sender.messages()); got != tt.wantSent {
				t.Errorf("sent %d messages by the time Notify returned, want %d", got, tt.wantSent)
			}
		})
	}
}

func TestInlinePublisher_ShouldRefuseATopicItHasNoHandlerFor(t *testing.T) {
	t.Parallel()

	publisher := NewNotificationHandler(testdb.New(t), mustRenderer(t), &recordingSender{}).InlinePublisher()
	if err := publisher.Publish("certrequest.sign", message.NewMessage(watermill.NewUUID(), []byte("{}"))); err == nil {
		t.Error("Publish accepted a topic other than the notification topic")
	}
	if err := publisher.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// flakySender refuses its first failures sends, then records like
// recordingSender.
type flakySender struct {
	recordingSender
	failures int
}

// Send refuses while failures remain.
func (s *flakySender) Send(ctx context.Context, msg mail.Outgoing) error {
	s.mu.Lock()
	if s.failures > 0 {
		s.failures--
		s.mu.Unlock()
		return errors.New("451 try again later")
	}
	s.mu.Unlock()
	return s.recordingSender.Send(ctx, msg)
}
