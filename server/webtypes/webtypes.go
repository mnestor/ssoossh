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

// PageMeta describes the window a paged list endpoint served, so a UI can
// render page numbers and decide whether "next" is reachable. Shared by every
// admin and auditor list; see server/utils/paging for the parameters that
// produce it.
//
// Total is the count of rows matching the search, not the count on this page:
// it is what "page 3 of 12" is computed from. It stays accurate to the
// filtered set, so a search that narrows the list narrows Total with it.
type PageMeta struct {
	// Total is how many rows match the request's filter, across all pages.
	Total int64 `json:"total" validate:"required"`

	// Limit is the page size actually served, which may be smaller than the
	// one asked for (see paging.MaxLimit).
	Limit int `json:"limit" validate:"required"`

	// Offset is how many rows were skipped to reach this page.
	Offset int `json:"offset" validate:"required"`

	// Page is the 1-based number of this page, and PageCount how many pages
	// Total fills at this size. Both are derivable from the three fields
	// above, and are sent anyway so every list UI computes them the same way
	// — including the "an empty list is page 1 of 1" boundary.
	Page      int `json:"page" validate:"required"`
	PageCount int `json:"page_count" validate:"required"`
}

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

	// Extra holds operator-configured extra fields captured at login from
	// OIDC claims (see config.OAuthFields.Extra). Each value is either a
	// string or an array of strings, reflecting the claim shape at login.
	// Missing or null values in this field are rendered as "MISSING" by key
	// ID templates, so this presence is important for debugging when
	// debugging a key ID template that expected a claim that did not arrive.
	// The frontend should display missing values visibly rather than hiding them.
	Extra map[string]any `json:"extra" validate:"required"`

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

	// NotificationEmail optionally points every notification about the
	// resulting enrollment at one address instead of fanning out to every
	// holder of the service account. Service-type requests only; ignored
	// for others. Empty means fan out, which is the default.
	//
	// Approval is where this belongs because it is the moment the approver
	// is already deciding what the enrollment is for: a team alias entered
	// here reaches the people who run the job rather than the one person
	// who clicked approve. It stays editable afterwards.
	NotificationEmail string `json:"notification_email,omitempty"`
}

// ResolveCodeRequestBody is the body of the console code-submission
// endpoint: the code a human read off a console screen, in whatever shape
// they typed it. Case, the display hyphen, stray spaces and Crockford's
// decoding aliases are all normalized server-side, so the UI never has to
// clean input before sending it.
type ResolveCodeRequestBody struct {
	Code string `json:"code" binding:"required"`
}

