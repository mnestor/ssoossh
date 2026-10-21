// Package notify holds the catalogue of notification kinds ssoosshd can
// send, the payload each one carries, and the queue message that moves one
// from the code that observed the event to the code that delivers it.
//
// It deliberately depends on nothing but the standard library — no gorm, no
// config, no go-mail — for the same reason server/certmsg does: the
// registry is read by three unrelated consumers (server/mail renders from
// it, server/service resolves preferences against it, server/controller
// serves it to the preferences UI), and any of those importing another
// would be a cycle waiting to happen.
//
// Adding a notification kind is meant to be a small, local edit. See
// https://mnestor.github.io/ssoossh/operations/email-notifications/ ("Adding
// a notification kind") — in short: a Kind constant, a payload struct in
// payloads.go, a Definition in the registry below, and the two template
// files. Everything else (preferences storage and UI, the docs table,
// delivery) is driven off the registry and needs no change.
package notify

// Kind identifies one notification type. The string value is persisted in
// notification_preferences.kind and appears in the web UI's preferences
// payload, so renaming one is a migration, not a rename.
type Kind string

const (
	// KindServiceEnrollmentCreated fires when a service certificate
	// request is approved and an enrollment code is minted.
	KindServiceEnrollmentCreated Kind = "service_enrollment_created"

	// KindServiceEnrollmentRedeemed fires on every `service retrieve` that
	// redeems an enrollment code, successful or not.
	KindServiceEnrollmentRedeemed Kind = "service_enrollment_redeemed"

	// KindServiceEnrollmentExpiring fires while an enrollment's code is
	// within mail.expiry_reminder_lead of expiring: weekly, then daily over
	// the final week. The only kind not emitted from an event path, because
	// the event is the absence of one: a scheduled sweep finds it instead.
	KindServiceEnrollmentExpiring Kind = "service_enrollment_expiring"

	// KindServiceEnrollmentExpiredAttempt fires when `service retrieve`
	// presents a code that has expired — a forgotten job now failing on
	// schedule, or someone replaying a credential that should no longer
	// exist. Rate-limited to one per enrollment per
	// mail.expired_attempt_window.
	KindServiceEnrollmentExpiredAttempt Kind = "service_enrollment_expired_attempt"

	// KindUserCertificateIssued fires when an interactive user certificate
	// is signed. Default off: one per login would make every existing
	// deployment noisy the day it upgrades.
	KindUserCertificateIssued Kind = "user_certificate_issued"

	// KindPAMCertificateIssued fires when a PAM certificate is signed for a
	// local `sudo`/`su`. Separate from the user kind, not a type field on
	// one, so a user who runs `sudo` forty times a day and logs in twice can
	// keep the login signal without drowning in the other.
	KindPAMCertificateIssued Kind = "pam_certificate_issued"

	// KindConsoleCertificateIssued fires when a console certificate is
	// signed for an interactive login on a machine with no browser. Its own
	// kind for the same reason PAM has one: a login session is a different
	// event from a `sudo`, and the request was made by an unauthenticated
	// machine and approved by a typed code, which makes "was this you?"
	// the question that matters most for this type.
	KindConsoleCertificateIssued Kind = "console_certificate_issued"

	// KindAdminAutoDisabled fires when the directory sync disables a user
	// who counts as an admin: their most recent login inside the sync's
	// look-back window carried a group the current config maps to admin.
	// It goes to that admin, and it is always sent. An admin whose account
	// the sync shut off has no session left to discover it in, so this is
	// the one message a preference must not be able to silence. Never sent
	// for a disable an operator made by hand.
	KindAdminAutoDisabled Kind = "admin_auto_disabled"

	// KindUserInfoPaused fires once when the server pauses its calls to the
	// OIDC UserInfo endpoint after a run of failures (see
	// config.AuthConfig.UserInfo): logins carry on with the ID token's
	// claims alone, so a claim only UserInfo sends, groups included, is
	// missing from every login until it resumes. It goes to every admin and
	// is always sent: nothing else tells anyone a login is quietly carrying
	// less than it should.
	KindUserInfoPaused Kind = "userinfo_paused"

	// KindUserInfoResumed fires once when a probe call succeeds and the
	// server resumes calling the UserInfo endpoint. Every admin, always
	// sent, so the pause mail never stands as the last word.
	KindUserInfoResumed Kind = "userinfo_resumed"

	// KindNSSKeyExpiring fires mail.expiry_reminder_lead before a fleet key
	// stops being accepted: weekly inside that window, then daily over the
	// final week. It goes to the key's notification address, not to a user.
	KindNSSKeyExpiring Kind = "nss_key_expiring"

	// KindNSSKeyRotationCleanup fires after mail.rotation_quiet_period
	// following a rotation, then daily while any address still presents the
	// previous secret, until the grace period ends. It lists the addresses
	// that have not switched yet.
	KindNSSKeyRotationCleanup Kind = "nss_key_rotation_cleanup"

	// KindNSSKeyExpiredAttempt fires when a host presents an expired fleet
	// key. At most once per key per mail.expired_attempt_window.
	KindNSSKeyExpiredAttempt Kind = "nss_key_expired_attempt"

	// KindNSSKeyRevokedAttempt fires when a host presents a revoked fleet
	// key. At most once per key per mail.expired_attempt_window.
	KindNSSKeyRevokedAttempt Kind = "nss_key_revoked_attempt"

	// KindNSSKeyRotated fires at once when a fleet key is rotated.
	KindNSSKeyRotated Kind = "nss_key_rotated"

	// KindNSSKeyRevoked fires at once when a fleet key is revoked.
	KindNSSKeyRevoked Kind = "nss_key_revoked"

	// KindNSSIDChangePending fires when the directory gives stored NSS
	// accounts or groups a different uid or gid, which the server holds
	// until an admin accepts it. One message per admin per batch: what one
	// sync run made pending, or what on-demand and login reads made pending
	// since the last send. It lists only what is new.
	KindNSSIDChangePending Kind = "nss_id_change_pending"

	// KindNSSIDChangeReminder fires once a week while any uid or gid change
	// is still waiting for an admin, and lists all of them with how long
	// each has waited.
	KindNSSIDChangeReminder Kind = "nss_id_change_reminder"
)

