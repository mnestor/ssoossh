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
// https://mnestor.github.io/ssoossh/internals/wire-types/, "Go server -> web UI".
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

	// Name is the caller's human-readable name, for greeting them as a
	// person rather than as an account. Display only, and empty when
	// neither the identity provider nor the directory supplied one.
	Name string `json:"name" validate:"required"`

	// OtherAccounts are alternate account identifiers this identity is
	// known by on target systems (see config.OAuthFields.OtherAccounts),
	// shown so a user can see every account name tied to their identity.
	// These are also the candidates, alongside Username, that the approval
	// page offers as principals for a user certificate (see
	// ApproveRequestBody.Principals).
	OtherAccounts []string `json:"other_accounts" validate:"required"`

	// ServiceAccounts are the service accounts this identity holds (see
	// config.OAuthFields.ServiceAccounts). It is also what the service
	// codes page lists, so it stays the claim alone rather than the wider
	// approval set below.
	ServiceAccounts []string `json:"service_accounts" validate:"required"`

	// ApprovableServiceAccounts is what the approval page's picker is
	// populated from: ServiceAccounts, plus the caller's own accounts when
	// cert_options.service.allow_user_accounts is on. Sent as its own list
	// rather than computed in the browser so the picker cannot offer an
	// account the server would then refuse.
	ApprovableServiceAccounts []string `json:"approvable_service_accounts" validate:"required"`

	// UserOwnServiceAccounts is the subset of ApprovableServiceAccounts that
	// is the caller's own account rather than a claimed service account, so
	// the picker can say what those entries mean before one is chosen: the
	// certificate is still non-interactive, for an unattended job, and is
	// not a way to log in as yourself. Empty when
	// cert_options.service.allow_user_accounts is off.
	UserOwnServiceAccounts []string `json:"user_own_service_accounts" validate:"required"`

	// Extra holds operator-configured extra fields captured at login from
	// OIDC claims (see config.OAuthFields.Extra). Each value is either a
	// string or an array of strings, reflecting the claim shape at login.
	// Missing or null values in this field are rendered as "MISSING" by key
	// ID templates, so this presence is important for debugging when
	// debugging a key ID template that expected a claim that did not arrive.
	// The frontend should display missing values visibly rather than hiding them.
	Extra map[string]any `json:"extra" validate:"required"`

	// IsAdmin, IsSOC and IsAuditor report the access levels this session
	// holds, so the UI can show the affordances each one unlocks. The roles
	// nest — an admin holds all three, a SOC member holds SOC and auditor —
	// so more than one is true at a time and the account page names every
	// one rather than only the narrowest. Display only: the server re-checks
	// the matching Grants* rule on every scoped request, so hiding or
	// showing an affordance changes nothing about what this session can do.
	IsAdmin   bool `json:"is_admin" validate:"required"`
	IsSOC     bool `json:"is_soc" validate:"required"`
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
	RetrievedAt time.Time `json:"retrieved_at" validate:"required"`
	SourceIP    string    `json:"source_ip" validate:"required"`
	// A decimal string on the wire, same as CertificateResponse.SerialNumber
	// and for the same reason.
	CertificateSerial uint64 `json:"certificate_serial,string" tstype:"string" validate:"required"`

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

// AccountHolderResponse is one person who holds an enrollment's service
// account, and therefore can see and manage the code.
type AccountHolderResponse struct {
	UserID   string `json:"user_id" validate:"required"`
	Username string `json:"username" validate:"required"`

	// Name and Email are display fields, empty when the identity provider
	// released neither.
	Name  string `json:"name"`
	Email string `json:"email"`

	// Disabled marks a holder whose account is disabled. Listed rather than
	// omitted: the claim is still on their row and comes back with the
	// account, so dropping them would answer "who has access" with a set
	// that quietly grows again later.
	Disabled bool `json:"disabled"`

	// Own marks a holder for whom this is their own account rather than one
	// a service_accounts claim named — only possible with
	// cert_options.service.allow_user_accounts on.
	Own bool `json:"own"`
}

