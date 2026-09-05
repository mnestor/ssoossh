// Package api is the ssoosshd HTTP API client used by client/, which may
// not import server/ directly — see docs/internals/invariants.md on package
// boundaries; this is the internal/ home for what the API consumers share.
// Built directly on net/http — see HTTPClient for why that matters to
// pam_ssoossh (github.com/mnestor/ssoossh-pam), which speaks the same API
// from its own module.
//
// The wire shapes themselves (request/response JSON bodies) live in
// internal/apitypes, not here, so server/controller can share the exact
// same type definitions instead of maintaining its own copies that could
// silently drift out of sync.
package api

import "github.com/mnestor/ssoossh/internal/apitypes"

// RequestedOptions and CertificateResult are aliases for the canonical
// wire types in internal/apitypes — kept as api.X here so existing callers
// (client/cmd, this package's own tests) don't need to import apitypes
// directly just to reference them.
type (
	RequestedOptions  = apitypes.RequestedOptions
	CertificateResult = apitypes.CertificateResult
)
