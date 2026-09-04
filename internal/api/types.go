// Package api is the ssoosshd HTTP API client shared by client and
// pam_ssoossh (neither may import the other or server/ directly — see
// docs/internals/invariants.md on package boundaries; this is the internal/
// home for what they share). Built directly on net/http — see HTTPClient
// for why that matters to the size of pam_ssoossh.
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
