package controller

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// Converters from the server's internal types to the web UI's wire shapes.
// The shapes themselves live in server/webtypes, which is what tygo reads to
// generate the frontend's TypeScript — keeping them in a package of their
// own means the generator sees wire types and nothing else. See docs/api.md
// for the wire contract.
//
// New endpoints use the {data, error} envelope from
// .claude/rules/server-api.md. The older client-facing endpoints
// (create/approve/events) return bare payloads instead; they are left alone
// because internal/api already depends on their shape.

// respondData writes payload in the success half of the {data, error}
// envelope. The error half is written by middleware.ErrorHandlerMiddleware,
// which is why handlers here return errors via c.Error rather than
// responding directly.
func respondData(c *gin.Context, payload any) {
	c.JSON(http.StatusOK, gin.H{"data": payload, "error": nil})
}

// handleError registers err on g for middleware.ErrorHandlerMiddleware to
// write into the error half of the {data, error} envelope. Callers still
// need their own `return` immediately after — this only registers the
// error, it doesn't stop the handler.
func handleError(g *gin.Context, err error) {
	_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
}

// orEmpty returns s, or a non-nil empty slice if s is nil — for wire-shape
// fields that must serialize as [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// newCurrentUserResponse converts the session identity to its wire shape.
func newCurrentUserResponse(identity *service.Identity) webtypes.CurrentUserResponse {
	return webtypes.CurrentUserResponse{
		Subject:  identity.Subject,
		Username: identity.Username,
		Email:    identity.Email,
		Groups:   orEmpty(identity.Groups),
	}
}

// newRequestDetailResponse converts a service.RequestDetail to its wire
// shape.
func newRequestDetailResponse(d *service.RequestDetail) webtypes.RequestDetailResponse {
	resp := webtypes.RequestDetailResponse{
		ID:            d.Request.ID,
		Type:          d.Request.Type,
		Status:        d.Request.Status,
		SourceIP:      d.Request.SourceIP,
		Hostname:      d.Request.Hostname,
		LocalUsername: d.Request.LocalUsername,
		LocalHostname: d.Request.LocalHostname,
		PublicKey:     d.Request.PublicKey,
		Principals:    orEmpty(d.Principals),
		ValidSeconds:  int(d.ValidDuration.Seconds()),
		Requested:     newCertificateOptionsResponse(d.Requested),
		Granted:       newCertificateOptionsResponse(d.Narrowed),
		CreatedAt:     d.Request.CreatedAt,
		ApprovalURL:   approvalURL(d.Request.ID),
		// Detail binds the request to the caller, so reaching this point at
		// all means they own it. Present as a field anyway so the UI does
		// not have to infer ownership from the absence of an error.
		IsOwnedByYou:  true,
		AlreadyClosed: d.Request.Status != model.CertificateRequestStatusPending,
	}

	if d.Decision != nil {
		setDecisionFields(&resp, d.Decision)
	}

	return resp
}

// setDecisionFields maps decision's fields onto resp's Decided* fields. Who
// ever sees a populated response is documented on
// webtypes.RequestDetailResponse's doc comment — the request-detail
// endpoint only ever shows this to the single identity bound to the
// request.
func setDecisionFields(resp *webtypes.RequestDetailResponse, decision *model.CertificateRequestDecision) {
	resp.DecidedByOutcome = string(decision.Outcome)
	resp.DecidedBySubject = decision.Subject
	resp.DecidedByUsername = decision.Username
	resp.DecidedByEmail = decision.Email
	resp.DecidedSourceIP = decision.SourceIP
	resp.DecidedUserAgent = decision.UserAgent
	resp.DecidedAcceptLanguage = decision.AcceptLanguage
	resp.DecidedForwardedFor = decision.ForwardedFor
	decidedAt := decision.DecidedAt
	resp.DecidedAt = &decidedAt

	resp.DecidedByGroups = decodeDecisionStringList("groups", decision.Groups)
	resp.DecidedByOtherAccounts = decodeDecisionStringList("other_accounts", decision.OtherAccounts)
	resp.DecidedByServiceAccounts = decodeDecisionStringList("service_accounts", decision.ServiceAccounts)
}

// decodeDecisionStringList decodes a JSON-encoded []string column from
// model.CertificateRequestDecision. A parse failure logs and returns nil
// rather than failing the whole response — this is audit data, not a
// security decision on the read path.
func decodeDecisionStringList(field, raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		slog.Error("failed to decode certificate request decision field", "field", field, "error", err)
		return nil
	}
	return out
}

// newCertificateOptionsResponse converts resolved options to their wire
// shape, normalizing nil slices to empty ones.
func newCertificateOptionsResponse(o service.RequestedOptions) webtypes.CertificateOptionsResponse {
	return webtypes.CertificateOptionsResponse{
		Extensions:      orEmpty(o.Extensions),
		ForceCommand:    o.ForceCommand,
		SourceAddresses: o.SourceAddresses,
		NoTouchRequired: o.NoTouchRequired,
	}
}

// newCertificateResponses converts rows to their wire shape, always
// returning a non-nil slice so the UI receives [] rather than null.
func newCertificateResponses(certs []model.Certificate) []webtypes.CertificateResponse {
	out := make([]webtypes.CertificateResponse, 0, len(certs))
	for _, c := range certs {
		out = append(out, webtypes.CertificateResponse{
			ID:           c.ID,
			Type:         c.Type,
			SerialNumber: c.SerialNumber,
			KeyID:        c.KeyID,
			Principals:   c.Principals,
			Fingerprint:  c.PublicKeyFingerprint,
			Hostname:     c.Hostname,
			IssuedAt:     c.IssuedAt,
			ExpiresAt:    c.ExpiresAt,
		})
	}
	return out
}
