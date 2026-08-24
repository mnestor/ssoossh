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

	// OtherAccounts are alternate account identifiers this identity is
	// known by on target systems (see config.OAuthFields.OtherAccounts),
	// shown so a user can see every account name tied to their identity.
	// These are also the candidates, alongside Username, that the approval
	// page offers as principals for a user certificate (see
	// ApproveRequestBody.Principals).
	OtherAccounts []string `json:"other_accounts" validate:"required"`

	// ServiceAccounts are the service accounts this identity may approve
	// service certificates for (see config.OAuthFields.ServiceAccounts) —
	// the approval page's picker is populated from them.
	ServiceAccounts []string `json:"service_accounts" validate:"required"`

	// IsAuditor reports whether this session holds auditor-level access
	// (config.AdminConfig.GrantsAuditor), so the UI can show
	// auditor-only affordances like other users' retrieval logs. Display
	// only — the server re-checks on every auditor-scoped read.
	IsAuditor bool `json:"is_auditor" validate:"required"`
}

// ApproveRequestBody is the optional body of the approve endpoint. For a
// service-type request the approver must name which of their service
// accounts the certificate is for; the server validates membership and the
// chosen account becomes the certificate principal. For a user-type request
// Principals may contain the username and/or any other accounts the approver
// holds; empty/absent Principals defaults to the approver's username server-side.
// Ignored for other request types.
type ApproveRequestBody struct {
	ServiceAccount string   `json:"service_account,omitempty"`
	Principals     []string `json:"principals,omitempty"`
}

// EnrollmentRetrievalResponse is one redemption of a service enrollment
// code, for the retrieval log shown to the enrollment's approver and to
// auditors. Codes are reusable, so an enrollment accumulates these.
type EnrollmentRetrievalResponse struct {
	RetrievedAt       time.Time `json:"retrieved_at" validate:"required"`
	SourceIP          string    `json:"source_ip" validate:"required"`
	CertificateSerial uint64    `json:"certificate_serial" validate:"required"`

	// Succeeded is false for a redemption that passed code validation but
	// failed at signing — still worth surfacing: someone held the code.
	Succeeded bool `json:"succeeded" validate:"required"`
}