// Field documents one variable a template for this kind may reference.
// Name is the Go field name on the payload struct, which is what a
// template writes as {{ .Name }}.
type Field struct {
	Name        string
	Type        string
	Description string
}

// Group is a set of kinds the preferences page shows together. Owned here,
// with the kinds, so a new kind lands in a group without a frontend change.
type Group string

const (
	// GroupServiceCodes is everything about the service codes on accounts
	// the reader holds.
	GroupServiceCodes Group = "service_codes"
	// GroupCertificates is the "was this you?" mail: one message per
	// certificate issued in the reader's name.
	GroupCertificates Group = "certificates"
	// GroupNSSDirectory is the NSS directory mail admins act on. Every kind
	// in it is AdminOnly, so the group is shown to admins alone.
	GroupNSSDirectory Group = "nss_directory"
)

// GroupDefinition is how the preferences page heads and explains a group.
type GroupDefinition struct {
	Group       Group
	Title       string
	Description string
}

// groups is the group registry, in the order the preferences page shows
// them.
var groups = []GroupDefinition{
	{
		Group:       GroupServiceCodes,
		Title:       "Service codes",
		Description: "The service codes on accounts you hold: when one is created, used, close to expiring, or presented after it expired.",
	},
	{
		Group:       GroupCertificates,
		Title:       "Certificates issued to you",
		Description: "One email for every login, sudo or console login, so these are off by default. Turn one on to hear about every certificate issued in your name.",
	},
	{
		Group:       GroupNSSDirectory,
		Title:       "NSS directory",
		Description: "Shown to admins only. Directory changes to a stored uid or gid, which hosts do not see until an admin accepts them under Admin > Directory.",
	},
}

// Groups returns the registered groups in preferences-page order. The
// returned slice is a copy, like Definitions.
func Groups() []GroupDefinition {
	out := make([]GroupDefinition, len(groups))
	copy(out, groups)
	return out
}