// ResolveCodeResponse is what a resolved console code yields: the request
// it named, and where to go next.
//
// Submitting a code claims the request for the submitting session, so this
// is a state-changing POST despite reading like a lookup — the same reason
// the approval page's first GET is state-changing.
type ResolveCodeResponse struct {
	RequestID string `json:"request_id" validate:"required"`

	// ApprovalURL is the page to send the browser to. Returned rather than
	// assembled client-side so the one definition of the path stays on the
	// server, alongside the create response's.
	ApprovalURL string `json:"approval_url" validate:"required"`
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
// certificate request's enrollment: the most recent page of it, plus how
// many rows exist in total.
//
// Bounded because it is not a small list. Codes are reusable and live as
// long as cert_options.service.enrollment_duration (a year by default), so
// an hourly cron leaves thousands of rows and a five-minute one leaves six
// figures. Returning all of them would put the whole history in one
// response body and in the DOM at once.
type EnrollmentRetrievalsResponse struct {
	Retrievals []EnrollmentRetrievalResponse `json:"retrievals" validate:"required"`

	// Total counts every logged redemption, not just the ones returned, so
	// the UI can say what it is showing a slice of rather than implying the
	// page is the whole history.
	Total int `json:"total" validate:"required"`
}

// ServiceEnrollmentResponse describes one approved service enrollment
// without its code.
//
// The code is deliberately absent and must stay that way: `service enroll`
// prints it once, the server stores it only to match a redemption against,
// and a page that handed it back would turn a browser session into a way to
// mint service certificates. What this answers instead is "what does this
// code hand out, and how long does it last" — the facts needed to decide
// whether it should be renewed or left to expire.
type ServiceEnrollmentResponse struct {
	ID string `json:"id" validate:"required"`

	// ServiceAccount is the account this code was approved for, and who
	// owns it: everyone holding the account (see
	// docs/proposals/enrollment-group-ownership.md). It is what the service
	// codes page groups by, which is why it is its own field rather than
	// left to be read out of Principals.
	ServiceAccount string `json:"service_account" validate:"required"`

	// ApprovedByUsername is who approved this code. Provenance, not
	// ownership — the reader may well not be them. Empty if that user's
	// record has since gone.
	ApprovedByUsername string `json:"approved_by_username,omitempty"`

	// CertificateRequestID is the request this enrollment was approved
	// from, and the id the retrieval log at
	// /api/certs/requests/{id}/retrievals is keyed on. Omitted for an
	// enrollment with no request linked to it.
	CertificateRequestID string `json:"certificate_request_id,omitempty"`

	// Principals is what every certificate this code produces carries. For
	// a service enrollment that is the single service account the approver
	// picked, fixed at approval time and never re-derived at redemption.
	Principals []string `json:"principals" validate:"required"`

	// KeyID is likewise fixed at approval and lands verbatim in every
	// certificate, which is what a target host's logs record.
	KeyID string `json:"key_id" validate:"required"`

	// PublicKeyFingerprint identifies the keypair the code is bound to.
	// `service retrieve` never sends a public key, so a code lifted without
	// this keypair produces nothing usable.
	//
	// Empty if the stored key could not be parsed — a display gap, not a
	// reason to withhold the rest of the row.
	PublicKeyFingerprint string `json:"public_key_fingerprint,omitempty"`

	// Options are the certificate options fixed at approval, already
	// narrowed by server config: what a redemption actually grants, not
	// what the client asked for.
	Options CertificateOptionsResponse `json:"options" validate:"required"`

	// CertificateValidSeconds is how long each redeemed certificate is
	// valid for, measured from its own redemption rather than from
	// approval. Omitted for an enrollment created before the code and
	// certificate lifetimes were split, where ExpiresAt bounded both.
	CertificateValidSeconds *int `json:"certificate_valid_seconds,omitempty"`

	// CreatedAt is when the enrollment was approved.
	CreatedAt time.Time `json:"created_at" validate:"required"`

	// ExpiresAt bounds the code, not the certificates it produces: past it
	// `service retrieve` stops redeeming, and the unattended job behind it
	// needs a fresh enrollment.
	ExpiresAt time.Time `json:"expires_at" validate:"required"`

	// FirstRedeemedAt is the first successful redemption, absent for a code
	// that has never produced a certificate.
	FirstRedeemedAt *time.Time `json:"first_redeemed_at,omitempty"`

	// LastRetrievedAt is the most recent redemption attempt, successful or
	// not. Together with RetrievalCount it is how the approver tells a code
	// still driving a cron job from one nothing has used in months.
	LastRetrievedAt *time.Time `json:"last_retrieved_at,omitempty"`

	// RetrievalCount counts every logged redemption attempt, including
	// those that failed at signing — someone held the code either way.
	RetrievalCount int `json:"retrieval_count" validate:"required"`

	// NotificationEmail is where notifications about this enrollment go
	// instead of to every holder of the service account. Empty means they
	// fan out, which is the default. Any holder may change it.
	NotificationEmail string `json:"notification_email,omitempty"`
}

// ServiceEnrollmentsResponse is the caller's own approved service
// enrollments, newest first.
type ServiceEnrollmentsResponse struct {
	Enrollments []ServiceEnrollmentResponse `json:"enrollments" validate:"required"`
}

// SetNotificationEmailRequestBody is the request to point an enrollment's
// notifications at one address, or to clear it.
type SetNotificationEmailRequestBody struct {
	// NotificationEmail is the address every notification about this
	// enrollment goes to. Empty clears it, restoring fan-out to every
	// holder of the service account.
	//
	// No omitempty: an absent field and an empty one must mean the same
	// thing here, because clearing the address is the whole reason to send
	// an empty one.
	NotificationEmail string `json:"notification_email"`
}

// AdminEnrollmentResponse describes one service enrollment from the auditor's
// and admin's perspective: the code's state, who approved it, what it grants,
// when it expires, and how it has been used.
//
// Mirrors ServiceEnrollmentResponse but adds ApprovedByUsername and
// ApprovedByEmail so the admin list can name who approved each code.
type AdminEnrollmentResponse struct {
	ID string `json:"id" validate:"required"`

	// ServiceAccount is the account this code was approved for, and who owns
	// it: everyone holding the account (see
	// docs/proposals/enrollment-group-ownership.md).
	ServiceAccount string `json:"service_account" validate:"required"`

	// ApprovedByUsername and ApprovedByEmail name the user who approved this
	// enrollment. Provenance, not ownership — the code outlives their access
	// to it and is not theirs to move.
	ApprovedByUsername string `json:"approved_by_username" validate:"required"`
	ApprovedByEmail    string `json:"approved_by_email" validate:"required"`

	// Principals is what every certificate this code produces carries.
	Principals []string `json:"principals" validate:"required"`

	// KeyID is likewise fixed at approval and lands verbatim in every
	// certificate.
	KeyID string `json:"key_id" validate:"required"`

	// PublicKeyFingerprint identifies the keypair the code is bound to.
	// Empty if the stored key could not be parsed.
	PublicKeyFingerprint string `json:"public_key_fingerprint,omitempty"`

	// Options are the certificate options fixed at approval.
	Options CertificateOptionsResponse `json:"options" validate:"required"`

	// CertificateValidSeconds is how long each redeemed certificate is valid.
	CertificateValidSeconds *int `json:"certificate_valid_seconds,omitempty"`

	// CreatedAt is when the enrollment was approved.
	CreatedAt time.Time `json:"created_at" validate:"required"`

	// ExpiresAt bounds the code.
	ExpiresAt time.Time `json:"expires_at" validate:"required"`

	// FirstRedeemedAt is the first successful redemption, absent for a code
	// that has never produced a certificate.
	FirstRedeemedAt *time.Time `json:"first_redeemed_at,omitempty"`

	// LastRetrievedAt is the most recent redemption attempt.
	LastRetrievedAt *time.Time `json:"last_retrieved_at,omitempty"`

	// RetrievalCount counts every logged redemption attempt.
	RetrievalCount int `json:"retrieval_count" validate:"required"`

	// NotificationEmail is where notifications about this enrollment go
	// instead of to every holder of the service account. Empty means they
	// fan out. Visible to auditors because it decides who hears about a
	// credential; changing it needs SOC.
	NotificationEmail string `json:"notification_email,omitempty"`
}

// AdminEnrollmentsResponse is the paged list of all service enrollments,
// visible to auditors and admins.
type AdminEnrollmentsResponse struct {
	Enrollments []AdminEnrollmentResponse `json:"enrollments" validate:"required"`
	Meta        PageMeta                  `json:"meta" validate:"required"`
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
	// TargetAccount is the local account a PAM or console request is
	// authenticating: who `sudo` is being run as, or the account typed at
	// the `login:` prompt. Empty for every other type. It is
	// reported by an unauthenticated client and never becomes a principal
	// (see model.CertificateRequest.Username), so the UI must present it as
	// what is being attempted rather than as what is being granted. Without
	// it the approver cannot see which account the sudo is for, since the
	// principals now describe the approver instead.
	TargetAccount string `json:"target_account,omitempty"`

	// The console context the request carried: which machine it claims to
	// be, through which PAM service, at which terminal, and what it
	// reported as the remote host.
	//
	// Every one of them is self-reported by an unauthenticated caller and
	// the UI must render them as claims rather than as facts — they are
	// what lets a human notice "I am at my desk, why is there a console
	// login on rack07", not what authorizes anything. A non-empty
	// RemoteHost on a console request is worth flagging outright: a real
	// console has no remote host.
	//
	// Set for PAM and console requests; empty for the others.
	Hostname   string `json:"hostname,omitempty"`
	PAMService string `json:"pam_service,omitempty"`
	TTY        string `json:"tty,omitempty"`
	RemoteHost string `json:"remote_host,omitempty"`

	// ExpiresAt is when the request stops being approvable, from its own
	// type's budget. The page counts down to it, which matters most for
	// console requests: their budget is deliberately the shortest, and an
	// approver who cannot see the clock cannot tell a slow OIDC login from
	// a request that has already died.
	ExpiresAt time.Time `json:"expires_at" validate:"required"`

	PublicKey     string                     `json:"public_key" validate:"required"`
	Principals    []string                   `json:"principals" validate:"required"`
	ValidSeconds  int                        `json:"valid_seconds" validate:"required"`
	Requested     CertificateOptionsResponse `json:"requested" validate:"required"`
	Granted       CertificateOptionsResponse `json:"granted" validate:"required"`
	CreatedAt     time.Time                  `json:"created_at" validate:"required"`
	ApprovalURL   string                     `json:"approval_url" validate:"required"`
	IsOwnedByYou  bool                       `json:"is_owned_by_you" validate:"required"`
	AlreadyClosed bool                       `json:"already_closed" validate:"required"`

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
// are ephemeral by design (see docs/internals/signing-pipeline.md). This is the audit
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

	// RetrievedSourceIP is the address the `service retrieve` call came
	// from — the machine the unattended job actually ran on. Present only
	// on a service certificate.
	//
	// It answers a different question from DecidedSourceIP below, which for
	// a service certificate is the approver's browser at enrollment time
	// and is therefore identical across every certificate the code mints.
	// This one is this certificate's own origin.
	RetrievedSourceIP string `json:"retrieved_source_ip,omitempty"`

	// RetrievedAt is when that redemption happened. Distinct from IssuedAt
	// only by the width of the signing round trip, and carried because the
	// retrieval log is timestamped by it — matching a certificate to a line
	// in that log means comparing like with like.
	RetrievedAt *time.Time `json:"retrieved_at,omitempty"`

	// EnrollmentID is the service code this certificate was redeemed from,
	// so the UI can link to it. Present only on a service certificate.
	EnrollmentID string `json:"enrollment_id,omitempty"`

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

// CertificateListAdminResponse is the payload for the admin certificate history
// endpoint, showing certificates across all users with offset pagination and metadata.
type CertificateListAdminResponse struct {
	Certificates []CertificateResponse `json:"certificates" validate:"required"`
	PageMeta     PageMeta              `json:"page_meta" validate:"required"`
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
	// Server connection settings. PublicURL is the origin browsers reach
	// the deployment at; its scheme says whether it is HTTPS.
	PublicURL string `json:"public_url" validate:"required"`
	Port      int    `json:"port" validate:"required"`

	// Database
	DBProvider string `json:"db_provider" validate:"required"`

	// OAuth/OIDC
	ProviderURL string `json:"provider_url" validate:"required"`

	// Admin authorization
	AdminRequireGroup string `json:"admin_require_group,omitempty"`
	AdminSOCGroup     string `json:"admin_soc_group,omitempty"`
	AdminAuditorGroup string `json:"admin_auditor_group,omitempty"`

	// Admin user management
	AdminContactEmail    string `json:"admin_contact_email,omitempty"`
	AdminDisabledMessage string `json:"admin_disabled_message,omitempty"`

	// Logging configuration
	LoggingLevel string `json:"logging_level" validate:"required"`

	// Certificate options
	CertUserValidDuration string   `json:"cert_user_valid_duration" validate:"required"`
	CertUserExtensions    []string `json:"cert_user_extensions" validate:"required"`
	// CertUserRequire renders cert_options.user.require in the canonical
	// condition form (e.g. `all_of(group "SSH Users", claim loc >= 20)`),
	// so an operator sees the gate that is actually applied rather than a
	// group name that no longer describes it. Empty means no gate.
	CertUserRequire          string   `json:"cert_user_require,omitempty"`
	CertServiceValidDuration string   `json:"cert_service_valid_duration" validate:"required"`
	CertServiceExtensions    []string `json:"cert_service_extensions" validate:"required"`
	CertServiceRequire       string   `json:"cert_service_require,omitempty"`
	CertPAMValidDuration     string   `json:"cert_pam_valid_duration" validate:"required"`
	CertPAMRequire           string   `json:"cert_pam_require,omitempty"`
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

// AdminUserSummary is one row in the paginated auditor user list view,
// showing identity and disable state but not detailed enrollment history.
type AdminUserSummary struct {
	// ID is the stable user identifier.
	ID string `json:"id" validate:"required"`

	// Username is the OIDC claim username, possibly changed at each login.
	Username string `json:"username" validate:"required"`

	// Email is the user's email from OIDC, possibly empty or changed at login.
	Email string `json:"email" validate:"required"`

	// Subject is the OIDC "sub" claim, stable across logins for this user.
	Subject string `json:"subject" validate:"required"`

	// DisabledAt is when an admin disabled this user. Omitted (null) if not
	// disabled.
	DisabledAt *time.Time `json:"disabled_at,omitempty"`

	// DisabledByUsername is the username of the admin that disabled this user.
	// Only populated when DisabledAt is non-null.
	DisabledByUsername string `json:"disabled_by_username,omitempty"`

	// CreatedAt is when the user first authenticated.
	CreatedAt time.Time `json:"created_at" validate:"required"`

	// UpdatedAt is when the user's identity was last refreshed at login.
	UpdatedAt time.Time `json:"updated_at" validate:"required"`
}

// AdminUsersListResponse is one page of the auditor user list, with paging info.
type AdminUsersListResponse struct {
	Users []AdminUserSummary `json:"users" validate:"required"`
	Meta  PageMeta           `json:"meta" validate:"required"`
}

// AdminUserDetail is the full details of one user for the auditor detail view,
// including identity fields, disable state, and enrollment/certificate counts.
type AdminUserDetail struct {
	// ID is the stable user identifier.
	ID string `json:"id" validate:"required"`

	// Username is the OIDC claim username.
	Username string `json:"username" validate:"required"`

	// Email is the user's email from OIDC, possibly empty.
	Email string `json:"email" validate:"required"`

	// Subject is the stable OIDC "sub" claim.
	Subject string `json:"subject" validate:"required"`

	// OtherAccounts are alternate account identifiers from OIDC, decoded
	// from the stored JSON array.
	OtherAccounts []string `json:"other_accounts" validate:"required"`

	// ServiceAccounts are service accounts from OIDC, decoded from stored JSON.
	ServiceAccounts []string `json:"service_accounts" validate:"required"`

	// ExtraFields are operator-configured extra claims, decoded from stored JSON map.
	ExtraFields map[string]any `json:"extra_fields" validate:"required"`

	// CreatedAt is when the user first authenticated.
	CreatedAt time.Time `json:"created_at" validate:"required"`

	// UpdatedAt is when the user's identity was last refreshed at login.
	UpdatedAt time.Time `json:"updated_at" validate:"required"`

	// DisabledAt is when an admin disabled this user. Omitted if not disabled.
	DisabledAt *time.Time `json:"disabled_at,omitempty"`

	// DisabledByUserID and DisabledByUsername identify the admin that
	// disabled this user. Both omitted if not disabled.
	DisabledByUserID   *string `json:"disabled_by_user_id,omitempty"`
	DisabledByUsername *string `json:"disabled_by_username,omitempty"`

	// DisabledReason is why the account was disabled, required at the API
	// and shown here because this is where it meets the person deciding
	// whether to re-enable. Omitted if not disabled.
	DisabledReason string `json:"disabled_reason,omitempty"`

	// ServiceEnrollmentCount is how many live (not expired) service
	// enrollments this user approved. Provenance, not a consequence of
	// disabling them: the codes belong to their service accounts and keep
	// working (see docs/proposals/enrollment-group-ownership.md).
	ServiceEnrollmentCount int `json:"service_enrollment_count" validate:"required"`

	// CertificateCount is how many certificates have been issued to this user.
	CertificateCount int `json:"certificate_count" validate:"required"`
}

// DisableUserConsequences describes what disabling a user does, shown in
// the confirmation dialog.
//
// What it mostly describes now is what disabling does *not* do. A service
// enrollment is owned by every holder of its service account rather than by
// the person who approved it (see
// docs/proposals/enrollment-group-ownership.md), so a disable revokes this
// person's access and nothing else: no enrollment expires, and every
// unattended job keeps running.
type DisableUserConsequences struct {
	// ServiceEnrollmentCount is how many live enrollments this user
	// approved. They are unaffected — reported so the dialog can say so
	// with a number, which is the reassurance an admin disabling a
	// colleague actually needs.
	ServiceEnrollmentCount int `json:"service_enrollment_count" validate:"required"`
}

// DisableUserRequestBody is the request to disable a user.
type DisableUserRequestBody struct {
	// Reason explains why the user is being disabled. Required and
	// server-validated (non-empty, length-capped): the next admin opening
	// this account needs to learn why it was disabled, and an optional
	// field does not get filled.
	Reason string `json:"reason" validate:"required"`
}

// ReEnableUserRequestBody is the request to re-enable a user.
type ReEnableUserRequestBody struct {
	// Reason explains why the user is being re-enabled, e.g. "cleared with
	// security, SEC-1234". Required on the same terms as the disable
	// reason, and as valuable to the person after this one.
	Reason string `json:"reason" validate:"required"`
}

// ExpireEnrollmentRequestBody is the request to expire an enrollment.
type ExpireEnrollmentRequestBody struct {
	// Reason explains why the enrollment is being expired. Required and
	// server-validated, like the user containment reasons.
	Reason string `json:"reason" validate:"required"`
}

// AuditSubjectResponse is one identity snapshot on an audit event: the
// values as they stood at event time, never a live lookup. See
// model.AuditEvent for why an audit row references nothing that can change.
type AuditSubjectResponse struct {
	// UserID is the grouping key the timelines are built on. It may be
	// empty (a system or anonymous actor) and it may no longer match any
	// row, which does not make the rest of the snapshot less true.
	UserID   string   `json:"user_id,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Username string   `json:"username,omitempty"`
	Email    string   `json:"email,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// AuditEventResponse is one administrative audit event for the UI.
type AuditEventResponse struct {
	ID        string    `json:"id" validate:"required"`
	CreatedAt time.Time `json:"created_at" validate:"required"`

	// Action is the namespaced action name, e.g. "user.disabled". The set
	// grows without a wire change, so a client must render an unknown
	// action rather than assume the list is closed.
	Action string `json:"action" validate:"required"`

	Actor  *AuditSubjectResponse `json:"actor,omitempty"`
	Target *AuditSubjectResponse `json:"target,omitempty"`
	// System marks an action taken by the server rather than a person,
	// which is how a reader tells "nobody" from "not recorded".
	System bool   `json:"system,omitempty"`
	Reason string `json:"reason,omitempty"`

	// Detail is the per-action specifics, passed through as decoded JSON so
	// a new action's fields reach the UI without a wire type change. Never
	// carries a secret.
	Detail map[string]any `json:"detail,omitempty"`
}

// AuditEventsResponse is one page of the audit stream, newest first.
type AuditEventsResponse struct {
	Events []AuditEventResponse `json:"events" validate:"required"`
	Total  int64                `json:"total"`
	// NextOffset is the offset for the following page, or 0 on the last one.
	NextOffset int `json:"next_offset,omitempty"`
}