// EnrollmentRetrievalsResponse is the retrieval log for one service
// certificate request's enrollment.
type EnrollmentRetrievalsResponse struct {
	Retrievals []EnrollmentRetrievalResponse `json:"retrievals" validate:"required"`
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
//
// The Decided* fields are populated from the request's decision-audit record
// (see model.CertificateRequestDecision) if the certificate was issued as a
// result of an approval decision. They are omitted (zero) for certificates
// whose originating request could not be found.
type CertificateResponse struct {
	ID           string                `json:"id" validate:"required"`
	Type         model.CertificateType `json:"type" validate:"required"`
	SerialNumber uint64                `json:"serial_number" validate:"required"`
	KeyID        string                `json:"key_id" validate:"required"`
	Principals   string                `json:"principals" validate:"required"`
	Fingerprint  string                `json:"public_key_fingerprint" validate:"required"`
	IssuedAt     time.Time             `json:"issued_at" validate:"required"`
	ExpiresAt    time.Time             `json:"expires_at" validate:"required"`

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

// CertificateListResponse is the data payload for the cursor-paginated
// certificate list endpoint. Certificates are ordered newest first.
// NextCursor is the ID of the last certificate in this page, to be passed
// as the "after" parameter for the next page; it is nil when no more pages
// exist.
type CertificateListResponse struct {
	Certificates []CertificateResponse `json:"certificates" validate:"required"`
	NextCursor   *string               `json:"next_cursor,omitempty"`
}

// BrandingResponse is optional branding for the login page and web UI.
// All fields are optional; empty values mean no branding is configured.
// This endpoint is unauthenticated, so only values safe for public display
// should be included.
type BrandingResponse struct {
	// OrgName is the organization name displayed in the web UI (e.g., "Acme Corp").
	// Empty disables organization-specific branding.
	OrgName string `json:"org_name,omitempty"`

	// LogoURL is generated by the server and points to /api/branding/logo when
	// a logo is configured. Always same-origin, never an external URL.
	// Omitted entirely when no logo is configured (not set to empty string).
	LogoURL string `json:"logo_url,omitempty"`

	// LoginNotice is a plain-text message shown on the login page before authentication.
	// Empty disables the notice. Supports newlines for multi-line text.
	LoginNotice string `json:"login_notice,omitempty"`
}

// EffectiveConfigResponse is the auditor view of the server's effective
// configuration, with sensitive fields redacted. It shows what policy is
// actually in effect, useful for debugging and audit trails. The CA private
// key, client secret, cookie signing key, and database connection string
// (which may contain credentials) are redacted; other fields are included to
// give a complete operational picture.
type EffectiveConfigResponse struct {
	// Server connection and TLS settings
	ServerName string `json:"server_name" validate:"required"`
	Port       int    `json:"port" validate:"required"`
	IsHTTPS    bool   `json:"is_https" validate:"required"`

	// Database
	DBProvider string `json:"db_provider" validate:"required"`

	// OAuth/OIDC
	ProviderURL string `json:"provider_url" validate:"required"`

	// Admin authorization
	AdminRequireGroup string `json:"admin_require_group,omitempty"`
	AdminAuditorGroup string `json:"admin_auditor_group,omitempty"`

	// Logging configuration
	LoggingLevel string `json:"logging_level" validate:"required"`

	// Certificate options
	CertUserValidDuration    string   `json:"cert_user_valid_duration" validate:"required"`
	CertUserRequireGroup     string   `json:"cert_user_require_group,omitempty"`
	CertUserExtensions       []string `json:"cert_user_extensions" validate:"required"`
	CertServiceValidDuration string   `json:"cert_service_valid_duration" validate:"required"`
	CertServiceRequireGroup  string   `json:"cert_service_require_group,omitempty"`
	CertServiceExtensions    []string `json:"cert_service_extensions" validate:"required"`
	CertPAMValidDuration     string   `json:"cert_pam_valid_duration" validate:"required"`
	CertPAMRequireGroup      string   `json:"cert_pam_require_group,omitempty"`
	// CertClientTimeout is the configured budget; the two below are what it
	// derives to. Both are surfaced because an operator debugging "why did
	// my request expire" needs the effective numbers, not just the input.
	CertClientTimeout string `json:"cert_client_timeout" validate:"required"`
	CertApprovalTTL   string `json:"cert_approval_ttl" validate:"required"`
	CertSigningGrace  string `json:"cert_signing_grace" validate:"required"`
}

// VersionResponse is the build identity of the running server, rendered in
// the web UI's footer. Like BrandingResponse this endpoint is
// unauthenticated, so it carries only what the project's public releases
// already state.
type VersionResponse struct {
	// Version is the release this binary was built from, without the tag's
	// leading "v" (goreleaser and the Makefile both strip it). Untagged
	// builds report the "development" default from internal/version.
	Version string `json:"version" validate:"required"`

	// Commit is the git revision the build came from. It is what identifies
	// a "development" build, which has no release of its own to point at.
	Commit string `json:"commit" validate:"required"`

	// GithubURL is the project's source repository. Served rather than
	// hardcoded in the frontend so that a fork only has to change
	// internal/version.
	GithubURL string `json:"github_url" validate:"required"`

	// ReleaseURL points at the GitHub release matching Version. Omitted for
	// an untagged build, where there is no release page to link to.
	ReleaseURL string `json:"release_url,omitempty"`
}

// NotificationKindResponse is one notification kind on the preferences
// page: what it is, and whether this user wants it.
//
// Title and Description are served rather than hardcoded in the frontend so
// that adding a notification kind stays a server-side change — the page
// renders whatever the server lists (see server/notify).
type NotificationKindResponse struct {
	// Kind is the stable identifier, the key the update body uses.
	Kind string `json:"kind" validate:"required"`

	// Title is the short label shown beside the toggle.
	Title string `json:"title" validate:"required"`

	// Description is the sentence explaining when this one fires.
	Description string `json:"description" validate:"required"`

	// Enabled is this user's answer, or the kind's default when they have
	// never given one.
	Enabled bool `json:"enabled"`
}

// NotificationPreferencesResponse is the preferences page's whole payload.
type NotificationPreferencesResponse struct {
	// MailEnabled reports whether the server can send mail at all. False
	// means the toggles are still recorded but nothing is delivered, which
	// the page says out loud rather than leaving the user to infer.
	MailEnabled bool `json:"mail_enabled"`

	// Address is where notifications would be sent, from the users table.
	// Empty when the identity provider releases no email claim — the other
	// reason nothing arrives, and equally worth showing.
	Address string `json:"address"`

	// Kinds is every notification the server knows how to send, in a
	// stable order.
	Kinds []NotificationKindResponse `json:"kinds" validate:"required"`
}

// UpdateNotificationPreferencesBody is the preferences page's save. Only
// the kinds named are changed, so a client that knows about fewer kinds
// than the server cannot silently reset the ones it has never heard of.
type UpdateNotificationPreferencesBody struct {
	// Kinds maps a notification kind to whether it should be sent. An
	// unknown key is rejected rather than ignored: silently dropping it
	// would report success for a preference that was never stored.
	Kinds map[string]bool `json:"kinds" validate:"required"`
}
