// Package service implements the server's business logic, independent of
// the HTTP transport layer.
package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// CAPublicKeyProvider exposes the CA's public key. CAService is the
// production implementation.
type CAPublicKeyProvider interface {
	GetCAPublicKey(ctx context.Context) (string, error)
}

// CAKeyRegistryReader supplies the active CA public keys.
type CAKeyRegistryReader interface {
	ActiveKeys(ctx context.Context) ([]string, error)
}

// CAService exposes the SSH certificate authority's public key, loaded from
// the registry of announced signer keys. The CA's private key is no longer
// parsed here — it lives in the signer (which may be in-process or remote),
// and this service reads only the public half.
type CAService struct {
	httpClient *http.Client
	registry   CAKeyRegistryReader
}

// NewCAService constructs a CAService backed by the given key registry.
func NewCAService(httpClient *http.Client, registry CAKeyRegistryReader) (*CAService, error) {
	if registry == nil {
		return nil, fmt.Errorf("ca registry is required")
	}
	return &CAService{
		httpClient: httpClient,
		registry:   registry,
	}, nil
}

// GetCAPublicKey returns the CA's public keys in authorized_keys format,
// one key per line. If no keys are active, returns an error.
func (s *CAService) GetCAPublicKey(ctx context.Context) (string, error) {
	keys, err := s.registry.ActiveKeys(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve active CA keys: %w", err)
	}

	if len(keys) == 0 {
		return "", fmt.Errorf("no signer has registered a CA key yet")
	}

	return strings.Join(keys, "\n"), nil
}