// Definition is everything the rest of the server needs to know about one
// notification kind without hardcoding it.
type Definition struct {
	// Kind is the stable identifier, persisted and sent to the UI.
	Kind Kind

	// Group is the set the preferences page shows this kind in.
	Group Group

	// Title and Description are shown on the preferences page and in the
	// generated documentation.
	Title       string
	Description string

	// DefaultEnabled is what applies to a user with no stored preference
	// for this kind — which is every user the moment a new kind is added,
	// so it decides whether an existing deployment starts sending
	// something nobody asked for.
	DefaultEnabled bool

	// AlwaysSent marks a kind that is not a preference at all: it is sent
	// whatever the recipient has chosen for every other kind, the
	// preferences page never offers it, and a stored choice for it is
	// refused. Such a kind has no Group, since no page lists it, and its
	// DefaultEnabled is true, since that is what it does.
	AlwaysSent bool

	// AdminOnly marks a kind that only admins are sent. The preferences
	// page lists it only to a session holding the admin role, so nobody
	// else is offered a switch for mail they never get. It changes nothing
	// at delivery: the code that publishes such a kind addresses admins
	// alone.
	AdminOnly bool

	// Fields documents the template variables. Kept honest by
	// TestDefinitions_shouldDocumentOnlyRealPayloadFields, which fails when
	// this list and the payload struct disagree in either direction.
	Fields []Field

	// NewPayload returns a fresh, empty payload pointer to decode an
	// Event's JSON into. A constructor rather than a sample value so two
	// concurrent deliveries never share one struct.
	NewPayload func() any
}

