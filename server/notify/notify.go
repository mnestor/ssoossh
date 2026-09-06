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
)

// Field documents one variable a template for this kind may reference.
// Name is the Go field name on the payload struct, which is what a
// template writes as {{ .Name }}.
type Field struct {
	Name        string
	Type        string
	Description string
}

// Definition is everything the rest of the server needs to know about one
// notification kind without hardcoding it.
type Definition struct {
	// Kind is the stable identifier, persisted and sent to the UI.
	Kind Kind

	// Title and Description are shown on the preferences page and in the
	// generated documentation.
	Title       string
	Description string

	// DefaultEnabled is what applies to a user with no stored preference
	// for this kind — which is every user the moment a new kind is added,
	// so it decides whether an existing deployment starts sending
	// something nobody asked for.
	DefaultEnabled bool

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
		Title:          "Service enrollment created",
		Description:    "Sent when you approve a service certificate request and an enrollment code is created for it.",
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
		},
	},
	{
		Kind:           KindServiceEnrollmentRedeemed,
		Title:          "Service enrollment redeemed",
		Description:    "Sent every time one of your enrollment codes is redeemed for a certificate, including failed attempts.",
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
		},
	},
	{
		Kind:           KindServiceEnrollmentExpiring,
		Title:          "Service enrollment expiring",
		Description:    "Sent while one of your enrollment codes is close to expiring, so an unattended job can be re-enrolled before it starts failing: weekly inside the reminder window, then daily over the final week.",
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
		},
	},
	{
		Kind:           KindServiceEnrollmentExpiredAttempt,
		Title:          "Expired enrollment code used",
		Description:    "Sent when an expired enrollment code is presented for redemption: either a job is still trying to use it, or someone is replaying a credential that should no longer exist.",
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
		},
	},
	{
		Kind:           KindUserCertificateIssued,
		Title:          "User certificate issued",
		Description:    "Sent every time an interactive SSH certificate is signed for you. Off by default: this is one message per login, for people who want to see every one.",
		DefaultEnabled: false,
		NewPayload:     func() any { return &CertificateIssued{} },
		Fields:         certificateIssuedFields,
	},
	{
		Kind:           KindPAMCertificateIssued,
		Title:          "PAM certificate issued",
		Description:    "Sent every time a certificate is signed for a local sudo or su on your behalf. Off by default: this is one message per sudo.",
		DefaultEnabled: false,
		NewPayload:     func() any { return &CertificateIssued{} },
		Fields:         certificateIssuedFields,
	},
	{
		Kind:           KindConsoleCertificateIssued,
		Title:          "Console certificate issued",
		Description:    "Sent every time a certificate is signed for a console login you approved with a typed code. Off by default: this is one message per login.",
		DefaultEnabled: false,
		NewPayload:     func() any { return &CertificateIssued{} },
		Fields:         certificateIssuedFields,
	},
}

// certificateIssuedFields documents CertificateIssued, shared by the three
// kinds that render it. Written once rather than three times so they cannot
// drift into documenting the same struct differently.
var certificateIssuedFields = []Field{
	{"CertificateType", "string", "The certificate type: \"user\", \"pam\" or \"console\"."},
	{"RequestID", "string", "The certificate request this certificate was issued for."},
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
}

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
