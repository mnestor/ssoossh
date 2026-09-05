// Package api is the ssoosshd HTTP API client used by client/, which may not
// import server/ directly — see
// https://mnestor.github.io/ssoossh/internals/invariants/ on package
// boundaries; this is the internal/ home for what the API consumers share.
// Built directly on net/http rather than on a convenience library; see
// HTTPClient for why.
//
// It is not the only implementation of this API. pam_ssoossh
// (github.com/mnestor/ssoossh-pam) speaks the same endpoints from C and
// shares no code with this package, so the shapes in internal/apitypes are a
// cross-repository contract rather than an internal detail — see
// https://mnestor.github.io/ssoossh/internals/wire-types/ and docs/wire-contract.json.
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
