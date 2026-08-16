package service

import (
	"context"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
)

// should construct without error and always report "not implemented" for Renew and SyncPrincipals
func TestHostService(t *testing.T) {
	t.Parallel()

	svc, err := NewHostService(&config.Config{})
	if err != nil {
		t.Fatalf("NewHostService() error = %v", err)
	}

	t.Run("Renew", func(t *testing.T) {
		t.Parallel()
		if _, err := svc.Renew(context.Background(), "web-01", "existing-cert", "new-pubkey"); err == nil {
			t.Error("Renew() error = nil, want error (not implemented)")
		}
	})

	t.Run("SyncPrincipals", func(t *testing.T) {
		t.Parallel()
		if _, err := svc.SyncPrincipals(context.Background(), "web-01"); err == nil {
			t.Error("SyncPrincipals() error = nil, want error (not implemented)")
		}
	})
}