// definitions is the registry, in the order the preferences page lists
// them. Append here to add a kind.
var definitions = []Definition{
	{
		Kind:           KindServiceEnrollmentCreated,
		Group:          GroupServiceCodes,
		Title:          "Service code created",
		Description:    "Sent when you approve a service certificate request and a service code is created for it.",
		DefaultEnabled: true,
		NewPayload:     func() any { return &ServiceEnrollmentCreated{} },
		Fields: []Field{
			{"ServiceAccount", "string", "The service account the enrollment was approved for. It is the sole principal of every certificate the code produces."},
			{"RequestID", "string", "The certificate request this enrollment came from."},
			{"EnrollmentID", "string", "The enrollment record's own identifier, as shown in the retrieval log."},
			{"KeyID", "string", "The SSH certificate key ID fixed at approval time."},
			{"Principals", "[]string", "The certificate principals fixed at approval time."},
			{"PublicKeyFingerprint", "string", "SHA256 fingerprint of the enrolled public key. The code only ever produces certificates for this key."},
			{"PublicKeyType", "string", "SSH algorithm of the enrolled public key, e.g. ssh-ed25519."},
			{"Extensions", "[]string", "SSH certificate extensions granted, after narrowing against server config."},
			{"ForceCommand", "string", "The force-command critical option, or empty if none was granted."},
			{"SourceAddresses", "[]string", "The source-address critical option, or empty if unrestricted."},
			{"NoTouchRequired", "bool", "Whether the no-touch-required extension was granted (hardware-backed sk- keys only)."},
			{"RequestSourceIP", "string", "The address the enrollment request was submitted from."},
			{"ApprovedAt", "time.Time", "When the request was approved and the code minted."},
			{"ApprovedByUsername", "string", "The username of the identity that approved the request."},
			{"ApprovedByName", "string", "The approver's human-readable name, or empty if none was captured."},
			{"ApprovedByEmail", "string", "The approver's email address, or empty if none was recorded."},
			{"ApproverSourceIP", "string", "The address the approval was made from, observed by the server. A different question from RequestSourceIP, which is the requester's."},
			{"CodeExpiresAt", "time.Time", "When the enrollment code stops being redeemable. Re-enroll before this to keep an unattended job running."},
			{"CertificateLifetime", "time.Duration", "How long each certificate redeemed from this code is valid for, measured from each redemption."},
			{"ServerURL", "string", "The server's public origin, for links back to the request."},
			readerField,
		},
	},
	{
		Kind:           KindServiceEnrollmentRedeemed,
		Group:          GroupServiceCodes,
		Title:          "Service code redeemed",
		Description:    "Sent every time one of your service codes is redeemed for a certificate, including failed attempts.",
		DefaultEnabled: true,
		NewPayload:     func() any { return &ServiceEnrollmentRedeemed{} },
		Fields: []Field{
			{"ServiceAccount", "string", "The service account the redeemed certificate is for."},
			{"RequestID", "string", "The certificate request the enrollment came from, or empty for an enrollment with no linked request."},
			{"EnrollmentID", "string", "The enrollment whose code was redeemed."},
			{"RetrievalID", "string", "This redemption's own identifier, matching the row in the retrieval log."},
			{"SourceIP", "string", "The address the redemption was made from."},
			{"RetrievedAt", "time.Time", "When the code was redeemed."},
			{"CertificateSerial", "uint64", "Serial of the certificate issued for this redemption."},
			{"CertificateExpiresAt", "time.Time", "When the issued certificate stops being valid."},
			{"KeyID", "string", "The SSH certificate key ID carried by the issued certificate."},
			{"Principals", "[]string", "The issued certificate's principals."},
			{"Succeeded", "bool", "False when the code was valid but signing failed; the failure detail is in the server log."},
			{"FirstRedemption", "bool", "True when this was the first time the code was redeemed."},
			{"CodeExpiresAt", "time.Time", "When the code itself stops being redeemable."},
			{"ServerURL", "string", "The server's public origin, for links back to the retrieval log."},
			readerField,
		},
	},
	{
		Kind:           KindServiceEnrollmentExpiring,
		Group:          GroupServiceCodes,
		Title:          "Service code expiring",
		Description:    "Sent while one of your service codes is close to expiring, so an unattended job can be enrolled again before it starts failing: weekly inside the reminder window, then daily over the final week.",
		DefaultEnabled: true,
		NewPayload:     func() any { return &ServiceEnrollmentExpiring{} },
		Fields: []Field{
			{"ServiceAccount", "string", "The service account the expiring enrollment belongs to."},
			{"RequestID", "string", "The certificate request the enrollment came from, or empty for an enrollment with no linked request."},
			{"EnrollmentID", "string", "The enrollment about to expire."},
			{"KeyID", "string", "The SSH certificate key ID fixed at approval time."},
			{"Principals", "[]string", "The certificate principals fixed at approval time."},
			{"PublicKeyFingerprint", "string", "SHA256 fingerprint of the enrolled public key."},
			{"PublicKeyType", "string", "SSH algorithm of the enrolled public key, e.g. ssh-ed25519."},
			{"FirstRedeemedAt", "time.Time", "When the code was first redeemed, or the zero time if it never was. A code never redeemed is usually a job that was never finished."},
			{"CodeExpiresAt", "time.Time", "When the code stops being redeemable. Re-enroll before this."},
			{"Daily", "bool", "True once the code is inside its final week and reminders come daily; false while they are still weekly."},
			{"ServerURL", "string", "The server's public origin, for links back to the enrollment."},
			readerField,
		},
	},
	{
		Kind:           KindServiceEnrollmentExpiredAttempt,
		Group:          GroupServiceCodes,
		Title:          "Expired service code used",
		Description:    "Sent when an expired service code is presented for redemption: either a job is still trying to use it, or someone is replaying a credential that should no longer exist.",
		DefaultEnabled: true,
		NewPayload:     func() any { return &ServiceEnrollmentExpiredAttempt{} },
		Fields: []Field{
			{"ServiceAccount", "string", "The service account the expired enrollment belongs to."},
			{"RequestID", "string", "The certificate request the enrollment came from, or empty for an enrollment with no linked request."},
			{"EnrollmentID", "string", "The enrollment whose expired code was presented."},
			{"KeyID", "string", "The SSH certificate key ID fixed at approval time."},
			{"Principals", "[]string", "The certificate principals fixed at approval time."},
			{"PublicKeyFingerprint", "string", "SHA256 fingerprint of the enrolled public key."},
			{"PublicKeyType", "string", "SSH algorithm of the enrolled public key, e.g. ssh-ed25519."},
			{"SourceIP", "string", "The address the attempt came from."},
			{"AttemptedAt", "time.Time", "When the expired code was presented."},
			{"CodeExpiredAt", "time.Time", "When the code stopped being redeemable."},
			{"ServerURL", "string", "The server's public origin, for links back to the enrollment."},
			readerField,
		},
	},
	{
		Kind:           KindUserCertificateIssued,
		Group:          GroupCertificates,
		Title:          "User certificate issued",
		Description:    "Sent every time an interactive SSH certificate is signed for you. Off by default: this is one message per login, for people who want to see every one.",
		DefaultEnabled: false,
		NewPayload:     func() any { return &CertificateIssued{} },
		Fields:         certificateIssuedFields,
	},
	{
		Kind:           KindPAMCertificateIssued,
		Group:          GroupCertificates,
		Title:          "PAM certificate issued",
		Description:    "Sent every time a certificate is signed for a local sudo or su on your behalf. Off by default: this is one message per sudo.",
		DefaultEnabled: false,
		NewPayload:     func() any { return &CertificateIssued{} },
		Fields:         certificateIssuedFields,
	},
	{
		Kind:           KindConsoleCertificateIssued,
		Group:          GroupCertificates,
		Title:          "Console certificate issued",
		Description:    "Sent every time a certificate is signed for a console login you approved with a typed code. Off by default: this is one message per login.",
		DefaultEnabled: false,
		NewPayload:     func() any { return &CertificateIssued{} },
		Fields:         certificateIssuedFields,
	},
	{
		Kind:           KindAdminAutoDisabled,
		Title:          "Admin account disabled by the directory sync",
		Description:    "Sent to an admin when the scheduled directory sync disables their account because its directory entry has been missing for longer than ldap.sync.disable_after. Always sent: it cannot be turned off, and it is not sent for a disable a SOC user or an admin made by hand.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &AdminAutoDisabled{} },
		Fields: []Field{
			{"Username", "string", "The username of the account that was disabled."},
			{"DisabledAt", "time.Time", "When the sync disabled the account."},
			{"MissingSince", "time.Time", "When the sync first searched successfully and found no directory entry for the account."},
			{"MissingFor", "time.Duration", "How long the entry had been missing when the account was disabled."},
			{"DisableAfter", "time.Duration", "The configured ldap.sync.disable_after: how long an entry may stay missing before the sync disables the account."},
			{"DirectoryDN", "string", "The last DN the account's entry was found at, or empty if none was recorded."},
			{"BaseDN", "string", "The configured ldap.base_dn the sync searches under."},
			{"Reenable", "bool", "Whether ldap.sync.reenable is on, in which case the sync clears this disable by itself once the entry is found again."},
			{"SupportEmail", "string", "Who to ask about this deployment (branding.support_email), or empty if none is configured."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the account belongs to."},
			readerField,
		},
	},

	{
		Kind:           KindUserInfoPaused,
		Title:          "OIDC UserInfo calls paused",
		Description:    "Sent to every admin when the server stops calling the identity provider's UserInfo endpoint after 10 failed calls in a row. Logins carry on with the ID token's claims alone, and one login every 15 minutes retries the call. Always sent: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &UserInfoPaused{} },
		Fields: []Field{
			{"Endpoint", "string", "The UserInfo endpoint the provider's discovery document names."},
			{"PausedAt", "time.Time", "When the server paused the calls."},
			{"ConsecutiveFailures", "int", "How many calls in a row had failed, across every instance."},
			{"LastError", "string", "The last failed call's error, with the access token removed."},
			{"NextProbeAt", "time.Time", "When the first retry may run: the first login after this time makes the call."},
			{"ServerURL", "string", "The server's public origin, naming the deployment."},
			readerField,
		},
	},
	{
		Kind:           KindUserInfoResumed,
		Title:          "OIDC UserInfo calls resumed",
		Description:    "Sent to every admin when a retry of the identity provider's UserInfo endpoint succeeds after a pause, and logins read its claims again. Always sent: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &UserInfoResumed{} },
		Fields: []Field{
			{"Endpoint", "string", "The UserInfo endpoint the provider's discovery document names."},
			{"PausedAt", "time.Time", "When the server had paused the calls."},
			{"ResumedAt", "time.Time", "When the retry succeeded and the calls resumed."},
			{"ServerURL", "string", "The server's public origin, naming the deployment."},
			readerField,
		},
	},

	{
		Kind:           KindNSSKeyExpiring,
		Title:          "NSS fleet key expiring",
		Description:    "Sent to the key's notification address (nss_keys.notification_email, which defaults to the creator's address) while a fleet key nears expiry: weekly inside mail.expiry_reminder_lead, then daily over the final week. Always sent, and not a preference: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &NSSKeyExpiring{} },
		Fields: []Field{
			{"KeyID", "string", "The fleet key's id, \"nss_\" followed by 8 characters."},
			{"KeyName", "string", "The fleet key's name."},
			{"Description", "string", "The key's description, or empty if none was set."},
			{"GroupName", "string", "The group whose members manage the key, or empty for none."},
			{"ExpiresAt", "time.Time", "When the key stops being accepted. Extend it, or create a replacement, before this."},
			{"Daily", "bool", "True once the key is inside its final week and reminders come daily; false while they are still weekly."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the key belongs to."},
			readerField,
		},
	},
	{
		Kind:           KindNSSKeyRotationCleanup,
		Title:          "NSS fleet key rotation cleanup",
		Description:    "Sent to the key's notification address (nss_keys.notification_email, which defaults to the creator's address) after a rotation, once mail.rotation_quiet_period has passed, and daily while any host still presents the previous secret, until the grace period ends. It lists the addresses that have not switched. Always sent, and not a preference: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &NSSKeyRotationCleanup{} },
		Fields: []Field{
			{"KeyID", "string", "The fleet key's id, \"nss_\" followed by 8 characters."},
			{"KeyName", "string", "The fleet key's name."},
			{"Generation", "int", "The key's current generation, the one hosts should present."},
			{"RotatedAt", "time.Time", "When the key was last rotated."},
			{"PreviousExpiresAt", "time.Time", "When the previous secret stops working. Equal to RotatedAt when no grace period is set."},
			{"Switched", "int", "Addresses whose last lookup presented the current secret."},
			{"NotSwitched", "int", "Addresses whose last lookup presented the previous secret."},
			{"Gone", "int", "Addresses seen before the rotation and not since."},
			{"NotSwitchedAddresses", "[]string", "Up to 50 addresses that have not switched, most recently seen first."},
			{"MoreNotSwitched", "int", "How many more not-switched addresses there are beyond the listed ones, or zero."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the key belongs to."},
			readerField,
		},
	},
	{
		Kind:           KindNSSKeyExpiredAttempt,
		Title:          "Expired NSS fleet key used",
		Description:    "Sent to the key's notification address (nss_keys.notification_email, which defaults to the creator's address) when a host presents an expired fleet key, at most once per mail.expired_attempt_window. The attempt is refused. Always sent, and not a preference: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &NSSKeyExpiredAttempt{} },
		Fields: []Field{
			{"KeyID", "string", "The fleet key's id, \"nss_\" followed by 8 characters."},
			{"KeyName", "string", "The fleet key's name."},
			{"SourceIP", "string", "The address the attempt came from, as the server observed it."},
			{"AttemptedAt", "time.Time", "When the expired key was presented."},
			{"ExpiredAt", "time.Time", "When the key stopped being accepted."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the key belongs to."},
			readerField,
		},
	},
	{
		Kind:           KindNSSKeyRevokedAttempt,
		Title:          "Revoked NSS fleet key used",
		Description:    "Sent to the key's notification address (nss_keys.notification_email, which defaults to the creator's address) when a host presents a revoked fleet key, at most once per mail.expired_attempt_window. The attempt is refused. Always sent, and not a preference: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &NSSKeyRevokedAttempt{} },
		Fields: []Field{
			{"KeyID", "string", "The fleet key's id, \"nss_\" followed by 8 characters."},
			{"KeyName", "string", "The fleet key's name."},
			{"SourceIP", "string", "The address the attempt came from, as the server observed it."},
			{"AttemptedAt", "time.Time", "When the revoked key was presented."},
			{"RevokedAt", "time.Time", "When the key was revoked."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the key belongs to."},
			readerField,
		},
	},
	{
		Kind:           KindNSSKeyRotated,
		Title:          "NSS fleet key rotated",
		Description:    "Sent to the key's notification address (nss_keys.notification_email, which defaults to the creator's address) at once when a fleet key is rotated. Always sent, and not a preference: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &NSSKeyRotated{} },
		Fields: []Field{
			{"KeyID", "string", "The fleet key's id, \"nss_\" followed by 8 characters."},
			{"KeyName", "string", "The fleet key's name."},
			{"Generation", "int", "The new current generation."},
			{"RotatedAt", "time.Time", "When the key was rotated."},
			{"RotatedByUsername", "string", "The account that rotated the key."},
			{"RotatedByName", "string", "That account's human-readable name, or empty if none was captured."},
			{"PreviousExpiresAt", "time.Time", "When the previous secret stops working. Equal to RotatedAt when no grace period is set or the key had expired."},
			{"ExpiresAt", "time.Time", "The key's expiry as the rotation renewed it: its own lifetime again from the rotation, at most nss.key_max_lifetime. Zero for a key that never expires."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the key belongs to."},
			readerField,
		},
	},
	{
		Kind:           KindNSSKeyRevoked,
		Title:          "NSS fleet key revoked",
		Description:    "Sent to the key's notification address (nss_keys.notification_email, which defaults to the creator's address) at once when a fleet key is revoked. Always sent, and not a preference: it cannot be turned off.",
		DefaultEnabled: true,
		AlwaysSent:     true,
		NewPayload:     func() any { return &NSSKeyRevoked{} },
		Fields: []Field{
			{"KeyID", "string", "The fleet key's id, \"nss_\" followed by 8 characters."},
			{"KeyName", "string", "The fleet key's name."},
			{"RevokedAt", "time.Time", "When the key was revoked."},
			{"RevokedByUsername", "string", "The account that revoked the key."},
			{"RevokedByName", "string", "That account's human-readable name, or empty if none was captured."},
			{"ServerURL", "string", "The server's public origin, naming the deployment the key belongs to."},
			readerField,
		},
	},
	{
		Kind:           KindNSSIDChangePending,
		Group:          GroupNSSDirectory,
		Title:          "Directory id change waiting for approval",
		Description:    "Sent to every admin when the directory gives a stored NSS account or group a different uid or gid. Hosts keep the stored number until an admin accepts the change. One message per sync run, or per batch of on-demand and login reads, listing only the changes that are new.",
		DefaultEnabled: true,
		AdminOnly:      true,
		NewPayload:     func() any { return &NSSIDChanges{} },
		Fields:         nssIDChangeFields,
	},
	{
		Kind:           KindNSSIDChangeReminder,
		Group:          GroupNSSDirectory,
		Title:          "Weekly reminder of waiting directory id changes",
		Description:    "Sent to every admin on Monday at 09:00 server time while any uid or gid change is still waiting, listing all of them with how long each has waited.",
		DefaultEnabled: true,
		AdminOnly:      true,
		NewPayload:     func() any { return &NSSIDChanges{} },
		Fields:         nssIDChangeFields,
	},
}

// nssIDChangeFields documents NSSIDChanges, shared by the two kinds that
// render it, for the reason certificateIssuedFields is shared.
var nssIDChangeFields = []Field{
	{"Changes", "[]notify.NSSIDChange", "The listed changes, oldest first, at most 50. Each has Entry (\"account\" or \"group\"), Attribute (\"uid\" or \"gid\"), Name, DN, DirectoryID (the ldap.id_attribute value, or empty), Stored (the number hosts are served), Pending (the directory's new number), PendingSince (when the server first saw it) and Waiting (how long it has waited, as a time.Duration). An account whose uid and gid both changed is two changes."},
	{"Total", "int", "How many changes the message is about, the listed ones included."},
	{"More", "int", "How many changes there are beyond the listed ones, or zero."},
	{"DirectoryURL", "string", "The pending changes on the admin directory page, where an admin accepts them: ServerURL followed by /admin/directory#pending-id-changes."},
	{"ServerURL", "string", "The server's public origin, naming the deployment the directory belongs to."},
	readerField,
}

// certificateIssuedFields documents CertificateIssued, shared by the three
// kinds that render it. Written once rather than three times so they cannot
// drift into documenting the same struct differently.
var certificateIssuedFields = []Field{
	{"CertificateType", "string", "The certificate type: \"user\", \"pam\" or \"console\"."},
	{"RequestID", "string", "The certificate request this certificate was issued for."},
	{"CertificateID", "string", "The certificate's own record, for a link to /certs/<id>. Empty if the record could not be read back."},
	{"KeyID", "string", "The SSH certificate key ID."},
	{"Principals", "[]string", "The accounts this certificate may log in as."},
	{"Serial", "uint64", "The certificate serial, matching the entry in your certificate history."},
	{"PublicKeyFingerprint", "string", "SHA256 fingerprint of the key the certificate was issued for."},
	{"LocalUsername", "string", "The local account the client reported, or empty if it reported none. Client-reported, so not evidence."},
	{"LocalHostname", "string", "The machine the client reported, or empty if it reported none. Client-reported, so not evidence."},
	{"RequestingUser", "string", "Who invoked the request, when that differs from the account it ran as: PAM_RUSER for a module, SUDO_USER for the client. Client-reported, so not evidence."},
	{"Process", "string", "The command line that asked, e.g. \"sudo systemctl restart nginx\" or \"ssoossh ssh login\". Client-reported, so not evidence."},
	{"TTY", "string", "The terminal the request came from, or empty if it had none. Client-reported, so not evidence."},
	{"RemoteHost", "string", "The peer address the requester reported, non-empty only when it was itself reached over the network. Client-reported, so not evidence."},
	{"PAMService", "string", "The PAM service that asked (sudo, su, sshd, login). Empty on a user certificate, which has none."},
	{"MachineID", "string", "The requesting machine's stable installation id, which survives a rename. Client-reported, so not evidence."},
	{"OS", "string", "The platform as the requesting machine describes itself. Client-reported, so not evidence."},
	{"Client", "string", "The implementation and version that sent the request, e.g. \"ssoossh/1.2.3\" or \"pam_ssoossh-c/0.3.0\"."},
	{"ClientMode", "string", "The PAM module's configured mode argument (auto, sudo, console). Empty for the client, which has none."},
	{"CallerUID", "*int64", "The requesting process's user id, or absent if it reported none. A pointer because uid 0 is a value."},
	{"CallerGID", "*int64", "The requesting process's group id, or absent if it reported none."},
	{"CallerPID", "*int64", "The requesting process's id, for joining against the machine's own logs."},
	{"CallerPPID", "*int64", "The requesting process's parent id."},
	{"SourceIP", "string", "The address the request was made from. Observed by the server, unlike everything the client reported."},
	{"IssuedAt", "time.Time", "When the certificate becomes valid."},
	{"ExpiresAt", "time.Time", "When the certificate stops being valid."},
	{"ApprovedByUsername", "string", "The account that approved the request, or empty if no decision record survives."},
	{"ApprovedByName", "string", "The approver's human-readable name, or empty if none was captured."},
	{"ApprovedByEmail", "string", "The approver's email address, or empty if none was recorded."},
	{"ApproverSourceIP", "string", "The address the approval was made from, observed by the server. On the unhappy path this is the fact that says whose session was used."},
	{"ApproverUserAgent", "string", "The browser that approved, as it identified itself."},
	{"ApprovedAt", "time.Time", "When the approval happened. Zero if no decision record survives."},
	{"Extensions", "[]string", "SSH certificate extensions granted, after narrowing against server config."},
	{"ForceCommand", "string", "The force-command critical option, or empty if none was granted."},
	{"SourceAddresses", "[]string", "The source-address critical option, or empty if unrestricted."},
	{"NoTouchRequired", "bool", "Whether the certificate waives the hardware-key touch, which makes it a weaker credential than one that does not."},
	{"ServerURL", "string", "The server's public origin, for links back to the certificate."},
	readerField,
}

// readerField documents Reader's one field, which every payload embeds.
var readerField = Field{"Advanced", "bool", "True when the recipient chose the advanced email detail level, false for simplified. Set per recipient at delivery; a template that ignores it sends the same message at both levels."}

// Definitions returns the registered kinds in preferences-page order. The
// returned slice is a copy, so a caller sorting or filtering it cannot
// reorder the registry for everyone else.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

// Lookup returns the definition for k, reporting whether it is registered.
// Unknown kinds are expected rather than exceptional: a stored preference
// row outlives the code that created it, so a downgrade or a removed kind
// leaves rows nothing answers to.
func Lookup(k Kind) (Definition, bool) {
	for _, def := range definitions {
		if def.Kind == k {
			return def, true
		}
	}
	return Definition{}, false
}

// DefaultEnabled reports what applies to a user with no stored preference
// for k. An unregistered kind is never sent, so it answers false.
func DefaultEnabled(k Kind) bool {
	def, ok := Lookup(k)
	return ok && def.DefaultEnabled
}

// AdminOnly reports whether k is sent to admins alone, so only they are
// offered it. An unregistered kind answers false.
func AdminOnly(k Kind) bool {
	def, ok := Lookup(k)
	return ok && def.AdminOnly
}

// AlwaysSent reports whether k is sent whatever its recipient has chosen.
// An unregistered kind is never sent, so it answers false.
func AlwaysSent(k Kind) bool {
	def, ok := Lookup(k)
	return ok && def.AlwaysSent
}