// AccountHoldersResponse is everyone known to hold one enrollment's service
// account.
type AccountHoldersResponse struct {
	// ServiceAccount is the account the holders were resolved for, echoed
	// back so the panel can name it without re-deriving it from principals.
	ServiceAccount string `json:"service_account" validate:"required"`

	Holders []AccountHolderResponse `json:"holders" validate:"required"`
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
	// https://mnestor.github.io/ssoossh/concepts/service-certificates/). It
	// is what the service codes page groups by, which is why it is its own
	// field rather than left to be read out of Principals.
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
	// https://mnestor.github.io/ssoossh/concepts/service-certificates/).
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

	// The rest of the host context, same trust: who invoked the service
	// (PAM_RUSER), the command line asking, the process identifiers, a
	// stable machine id, the platform, the module and its configured
	// mode, the host's clock, and the CA fingerprints the host trusts.
	// See apitypes.PAMRequestBody for each. Set for PAM and console
	// requests; the numeric ones are absent rather than zero when the
	// module did not report them.
	RequestingUser        string     `json:"requesting_user,omitempty"`
	Process               string     `json:"process,omitempty"`
	CallerUID             *int64     `json:"caller_uid,omitempty"`
	CallerGID             *int64     `json:"caller_gid,omitempty"`
	CallerPID             *int64     `json:"caller_pid,omitempty"`
	CallerPPID            *int64     `json:"caller_ppid,omitempty"`
	MachineID             string     `json:"machine_id,omitempty"`
	OS                    string     `json:"os,omitempty"`
	Client                string     `json:"client,omitempty"`
	ClientMode            string     `json:"client_mode,omitempty"`
	ClientTime            *time.Time `json:"client_time,omitempty"`
	TrustedCAFingerprints []string   `json:"trusted_ca_fingerprints,omitempty"`

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

	// What the approval granted and why: the principals selected, the
	// options after every narrowing, and the lifetime policy's own
	// explanation as a JSON document (see service.PolicyExplanation).
	// Approvals only; absent for denials and for decisions predating the
	// columns.
	DecidedPrincipals        []string                    `json:"decided_principals,omitempty"`
	DecidedGrantedOptions    *CertificateOptionsResponse `json:"decided_granted_options,omitempty"`
	DecidedPolicyExplanation string                      `json:"decided_policy_explanation,omitempty"`
}

