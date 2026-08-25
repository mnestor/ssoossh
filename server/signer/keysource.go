// Package signer turns approved certificate requests into signed SSH
// certificates. It consumes certmsg.SignQueueTopic and publishes results to
// certmsg.SignedTopic.
//
// This package has, and must keep, **zero database access**. That isn't a
// convenience — it's what lets the signer become a genuinely separate,
// minimally-privileged process later (see docs/dev/signer-split-deferred.md) without
// a rewrite. It deliberately imports neither gorm, nor server/service, nor
// server/config; bootstrap passes it the raw key material instead. There's a
// test (see zerodb_test.go) that fails if gorm ever appears in this
// package's dependency graph.
//
// It also performs **no policy decisions**. CertRequestService.Approve has
// already resolved everything against server config; the signer signs
// exactly what the job says. Adding a check here would create a second,
// divergent policy point.
package signer

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// CAKeySource supplies the ssh.Signer certificates are signed with. The
// indirection is the seam for the CA key eventually living somewhere other
// than the config file — an ssh-agent connection (per docs/internals/invariants.md's "CA
// key lives in an ssh-agent the server process can reach (v1)"), a PKCS#11
// token, or a cloud KMS — without touching Sign.
type CAKeySource interface {
	Signer(ctx context.Context) (ssh.Signer, error)
}

// ConfigKeySource is a CAKeySource backed by a private key parsed once at
// construction from configuration.
//
// Note that server/service.CAService parses the same key material to serve
// the CA *public* key over the API, and deliberately discards the private
// half. The two are independent on purpose: once the signer is a separate
// process (see docs/dev/signer-split-deferred.md), only the signer gets
// the private key and the webserver is configured with public keys alone.
type ConfigKeySource struct {
	signer ssh.Signer
}

// NewConfigKeySource parses a PEM-encoded private key. It takes the raw PEM
// string rather than a *config.Config so this package doesn't depend on the
// server's configuration types (see the package doc).
func NewConfigKeySource(privateKeyPEM string) (*ConfigKeySource, error) {
	if privateKeyPEM == "" {
		return nil, errors.New("no CA private key configured")
	}

	signer, err := ssh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA private key: %w", err)
	}
	return &ConfigKeySource{signer: signer}, nil
}

// Signer implements CAKeySource.
func (s *ConfigKeySource) Signer(context.Context) (ssh.Signer, error) {
	return s.signer, nil
}
