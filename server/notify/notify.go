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
// docs/operations/email-notifications.md ("Adding a notification kind") — in short: a
// Kind constant, a payload struct in payloads.go, a Definition in the
// registry below, and the two template files. Everything else (preferences
// storage and UI, the docs table, delivery) is driven off the registry and
// needs no change.
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