// CertificateResponse is one row of a user's issued-certificate history.
//
// The certificate itself is absent because it is never persisted — these are
// ephemeral by design (see
// https://mnestor.github.io/ssoossh/internals/architecture/). This is the
// audit trail, not a place to re-download one.
//
// The Decided* fields are populated from the request's decision-audit record
// (see model.CertificateRequestDecision) if the certificate was issued as a
// result of an approval decision. They are omitted (zero) for certificates
// whose originating request could not be found.
type CertificateResponse struct {
	ID   string                `json:"id" validate:"required"`
	Type model.CertificateType `json:"type" validate:"required"`

	// SerialNumber is a decimal string, not a JSON number. Serials are 63
	// bits of randomness (internal/serial), so all but a vanishing fraction
	// exceed JavaScript's Number.MAX_SAFE_INTEGER (2^53-1) and a browser
	// parsing one as a number silently rounds it -- 3260700569889958163
	// reads back as 3260700569889958400. On an audit record that is a wrong
	// answer, and an unsearchable one: the serial an operator reads off
	// `ssh-keygen -L` never matches what is on screen.
	//
	// Both tags are needed. `json:",string"` decides what Go writes;
	// `tstype` decides what tygo tells the browser to expect, which it
	// otherwise infers from the Go type and gets wrong.
	SerialNumber uint64    `json:"serial_number,string" tstype:"string" validate:"required"`
	KeyID        string    `json:"key_id" validate:"required"`
	Principals   string    `json:"principals" validate:"required"`
	Fingerprint  string    `json:"public_key_fingerprint" validate:"required"`
	IssuedAt     time.Time `json:"issued_at" validate:"required"`
	ExpiresAt    time.Time `json:"expires_at" validate:"required"`

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

	// Extensions are the SSH certificate extensions the certificate was
	// signed with (permit-pty, permit-agent-forwarding, ...), decoded from
	// the audit row's JSON column rather than passed through as a string:
	// the browser should not have to parse JSON out of JSON.
	//
	// Populated by the detail endpoint alone. A list row does not display
	// them, and carrying them on every row of a hundred-row page would be
	// payload nobody reads.
	Extensions []string `json:"extensions,omitempty"`

	// CriticalOptions are the options fixed into the certificate
	// (force-command, source-address). Kept separate from Extensions
	// because sshd treats them differently: it rejects a certificate
	// carrying a critical option it does not understand, where an unknown
	// extension is ignored. Populated by the detail endpoint alone, same as
	// Extensions.
	CriticalOptions map[string]string `json:"critical_options,omitempty"`

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

	// The decision's content and reasoning; see RequestDetailResponse.
	// Populated on the detail endpoint only.
	DecidedPrincipals        []string                    `json:"decided_principals,omitempty"`
	DecidedGrantedOptions    *CertificateOptionsResponse `json:"decided_granted_options,omitempty"`
	DecidedPolicyExplanation string                      `json:"decided_policy_explanation,omitempty"`

	// What asked for this certificate, as the requester reported it, taken
	// from the decision record's snapshot rather than from the request --
	// see model.CertificateRequestDecision. Without these the history could
	// say who approved a certificate and what it granted but not which
	// machine or command it was for, which is the question an incident
	// review actually starts from.
	//
	// Every one is self-reported by an unauthenticated caller and must be
	// rendered as a claim, exactly as the approval page renders it (see
	// RequestDetailResponse). The one field here the server established
	// itself is DecidedSourceIP above, and that is the approver's, not the
	// requester's.
	//
	// ReportedUsername and ReportedHostname are the "user@host" that asked:
	// the PAM account and machine for a pam or console certificate, the
	// local client's for a user one. The rest are PAM and console only, and
	// empty on a user certificate, which has no service or terminal to
	// report.
	//
	// The pair is on a list row too, because it is what a history row leads
	// with: the row's subject is where the certificate was fetched from, and
	// the approver's identity is the same value on every row of a person's
	// own history. The remaining seven stay detail-only -- seven more strings
	// on every row is payload nobody reads.
	ReportedUsername       string `json:"reported_username,omitempty"`
	ReportedHostname       string `json:"reported_hostname,omitempty"`
	ReportedService        string `json:"reported_pam_service,omitempty"`
	ReportedTTY            string `json:"reported_tty,omitempty"`
	ReportedRemoteHost     string `json:"reported_remote_host,omitempty"`
	ReportedRequestingUser string `json:"reported_requesting_user,omitempty"`
	ReportedProcess        string `json:"reported_process,omitempty"`
	ReportedMachineID      string `json:"reported_machine_id,omitempty"`
	ReportedClient         string `json:"reported_client,omitempty"`
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

// DeniedRequestResponse is one denial in the caller's own history.
//
// Deliberately not a CertificateResponse with empty fields: a denial issues
// nothing, so it has no serial, key id, fingerprint or validity window, and
// zero values for those would put a row on the history page that reads like
// a certificate nobody can find. The client renders this shape as its own
// kind of row.
type DeniedRequestResponse struct {
	// ID is the decision's id, not a certificate's -- there is no
	// certificate. It is what the audit event for this denial carries.
	ID string `json:"id" validate:"required"`

	// CertificateRequestID is the request that was refused.
	CertificateRequestID string `json:"certificate_request_id" validate:"required"`

	// Type is what was asked for. Empty when the request row behind the
	// decision is gone: the decisions table is the permanent one by design,
	// so the client must render a row whose type it does not know.
	Type model.CertificateType `json:"type,omitempty"`

	DecidedAt time.Time `json:"decided_at" validate:"required"`

	// DecidedSourceIP is the address the denial was made from -- the
	// decider's browser, server-observed, not anything the requester
	// claimed.
	DecidedSourceIP string `json:"decided_source_ip,omitempty"`

	// ReportedUsername and ReportedHostname are the "user@host" the request
	// claimed for itself, self-reported by an unauthenticated caller and
	// never verified. They are here because they are what makes a denial
	// identifiable a month later: "I refused a console login on rack07" is
	// a memory, "I refused request 4f2a" is not.
	ReportedUsername string `json:"reported_username,omitempty"`
	ReportedHostname string `json:"reported_hostname,omitempty"`

	// The rest of the compact host-context snapshot the decision row
	// carries, and claims in exactly the same way: PAMService, TTY and
	// RemoteHost are what a PAM or console request said it was doing, and
	// Client is what a user request reported instead, since it has no PAM
	// service or terminal.
	//
	// Present because "I refused a sudo on rack07 from 10.1.2.9" is a
	// memory a month later and "I refused request 4f2a" is not. All of it
	// is copied onto the decision at decision time, so unlike the request's
	// own columns it cannot go blank later.
	PAMService string `json:"pam_service,omitempty"`
	TTY        string `json:"tty,omitempty"`
	RemoteHost string `json:"remote_host,omitempty"`
	Client     string `json:"client,omitempty"`
}

// DeniedRequestListResponse is the data payload for the cursor-paginated
// denial list. Ordered newest first. NextCursor is the id of the last
// decision in this page, passed as "after" for the next; nil when no more
// pages exist.
type DeniedRequestListResponse struct {
	Denials    []DeniedRequestResponse `json:"denials" validate:"required"`
	NextCursor *string                 `json:"next_cursor,omitempty"`
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

	// SupportEmail is the deployment's own support address, validated at
	// startup. When set, the login page's "contact your administrator" and
	// the footer's issue link both become mailto: links to it; when unset,
	// both keep their defaults. Omitted entirely when unconfigured.
	SupportEmail string `json:"support_email,omitempty"`

	// SupportLabel is the link text for SupportEmail. Omitted when unset,
	// and the client falls back to showing the address itself.
	SupportLabel string `json:"support_label,omitempty"`
}

// ConfigSetting is one leaf of the server's effective configuration.
type ConfigSetting struct {
	// Key is the dotted configuration key, e.g. "http.tls.min_version" —
	// the path an operator would write in their own config file, so a
	// value on this screen can be traced back to the line that set it.
	Key string `json:"key" validate:"required"`

	// Value is what is in effect, rendered as text. Empty means unset: an
	// empty string, a nil pointer, or an empty list or map.
	Value string `json:"value"`

	// Secret marks a key whose value is never sent. Value is the redaction
	// placeholder when a secret is configured and empty when none is, so
	// an operator can still tell whether one is set.
	Secret bool `json:"secret"`
}

// ConfigSection groups the settings under one top-level configuration key.
type ConfigSection struct {
	// Name is the top-level key the section covers ("http", "cert_options"),
	// or "server" for the switches that sit at the root of the file.
	Name string `json:"name" validate:"required"`

	// Settings are the section's leaves, in the order they are declared.
	Settings []ConfigSetting `json:"settings" validate:"required"`
}

// EffectiveConfigResponse is the auditor view of the server's effective
// configuration, with secrets redacted. It shows what policy is actually in
// effect, useful for debugging and audit trails.
//
// Every key is included, because the view is built by reflecting over the
// configuration struct rather than by listing fields: a screen whose job is
// to state what is in effect is wrong the moment a key is added and nobody
// remembers to add it here, and an operator reading it cannot tell an unset
// key from an unlisted one. The CA private key, HSM PIN, client secret,
// cookie signing key, database connection string, LDAP bind password, and
// SMTP password are redacted at their declarations (`secret:"true"`).
type EffectiveConfigResponse struct {
	Sections []ConfigSection `json:"sections" validate:"required"`
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

	// Name is the person's human-readable name, so a list of usernames can
	// be scanned by someone who thinks in names. Display only, and empty
	// when nothing supplied one.
	Name string `json:"name" validate:"required"`

	// Email is the user's email from OIDC, possibly empty or changed at login.
	Email string `json:"email" validate:"required"`

	// Subject is the unique account identifier (authentication.fields.subject,
	// "sub" by default) — the one field here that is stable across logins.
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

	// Username is the OIDC claim username. Not an identifier — it changes
	// when a person is renamed, which is what Subject exists to survive.
	Username string `json:"username" validate:"required"`

	// Email is the user's email from OIDC, possibly empty. Not an
	// identifier either, for the same reason as Username.
	Email string `json:"email" validate:"required"`

	// Subject is the unique account identifier, read from the claim named
	// by authentication.fields.subject ("sub" by default). It is the only
	// value a login is keyed by, and the only one guaranteed not to move.
	Subject string `json:"subject" validate:"required"`

	// Name is the person's human-readable name. Display only: it is not a
	// principal, not a key ID input, and never an authorization input.
	// Empty when neither the identity provider nor the directory supplied
	// one.
	Name string `json:"name" validate:"required"`

	// OtherAccounts, ServiceAccounts, ExtraFields and Name above are the
	// OIDC capture — exactly what the ID token carried at the last login,
	// as stored on the users row. They are deliberately *not* the values
	// the server acts on: a configured ldap.fields entry overrides its
	// OIDC counterpart wholesale, and DirectoryOverrides below names every
	// field where that is currently happening and what the effective value
	// is.
	//
	// Showing the OIDC half even when it is overridden is the point. An
	// operator debugging "why is this person missing a principal" needs to
	// see both sides and which one won; showing only the winner makes a
	// misconfigured claim mapping invisible.
	OtherAccounts []string `json:"other_accounts" validate:"required"`

	// ServiceAccounts are service accounts from OIDC, decoded from stored JSON.
	ServiceAccounts []string `json:"service_accounts" validate:"required"`

	// ExtraFields are operator-configured extra claims, decoded from stored JSON map.
	ExtraFields map[string]any `json:"extra_fields" validate:"required"`

	// DirectoryOverrides names each identity field the directory currently
	// supplies in place of the OIDC value, with both sides shown. Empty
	// when LDAP is disabled, unconfigured, or has never resolved this
	// person — in which case the OIDC values above are what the server
	// acts on.
	DirectoryOverrides []AdminUserOverride `json:"directory_overrides" validate:"required"`

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
	// working (see
	// https://mnestor.github.io/ssoossh/concepts/service-certificates/).
	ServiceEnrollmentCount int `json:"service_enrollment_count" validate:"required"`

	// CertificateCount is how many certificates have been issued to this user.
	CertificateCount int `json:"certificate_count" validate:"required"`

	// DisabledSource says what disabled the account: "admin", "soc" or
	// "ldap_sync". It is what makes auto-re-enable safe — the sync clears
	// only its own disables — so it belongs next to the reason rather than
	// only in the database. Omitted if not disabled, and absent on rows
	// predating the column.
	DisabledSource string `json:"disabled_source,omitempty"`

	// Groups are the persisted group memberships. Both capture paths while
	// ldap.enabled is true; OIDC only when it is false, on the same
	// reasoning as Directory below — the directory rows are frozen, so
	// they are neither shown nor used for notification fan-out until the
	// directory is switched back on.
	// Never an authorization input (see
	// https://mnestor.github.io/ssoossh/internals/invariants/): this is
	// what the server recorded, shown so an operator can see why a
	// notification reached someone, or why a group they expected is
	// missing.
	Groups []AdminUserGroup `json:"groups" validate:"required"`

	// DirectoryEnabled mirrors ldap.enabled, and is what disambiguates an
	// absent Directory. Without it, "no directory record" means either "this
	// person has never been enriched" or "the directory is switched off and
	// their record is being withheld" — two states that call for opposite
	// actions from whoever is reading the page.
	DirectoryEnabled bool `json:"directory_enabled"`

	// Directory is the user's directory bookkeeping row, absent when they
	// have never been enriched. It is what answers "why is this person
	// missing a group" from data already stored.
	//
	// Also absent whenever ldap.enabled is false, even for a user who has a
	// row. Switching the directory off stops the sync, so everything in
	// that row is frozen at whatever the last pass read, and the server no
	// longer acts on any of it — the session identity falls back to the
	// OIDC values on the next request. Presenting frozen data beside live
	// data with no way to tell them apart is how someone ends up granting
	// access on a principal list that has not been true for months.
	Directory *AdminUserDirectory `json:"directory,omitempty"`

	// OIDCFields reports, per identity destination, whether
	// authentication.fields names a claim that populates it. It is what
	// separates "the ID token carried nothing for this field" from "nothing
	// was ever asked of the ID token for this field" — two states that read
	// identically on the page and call for opposite actions.
	//
	// It also decides how a directory value is described. A field with no
	// configured claim has no OIDC side to override, so calling the
	// directory value an override there names a conflict that does not
	// exist; the directory is simply the only source.
	//
	// Keyed by destination name: "other_accounts", "service_accounts",
	// "name", and one entry per configured extra field.
	OIDCFields map[string]bool `json:"oidc_fields" validate:"required"`

	// NotificationPreferences is every notification kind the server knows
	// how to send, with what applies to this user and whether they chose
	// it. Registry order, then any stored rows for kinds no longer
	// registered.
	NotificationPreferences []AdminUserNotificationPreference `json:"notification_preferences" validate:"required"`
}

// AdminUserGroup is one persisted group membership.
type AdminUserGroup struct {
	// Name is the reduced, comparable name: a memberOf DN is reduced to its
	// CN before it is stored.
	Name string `json:"name" validate:"required"`

	// Source is "oidc" or "ldap". The two capture paths never collide and
	// a name can appear under both.
	Source string `json:"source" validate:"required"`

	// FirstSeenAt survives a refresh that keeps the membership, so "since
	// when" is answerable; LastSeenAt is the last capture that saw it.
	FirstSeenAt time.Time `json:"first_seen_at" validate:"required"`
	LastSeenAt  time.Time `json:"last_seen_at" validate:"required"`
}

// AdminUserDirectory is the user's directory bookkeeping row: where their
// entry is, what was last read from it, and whether it is currently
// resolving.
// AdminUserOverride is one identity field the directory supplies in place
// of the OIDC claim, with both values.
//
// The merge rule it reports is per field and total: a configured
// ldap.fields entry replaces its OIDC counterpart rather than being unioned
// with it, so that a principal retired in one source can actually be
// retired. That makes "which source won this field" a question with a real
// answer, and this is it.
type AdminUserOverride struct {
	// Field is the destination name: "other_accounts", "service_accounts",
	// "name", or an operator-chosen extra field.
	Field string `json:"field" validate:"required"`

	// OIDC is what the ID token supplied for this field at the last login,
	// shown even though it lost — a claim mapping that is quietly wrong is
	// invisible otherwise. Empty when the token carried nothing.
	OIDC []string `json:"oidc" validate:"required"`

	// Effective is what the directory supplies, and therefore what the
	// server actually acts on for this field.
	Effective []string `json:"effective" validate:"required"`
}

type AdminUserDirectory struct {
	// DirectoryID is the entry's unique, immutable identifier (the
	// attribute named by ldap.id_attribute), and the anchor that survives a
	// rename or a move between OUs. Empty when ldap.id_attribute is
	// unconfigured, which leaves resolution on the DN-then-filter path.
	DirectoryID string `json:"directory_id" validate:"required"`

	// DN is the entry's distinguished name from the last successful read.
	DN string `json:"dn" validate:"required"`

	// Attributes is the stored field map — the same values the login path
	// merges onto the identity, and the last-known-good cache it falls back
	// to when the directory is unreachable.
	Attributes map[string][]string `json:"attributes" validate:"required"`

	// LastSeenAt is the last successful read of the entry; LastSyncedAt is
	// the last pass that reached the directory at all. They differ exactly
	// when the entry is missing.
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`

	// FirstMissingAt is when the entry was first found to be missing, and
	// what the auto-disable threshold is measured against. Absent means the
	// entry is currently resolving.
	FirstMissingAt *time.Time `json:"first_missing_at,omitempty"`

	// ConsecutiveMisses is how many passes have observed the absence.
	// Reporting only: the disable is decided on elapsed time, since a pass
	// count measures replica count rather than absence.
	ConsecutiveMisses int `json:"consecutive_misses"`
}

// AdminUserNotificationPreference is one notification kind as it stands for
// this user: what the server would send them, and whether that is their own
// choice or the kind's registered default.
//
// Every registered kind is reported, not only the ones with a stored row.
// The list used to be the stored rows alone, which meant the common case —
// a user who has never touched the preferences page — rendered as an empty
// section that read as "notifications are off" when it meant the opposite.
// Title and Description come from the same registry the preferences page
// renders (server/notify), so the two screens name a kind identically.
type AdminUserNotificationPreference struct {
	Kind string `json:"kind" validate:"required"`

	// Title and Description are the registry's own wording. Empty for a
	// stored row whose kind is no longer registered — a downgrade or a
	// removed kind leaves rows nothing answers to, and they are still shown
	// rather than dropped, since the choice is real and still stored.
	Title       string `json:"title"`
	Description string `json:"description"`

	// Enabled is what applies: the stored choice where there is one, the
	// registered default otherwise.
	Enabled bool `json:"enabled"`

	// Default is what would apply with no stored choice, so a reader can
	// see at a glance which of these the user actually decided.
	Default bool `json:"default"`

	// Explicit reports whether the user has stored a choice for this kind.
	// False means Enabled is the registered default.
	Explicit bool `json:"explicit"`

	// Registered reports whether the kind is still in the server's
	// registry. False marks a stored row left behind by a removed kind.
	Registered bool `json:"registered"`

	// UpdatedAt is when the choice was last changed, absent when there is
	// no stored choice to have changed.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// DisableUserConsequences describes what disabling a user does, shown in
// the confirmation dialog.
//
// What it mostly describes now is what disabling does *not* do. A service
// enrollment is owned by every holder of its service account rather than by
// the person who approved it (see
// https://mnestor.github.io/ssoossh/concepts/service-certificates/), so a
// disable revokes this person's access and nothing else: no enrollment
// expires, and every unattended job keeps running.
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

// LDAPSyncRunResponse is one recorded directory sync pass.
//
// The counts are what the pass concluded; on a dry run they are what it
// would have concluded, since a dry run changes nothing but this record.
type LDAPSyncRunResponse struct {
	ID        string    `json:"id" validate:"required"`
	StartedAt time.Time `json:"started_at" validate:"required"`
	// FinishedAt is absent while the pass runs, which is also how a pass
	// that died with its process reads afterwards.
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// Trigger is "schedule" or "manual".
	Trigger string `json:"trigger" validate:"required"`
	DryRun  bool   `json:"dry_run"`

	// ActorUsername is the admin who triggered it, absent for a scheduled
	// pass. The username rather than the id, since it is displayed.
	ActorUsername string `json:"actor_username,omitempty"`

	// Instance is the host that ran it. Jobs are not leader-elected, so
	// every instance runs its own pass and this says whose log to read.
	Instance string `json:"instance,omitempty"`

	UsersSeen int `json:"users_seen"`
	Found     int `json:"found"`
	Missing   int `json:"missing"`
	Failed    int `json:"failed"`
	Disabled  int `json:"disabled"`
	Reenabled int `json:"reenabled"`

	// Error is why the pass could not run — an unreachable directory, a
	// failed bind. Empty for a pass that completed, whatever it concluded
	// about individual users.
	Error string `json:"error,omitempty"`
}

// LDAPStatusResponse answers "is the sync even running", and describes the
// directory configuration the probe console runs against. Auditor-readable:
// it names no credential.
type LDAPStatusResponse struct {
	// Enabled is false when ldap.enabled is off, in which case everything
	// below is unset and there is nothing to probe.
	Enabled bool `json:"enabled"`

	// URL, BaseDN and UserFilter are the connection and query the probe is
	// pinned to. The probe cannot be re-pointed, so these are the whole
	// target.
	URL        string `json:"url,omitempty"`
	BaseDN     string `json:"base_dn,omitempty"`
	UserFilter string `json:"user_filter,omitempty"`

	// ConfiguredAttributes are the attribute names the configured fields
	// read, which is what the console highlights in a returned entry.
	ConfiguredAttributes []string `json:"configured_attributes,omitempty"`

	// SyncIntervalSeconds is zero when the scheduled sync is off, which is
	// itself the answer to "why has nothing synced".
	SyncIntervalSeconds int `json:"sync_interval_seconds"`
	// DisableAfterSeconds is how long an entry may stay missing before the
	// user is auto-disabled. Zero means never.
	DisableAfterSeconds int `json:"disable_after_seconds"`
	// Reenable reports whether the sync clears its own disables when an
	// entry reappears.
	Reenable bool `json:"reenable"`

	// TLSInsecureSkipVerify reports that directory connections do not
	// verify the server certificate. Reported rather than silently
	// honoured: a probe that succeeds only because verification is off has
	// to say so.
	TLSInsecureSkipVerify bool `json:"tls_insecure_skip_verify"`

	// Running reports a pass in progress on the instance that answered
	// this request. Passes are not leader-elected, so another instance may
	// be running one too.
	Running bool `json:"running"`

	// LastRun is the most recent pass on any instance, absent when none has
	// ever run.
	LastRun *LDAPSyncRunResponse `json:"last_run,omitempty"`
}

// LDAPSyncRequestBody is the body of the sync-now endpoint.
type LDAPSyncRequestBody struct {
	// DryRun reads the directory and reports what the pass would do,
	// changing nothing. It is what makes the button safe to press during an
	// incident.
	DryRun bool `json:"dry_run,omitempty"`
}

// LDAPProbeBindings are the identity a template-mode filter renders against.
// Typed values are what let an admin test an entry before that person has
// ever logged in.
type LDAPProbeBindings struct {
	Username string            `json:"username,omitempty"`
	Email    string            `json:"email,omitempty"`
	Subject  string            `json:"subject,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
}

// LDAPProbeRequestBody is one probe.
//
// There is deliberately no connection here. The probe always uses the
// running ldap.url, bind credentials and base_dn; what an operator varies is
// the question, not who is asked.
type LDAPProbeRequestBody struct {
	// Mode is "template" (render Filter against the bindings, with RFC 4515
	// escaping applied) or "literal" (send Filter exactly as typed).
	// Defaults to template.
	Mode string `json:"mode,omitempty"`

	// Filter is the filter to run. Empty in template mode means the
	// configured ldap.user_filter.
	Filter string `json:"filter,omitempty"`

	// Attributes are the attribute names to request. Empty requests every
	// user attribute, which is the point of the console.
	Attributes []string `json:"attributes,omitempty"`

	// BindingSource selects where the template bindings come from: "self"
	// (the calling admin's own identity, the default), "user" (an existing
	// user named by UserID), or "custom" (the typed Bindings below).
	BindingSource string `json:"binding_source,omitempty"`

	// UserID names the user to bind against when BindingSource is "user".
	UserID string `json:"user_id,omitempty"`

	// Bindings are the typed values used when BindingSource is "custom".
	Bindings *LDAPProbeBindings `json:"bindings,omitempty"`
}

// LDAPProbeAttribute is one attribute of the entry the directory returned.
type LDAPProbeAttribute struct {
	Name   string   `json:"name" validate:"required"`
	Values []string `json:"values" validate:"required"`
	// Configured reports that a configured field reads this attribute,
	// which is what the console highlights.
	Configured bool `json:"configured"`
	// TruncatedValues is how many values the probe's own cap dropped.
	TruncatedValues int `json:"truncated_values,omitempty"`
}

// LDAPProbeEntry is the matched entry, before any mapping.
type LDAPProbeEntry struct {
	DN         string               `json:"dn" validate:"required"`
	Attributes []LDAPProbeAttribute `json:"attributes" validate:"required"`
}

// LDAPProbeSearch is one secondary search's contribution to a field.
type LDAPProbeSearch struct {
	Name string `json:"name" validate:"required"`
	// BaseDN and FilterSent are what actually went to the directory.
	BaseDN     string `json:"base_dn,omitempty"`
	FilterSent string `json:"filter_sent,omitempty"`
	// Value is the attribute read off each matched entry.
	Value   string   `json:"value,omitempty"`
	Entries int      `json:"entries"`
	Values  []string `json:"values,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// LDAPProbeField is one configured field's resolution.
type LDAPProbeField struct {
	Name string `json:"name" validate:"required"`
	// Attribute is the entry attribute the field reads, absent for a
	// search-only field.
	Attribute string `json:"attribute,omitempty"`
	// AttributePresent distinguishes an attribute that is empty from one
	// that is not on the entry at all — which is usually a typo.
	AttributePresent bool     `json:"attribute_present"`
	AttributeValues  []string `json:"attribute_values,omitempty"`

	Searches []LDAPProbeSearch `json:"searches,omitempty"`
	// Values is what the field resolved to, as the login path would
	// compute it.
	Values []string `json:"values" validate:"required"`
	Error  string   `json:"error,omitempty"`
}

// LDAPProbeMerge is one field's fate at the merge stage.
type LDAPProbeMerge struct {
	Name string `json:"name" validate:"required"`
	// Action is "override", "persist-groups" or "extra".
	Action string `json:"action" validate:"required"`
	// Kept and Dropped split the values by the group allowlist. Dropped is
	// only ever non-empty for the group field.
	Kept    []string `json:"kept,omitempty"`
	Dropped []string `json:"dropped,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// LDAPProbeSuggestion is a config block that would map something the probe
// found and the configuration ignores. A suggestion, not a decision.
type LDAPProbeSuggestion struct {
	Reason string `json:"reason" validate:"required"`
	YAML   string `json:"yaml" validate:"required"`
}

// LDAPProbeResponse is everything one probe learned, in the three stages the
// login path runs: the entry as returned, the field mapping, then the merge
// and allowlist.
//
// Nothing here was written. No user_ldap row, no group rows, no miss
// windows, no auto-disable.
type LDAPProbeResponse struct {
	// BaseDN, FilterSent, Mode and Attributes are the request as it went
	// out, so an operator sees the rendered filter rather than the
	// template.
	BaseDN     string   `json:"base_dn" validate:"required"`
	FilterSent string   `json:"filter_sent" validate:"required"`
	Mode       string   `json:"mode" validate:"required"`
	Attributes []string `json:"attributes" validate:"required"`

	// Matched is how many entries the filter found, capped by the probe.
	// The login path refuses anything but exactly one.
	Matched int `json:"matched"`

	// IDAttribute echoes ldap.id_attribute and DirectoryID is what it
	// resolved to on the matched entry — the value that would be stored as
	// the re-anchoring identifier. Both empty when it is unconfigured, in
	// which case Suggestions names a candidate the entry actually carries.
	IDAttribute string `json:"id_attribute,omitempty"`
	DirectoryID string `json:"directory_id,omitempty"`

	Entry       *LDAPProbeEntry       `json:"entry,omitempty"`
	Fields      []LDAPProbeField      `json:"fields,omitempty"`
	Merge       []LDAPProbeMerge      `json:"merge,omitempty"`
	Suggestions []LDAPProbeSuggestion `json:"suggestions,omitempty"`

	ElapsedMS int `json:"elapsed_ms"`
	TimeoutMS int `json:"timeout_ms"`

	// TLSInsecureSkipVerify reports that the connection did not verify the
	// directory certificate.
	TLSInsecureSkipVerify bool `json:"tls_insecure_skip_verify"`

	// Wrote is always false and is serialized anyway: the guarantee is part
	// of the response, not only of the documentation.
	Wrote bool `json:"wrote"`
}

// IdentityEchoStartResponse is where to send the browser to see a fresh ID
// token. The echo re-authenticates rather than remembering: the server keeps
// only the claims the configuration maps, so there is no stored copy of the
// rest to show.
type IdentityEchoStartResponse struct {
	// AuthorizationURL carries prompt=login, so the provider issues a fresh
	// token rather than replaying its own session.
	AuthorizationURL string `json:"authorization_url" validate:"required"`
}

// ClaimMappingResponse says which claim each configured field reads, so an
// echo can be annotated against the configuration rather than printed raw.
type ClaimMappingResponse struct {
	// Subject names the claim the unique account identifier is read from,
	// and is the one to check first: every other mapping can be wrong and
	// be corrected later, while a subject claim that varies between logins
	// forks the person's certificate history into a new account each time.
	Subject         string `json:"subject,omitempty"`
	Username        string `json:"username,omitempty"`
	Name            string `json:"name,omitempty"`
	Groups          string `json:"groups,omitempty"`
	OtherAccounts   string `json:"other_accounts,omitempty"`
	ServiceAccounts string `json:"service_accounts,omitempty"`
	Email           string `json:"email,omitempty"`

	// Extra maps each configured extra field name to the claim it reads.
	Extra map[string]string `json:"extra,omitempty"`
}

// IdentityEchoPayload is one echo result: the decoded ID token, annotated
// against the configuration.
//
// It is handed to the page in the redirect fragment rather than returned by
// an endpoint, because a fragment never reaches the server. Nothing about
// this is stored: not in the database, not in the session, not in a server
// log. It exists on the page that rendered it and nowhere else.
type IdentityEchoPayload struct {
	// Claims is the decoded ID token, in full.
	Claims map[string]any `json:"claims" validate:"required"`

	// Mapping is what the configuration reads out of it.
	Mapping ClaimMappingResponse `json:"mapping" validate:"required"`

	// Suggestions are config lines that would capture a claim nothing
	// currently reads. Suggestions, not decisions.
	Suggestions []ClaimSuggestion `json:"suggestions,omitempty"`

	// IssuedAt is when the echo was produced, so a page left open is
	// visibly stale rather than quietly so.
	IssuedAt time.Time `json:"issued_at" validate:"required"`
}

// ClaimSuggestion is the config line that would capture one unmapped claim.
type ClaimSuggestion struct {
	// Claim is the claim name nothing currently reads.
	Claim string `json:"claim" validate:"required"`
	// Reason says why it is worth naming — a numeric value a policy
	// condition could gate on, say.
	Reason string `json:"reason" validate:"required"`
	// YAML is the block to add, ready to paste and review.
	YAML string `json:"yaml" validate:"required"`
}

// DiagnosticCheckResult is one deployment self-check on the admin
// diagnostics page: an identifier, a severity, a headline, the individual
// observations, and the fix. See server/service's DiagnosticsService.
type DiagnosticCheckResult struct {
	// ID is a stable machine key for the check, e.g. "reachability".
	ID string `json:"id" validate:"required"`

	// Title is the human name of the check.
	Title string `json:"title" validate:"required"`

	// Status is the worst finding's severity: "ok", "warn", "critical", or
	// "skipped".
	Status string `json:"status" validate:"required"`

	// Summary is the one-line headline for the check.
	Summary string `json:"summary" validate:"required"`

	// Findings are the individual observations, already phrased for an
	// operator. Empty when the check found nothing to report.
	Findings []string `json:"findings"`

	// Remediation is the concrete fix. Empty when the status is "ok".
	Remediation string `json:"remediation,omitempty"`
}

// DiagnosticsResponse is the result of one run of the admin diagnostics
// self-checks (edge headers, CORS, proxy trust, reachability).
type DiagnosticsResponse struct {
	// PublicOrigin is the URL the edge checks probed, echoed so the operator
	// can confirm the run tested what they expected. Empty when
	// http.public_url is not set.
	PublicOrigin string `json:"public_origin,omitempty"`

	// Checks are the individual results, in display order.
	Checks []DiagnosticCheckResult `json:"checks"`
}
