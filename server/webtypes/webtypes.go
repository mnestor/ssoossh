// Package webtypes holds the JSON response shapes the web UI consumes.
//
// These live in their own package rather than in server/controller for two
// reasons. First, tygo generates the frontend's TypeScript from this package
// (see tygo.yaml and frontend/src/lib/api/generated/), and it works a
// package at a time — pointing it at controller would drag handlers,
// middleware wiring, and service interfaces into its view. Second, a package
// containing nothing but wire types makes the contract easy to review: a
// change here is a change the frontend sees, and golden_test.go fails when
// one happens without the generated TypeScript and docs/openapi.yaml being
// updated to match.
//
// They are deliberately not in internal/apitypes. That package exists to stop
// the Go client and this server drifting apart; nothing in Go consumes these,
// and the only consumer is the SvelteKit frontend. See
// docs/delivery-phase3-web-api.md.
//
// The authoritative human-readable contract is docs/openapi.yaml.
package webtypes

import (
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// CurrentUserResponse is the authenticated identity, for the UI to render
// who it is acting as and decide what to show.
//
// Groups are included because the UI needs them to anticipate what the
// server will allow (see config.CertOptionsUser.RequireGroup). This is not
// a certificate — group membership never appears in one — it is the
// session's own view of itself.
type CurrentUserResponse struct {
	Subject  string   `json:"subject" validate:"required"`
	Username string   `json:"username" validate:"required"`
	Email    string   `json:"email" validate:"required"`
	Groups   []string `json:"groups" validate:"required"`
}

// CertificateOptionsResponse is one side of the requested/granted pair the
// approval page shows.
type CertificateOptionsResponse struct {
	Extensions      []string `json:"extensions" validate:"required"`
	ForceCommand    string   `json:"force_command,omitempty"`
	SourceAddresses []string `json:"source_addresses,omitempty"`
	NoTouchRequired bool     `json:"no_touch_required" validate:"required"`
}

// RequestDetailResponse is what a human is shown before approving.
//
// Requested and Granted are both present because server config trims what a
// client asks for rather than rejecting it, and the UI has to surface that
// difference before approval — a client asking for an extension the
// deployment forbids should be visibly not getting it, not silently.
//
// The Decided* fields are the request's decision-audit record (see
// model.CertificateRequestDecision) — all omitted (zero) for a request
// that hasn't been decided yet, which is the common case for a request
// being viewed. Who sees a populated one: this endpoint binds a request to
// the single identity that requested/is deciding it (see
// service.CertRequestService.Detail's bindRequester call), so a full
// snapshot of the decider's identity and connection context is never shown
// to anyone but that same person.
type RequestDetailResponse struct {
	ID            string                         `json:"id" validate:"required"`
	Type          model.CertificateType          `json:"type" validate:"required"`
	Status        model.CertificateRequestStatus `json:"status" validate:"required"`
	SourceIP      string                         `json:"source_ip" validate:"required"`
	Hostname      string                         `json:"hostname,omitempty"`
	LocalUsername string                         `json:"local_username,omitempty"`
	LocalHostname string                         `json:"local_hostname,omitempty"`
	PublicKey     string                         `json:"public_key" validate:"required"`
	Principals    []string                       `json:"principals" validate:"required"`
	ValidSeconds  int                            `json:"valid_seconds" validate:"required"`
	Requested     CertificateOptionsResponse     `json:"requested" validate:"required"`
	Granted       CertificateOptionsResponse     `json:"granted" validate:"required"`
	CreatedAt     time.Time                      `json:"created_at" validate:"required"`
	ApprovalURL   string                         `json:"approval_url" validate:"required"`
	IsOwnedByYou  bool                           `json:"is_owned_by_you" validate:"required"`
	AlreadyClosed bool                           `json:"already_closed" validate:"required"`

	DecidedByOutcome         string     `json:"decided_by_outcome,omitempty"`
	DecidedBySubject         string     `json:"decided_by_subject,omitempty"`
	DecidedByUsername        string     `json:"decided_by_username,omitempty"`
	DecidedByEmail           string     `json:"decided_by_email,omitempty"`
	DecidedByGroups          []string   `json:"decided_by_groups,omitempty"`
	DecidedByOtherAccounts   []string   `json:"decided_by_other_accounts,omitempty"`
	DecidedByServiceAccounts []string   `json:"decided_by_service_accounts,omitempty"`
	DecidedSourceIP          string     `json:"decided_source_ip,omitempty"`
	DecidedUserAgent         string     `json:"decided_user_agent,omitempty"`
	DecidedAcceptLanguage    string     `json:"decided_accept_language,omitempty"`
	DecidedForwardedFor      string     `json:"decided_forwarded_for,omitempty"`
	DecidedAt                *time.Time `json:"decided_at,omitempty"`
}

// CertificateResponse is one row of a user's issued-certificate history.
//
// The certificate itself is absent because it is never persisted — these
// are ephemeral by design (see docs/signing-pipeline.md). This is the audit
// trail, not a place to re-download one.
type CertificateResponse struct {
	ID           string                `json:"id" validate:"required"`
	Type         model.CertificateType `json:"type" validate:"required"`
	SerialNumber uint64                `json:"serial_number" validate:"required"`
	KeyID        string                `json:"key_id" validate:"required"`
	Principals   string                `json:"principals" validate:"required"`
	Fingerprint  string                `json:"public_key_fingerprint" validate:"required"`
	Hostname     string                `json:"hostname,omitempty"`
	IssuedAt     time.Time             `json:"issued_at" validate:"required"`
	ExpiresAt    time.Time             `json:"expires_at" validate:"required"`
}
