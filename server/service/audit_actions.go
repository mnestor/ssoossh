package service

// This file holds the audit taxonomy and nothing else, because tygo
// generates frontend/src/lib/api/generated/auditactions.ts from it (see
// tygo.yaml). The web UI's audit timeline has a test that fails for any
// action here without a sentence of its own, so a new action cannot ship
// rendering as its raw name. Keep other declarations out of this file: tygo
// would emit them too.

// AuditAction is a namespaced action name in the audit taxonomy. They are
// plain strings, in the payload and in its indexed copy in the action
// column, rather than a database enum, so the set grows without a migration.
type AuditAction string

// The taxonomy. Namespaced by subject area: authentication, certificates,
// enrollments, user containment, and privileged views.
const (
	// AuditAuthLogin records a successful login, snapshotting the groups the
	// identity carried at that moment. Since group membership is never persisted
	// (see https://mnestor.github.io/ssoossh/internals/invariants/), this is in
	// fact the only durable record of what access an identity held on a given
	// day.
	AuditAuthLogin AuditAction = "auth.login"
	// AuditAuthLoginDenied records a disabled account attempting to log in.
	// There is deliberately no logout event: sessions mostly end by expiry,
	// so an explicit logout carries too little signal to keep.
	AuditAuthLoginDenied AuditAction = "auth.login_denied"

	AuditCertRequested AuditAction = "cert.requested"
	AuditCertApproved  AuditAction = "cert.approved"
	AuditCertDenied    AuditAction = "cert.denied"
	// AuditCertCodeResolved records a console login code being typed into
	// the web UI and resolving. It is the moment a request created by an
	// unauthenticated machine acquires a named human, which is the step the
	// consent-phishing case in
	// https://mnestor.github.io/ssoossh/concepts/console-flow/ turns on: an
	// approval that follows a phone call looks exactly like one that does
	// not, except that this event names who was talked into it and which
	// machine's console they were told about.
	AuditCertCodeResolved AuditAction = "cert.code_resolved"
	// AuditCertIssued goes to the shipped log only, never the table — see
	// tableSkipped. The UI already has certificate history from the
	// certificates table, so a table copy would be pure duplication; the
	// archive line, which an incident reviewer joins against target-host
	// sshd logs, is the valuable part.
	AuditCertIssued AuditAction = "cert.issued"
	// AuditCertClaimed records the first browser opening an approval page
	// (see CertRequestService.ClaimApprovalPage). It is the moment a link
	// that was sent somewhere turns into a page in front of someone, and
	// the event a reviewer reads to learn whether a link was opened at all
	// and from what user agent, before any decision was made.
	AuditCertClaimed AuditAction = "cert.claimed"
	// AuditCertExpired records a request nobody answered within its budget.
	// System event: there is no actor, which is the point of recording it.
	AuditCertExpired AuditAction = "cert.expired"
	// AuditCertSignFailed records an approval that produced no certificate:
	// the signer refused the job, or the stranded-request sweep found the
	// row stuck in signing. Without it an approval that never became a
	// certificate reads exactly like one that did.
	AuditCertSignFailed AuditAction = "cert.sign_failed"
	// AuditCertRequestRefusedNetwork records a certificate request the
	// server refused because its source address is outside the type's
	// allowed_networks. System event: the caller has not signed in, so
	// there is no actor, and the username it sent is kept only as a claim.
	// The source is the address the server observed (through
	// http.trusted_proxies), never one the client supplied. One row per
	// refusal, so a run of attempts shows each time and source. Anyone on a
	// refused network can raise it without signing in; the row-cap sweep
	// still takes these rows oldest first like any other (see
	// SweepAuditEvents), leaving a burst to the firewall, the rate limits
	// and the shipped log.
	AuditCertRequestRefusedNetwork AuditAction = "cert.request_refused_network"

	AuditEnrollmentCodeCreated AuditAction = "enrollment.code_created"
	AuditEnrollmentRedeemed    AuditAction = "enrollment.redeemed"
	AuditEnrollmentExpired     AuditAction = "enrollment.expired"

	// AuditEnrollmentNotificationEmailSet records a change to an
	// enrollment's notification address. Audited because it redirects every
	// subsequent notification about a credential to an address of the
	// setter's choosing — an unremarkable convenience, and also the quiet
	// way to stop an account's holders hearing about their own code.
	//
	// The address itself is in the detail: it is operator-entered
	// configuration in an admin-only log, not a user secret, and an event
	// that recorded only "the address changed" would not answer the
	// question anyone reads it for.
	AuditEnrollmentNotificationEmailSet AuditAction = "enrollment.notification_email_set"

	// AuditEnrollmentReassigned is no longer emitted: group ownership
	// removed reassignment (see
	// https://mnestor.github.io/ssoossh/concepts/service-certificates/).
	// The constant stays so events recorded before that still resolve to a
	// name rather than a raw string in every reader of the log.
	AuditEnrollmentReassigned AuditAction = "enrollment.reassigned"

	AuditUserDisabled AuditAction = "user.disabled"
	AuditUserEnabled  AuditAction = "user.enabled"
	// AuditUserAutoDisabled is a system action, so it carries no actor.
	AuditUserAutoDisabled AuditAction = "user.auto_disabled"
	// AuditUserDisableRefused records a disable the server refused because
	// the caller named their own account. Actor and target are the same
	// person; the reason is the server's, not one the caller typed, and the
	// detail carries the source address the server observed. It exists so
	// the attempt shows on that account's timeline: a slog line alone is
	// invisible to the reviewer reading it.
	AuditUserDisableRefused AuditAction = "user.disable_refused"

	// Privileged detail views. List views are deliberately not audited:
	// a row per page of the user directory says nothing. If a list query
	// ever needs auditing, record it as one event carrying the search
	// parameters.
	AuditAdminUserViewed       AuditAction = "admin.user_viewed"
	AuditAdminEnrollmentViewed AuditAction = "admin.enrollment_viewed"
	// AuditAdminConfigViewed is no longer emitted. The effective-config
	// screen is read-only and gets reloaded constantly while an operator
	// works, so the event arrived several times a minute and buried the
	// decisions this log exists to record. The constant stays so events
	// recorded before that still resolve to a name rather than a raw
	// string in every reader of the log.
	AuditAdminConfigViewed AuditAction = "admin.config_viewed"
	// AuditAdminAuditViewed is one event per visit to the audit feed, not
	// one per event displayed — which settles the recursion question.
	AuditAdminAuditViewed AuditAction = "admin.audit_viewed"

	// AuditLDAPSyncTriggered is an admin running the directory sync by
	// hand. Recorded with the counts the pass produced, and with dry_run,
	// so a pass that only reported reads as one.
	AuditLDAPSyncTriggered AuditAction = "ldap.sync_triggered"

	// AuditLDAPProbed is one directory probe. The probe writes nothing,
	// but it makes the server open an outbound connection and read an
	// entry in full, so it is recorded with the filter it sent.
	AuditLDAPProbed AuditAction = "ldap.probed"

	// AuditAdminDiagnosticsRun is an admin running the deployment
	// self-checks. Like the directory probe it makes the server open an
	// outbound connection (to its own public URL), so it is recorded with
	// the worst status each check reached.
	AuditAdminDiagnosticsRun AuditAction = "admin.diagnostics_run"
)

