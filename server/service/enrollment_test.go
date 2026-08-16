package service

import (
	"context"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
)

// should construct without error and always report "not implemented" for Retrieve
func TestEnrollmentService(t *testing.T) {
	t.Parallel()

	svc, err := NewEnrollmentService(&config.Config{})
	if err != nil {
		t.Fatalf("NewEnrollmentService() error = %v", err)
	}

	if _, err := svc.Retrieve(context.Background(), "some-code"); err == nil {
		t.Error("Retrieve() error = nil, want error (not implemented)")
	}
}