// The NSS directory's lookup endpoints and fleet keys. The lookups
// themselves are never audited, one row per getpwnam would bury everything
// else; what is recorded is what an operator acts on: a refused source
// address, a dead key still being presented, the usage alerts, every change
// to a key, and a person choosing their login shell. The alerts are each
// raised at most once per window across every instance (see
// claimNSSAlert), so a flood records one row, not thousands.
const (
	// AuditNSSLookupForbidden is a lookup from an address outside
	// nss.source_allow_list, refused before its key was checked. The
	// detail carries the address the server observed and the key id the
	// request presented. At most hourly per address and key id.
	AuditNSSLookupForbidden AuditAction = "nss.lookup_forbidden"
	// AuditNSSKeyRevokedAttempt is a lookup presenting a revoked fleet
	// key's id. At most hourly per key; every attempt is logged at debug.
	AuditNSSKeyRevokedAttempt AuditAction = "nss.key_revoked_attempt"
	// AuditNSSKeyExpiredAttempt is a lookup presenting an expired fleet
	// key's id. At most hourly per key, like the revoked one.
	AuditNSSKeyExpiredAttempt AuditAction = "nss.key_expired_attempt"
	// AuditNSSKeySourceAlert is one key seen from more distinct source
	// addresses in a day than nss.key_source_alert allows, which suggests
	// the key has leaked beyond its fleet. At most daily per key.
	AuditNSSKeySourceAlert AuditAction = "nss.key_source_alert"
	// AuditNSSMissRateAlert is one key's not-found lookups running above
	// nss.miss_alert_rate per second over a minute: a host enumerating
	// names it does not know. Never a block. At most daily per key.
	AuditNSSMissRateAlert AuditAction = "nss.miss_rate_alert"
	// AuditNSSPrivilegedNameLookup is a lookup for a name listed in
	// nss.privileged_name_alert, which a directory entry could use to
	// shadow a host's own system account. At most daily per key and name.
	AuditNSSPrivilegedNameLookup AuditAction = "nss.privileged_name_lookup"

	// AuditNSSKeyCreated is a fleet key created. The secret is never in
	// the detail; it is shown once, to the creator.
	AuditNSSKeyCreated AuditAction = "nss.key_created"
	// AuditNSSKeyUpdated is a change to a key's description, expiry,
	// notification address or managing group, recorded with each changed
	// field's value before and after.
	AuditNSSKeyUpdated AuditAction = "nss.key_updated"
	// AuditNSSKeyRotated is a new secret issued for a key. The previous
	// one stays accepted until the detail's previous_expires_at.
	AuditNSSKeyRotated AuditAction = "nss.key_rotated"
	// AuditNSSKeyPreviousCleared is the previous secret ended before its
	// rotation grace ran out.
	AuditNSSKeyPreviousCleared AuditAction = "nss.key_previous_cleared"
	// AuditNSSKeyRevoked is a key revoked. Terminal: a revoked key is
	// never accepted again.
	AuditNSSKeyRevoked AuditAction = "nss.key_revoked"
	// AuditNSSShellChanged is a person choosing, or clearing, the login
	// shell their own POSIX account is served with.
	AuditNSSShellChanged AuditAction = "nss.shell_changed"
)
