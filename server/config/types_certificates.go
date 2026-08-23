package config

import (
	"fmt"
	"time"
)

// CertificateOptions groups the certificate-issuance options for each
// SSH certificate type ssoosshd can sign.
type CertificateOptions struct {
	User    CertOptionsUser    `mapstructure:"user"`
	Service CertOptionsService `mapstructure:"service"`
	PAM     CertOptionsPAM     `mapstructure:"pam"`

	// RequestTTL is how long a pending certificate request (user or
	// service) stays valid for approval before it's treated as expired.
	// Shared across the types — it's "how stale can an unapproved
	// request get," not a per-type concept like ValidDuration (the issued
	// certificate's own lifetime).
	RequestTTL time.Duration `mapstructure:"request_ttl,string"`

	// SigningTimeout is how long an approved request may sit awaiting
	// signature before the sweep treats it as stranded and fails it (see
	// docs/signing-pipeline.md). A healthy signature
	// takes milliseconds; this only needs to be generous enough for a slow
	// signing backend (an HSM, say) under load.
	//
	// Note this is not measured from approval — nothing records when a
	// request entered signing — but from creation, offset by RequestTTL, so
	// the sweep can never cancel a request that might still be in flight.
	// See the sweep's doc comment for the arithmetic.
	SigningTimeout time.Duration `mapstructure:"signing_timeout,string"`
}

// Validate rejects certificate options the rest of the server cannot derive
// a bound from. Called at startup so a bad value stops the process, rather
// than surfacing much later as a sweep that fails live requests or a cache
// that grows without limit.
func (c *CertificateOptions) Validate() error {
	// RequestTTL = 0 used to mean "expiry disabled", and every consumer
	// carried a fallback for it. Each fallback was a hazard rather than a
	// feature: the stranded-request sweep has no cutoff to work from and
	// treats every signing row as stranded, and the resolved-outcome cache
	// has no age at which an entry is safe to evict. Requiring a positive
	// value removes both special cases at the source.
	if c.RequestTTL <= 0 {
		return fmt.Errorf("cert_options.request_ttl must be greater than zero (the default is 5m): a disabled TTL leaves pending requests unbounded and gives the stranded-request sweep no cutoff")
	}
	return nil
}

// CertOptionsUser configures issuance of user certificates: who may approve
// one, how long they're valid for, and which SSH certificate extensions to
// grant.
type CertOptionsUser struct {
	// RequireGroup is the OIDC group an approver must belong to. Empty — the
	// default — means any authenticated user may approve, which is the
	// behavior every deployment has had so far.
	//
	// Worth setting even though approval is already bound to the requester
	// (see CertRequestService.Approve): the binding answers "is this your
	// request", this answers "are you allowed certificates at all".
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`

	// KeyIDTemplate is the fallback for CertOptionsService.KeyIDTemplate
	// and CertOptions.KeyIDTemplate (host) when either is empty, since
	// user certificates are the common case. See
	// docs/features.md (key ID templating).
	KeyIDTemplate string `mapstructure:"key_id_template"`

	// LifetimePolicy configures tiered certificate duration based on OIDC
	// group membership and source network narrowing — see
	// docs/certificate-lifetime-policy.md.
	LifetimePolicy LifetimePolicy `mapstructure:"lifetime_policy"`
}

// CertOptionsService configures issuance of service certificates: the OIDC
// group required to request one, how long they're valid for, and which SSH
// certificate extensions to grant.
type CertOptionsService struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`

	// KeyIDTemplate; see CertOptions.KeyIDTemplate and
	// docs/features.md (key ID templating). Empty falls back to
	// CertificateOptions.User.KeyIDTemplate.
	KeyIDTemplate string `mapstructure:"key_id_template"`

	// LifetimePolicy configures tiered certificate duration based on OIDC
	// group membership and source network narrowing — see
	// docs/certificate-lifetime-policy.md.
	LifetimePolicy LifetimePolicy `mapstructure:"lifetime_policy"`
}

// CertOptionsPAM configures issuance of PAM certificates: short-lived
// certificates a pam_ssoossh-authenticated local operation (e.g. `sudo`)
// validates once and discards. Structurally identical to CertOptionsUser,
// but its defaults and fallback behavior deliberately diverge — see each
// field's comment.
type CertOptionsPAM struct {
	// RequireGroup is the OIDC group an approver must belong to for a PAM
	// certificate to be issued. Unlike CertOptionsUser.RequireGroup, empty
	// here means no PAM certificates are ever issued rather than "any
	// authenticated user may approve" — "who may sudo on this host" is a
	// narrower question than "who may log in", and this option has to fail
	// closed rather than default open (see docs/features.md's
	// identical empty-denies rule for admin.require_group). An operator must
	// set this explicitly to enable PAM issuance at all.
	RequireGroup string `mapstructure:"require_group"`

	// ValidDuration should be seconds, not hours: a PAM certificate is
	// validated once, in-process, and discarded — it never enters an agent
	// and is never reused. Pick this together with the client's skew
	// tolerance (see pam_ssoossh/checks.go, check 4).
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`

	// Extensions should default to empty. permit-pty and friends are
	// meaningless for a certificate that authenticates a single local
	// operation and is then thrown away.
	Extensions []string `mapstructure:"extensions"`

	// KeyIDTemplate does NOT fall back to CertificateOptions.User's the way
	// CertOptionsService's and CertOptions' (host) do — see
	// newKeyIDTemplates. A sudo and a login by the same person must stay
	// distinguishable in an sshd or sudo audit log, so PAM gets its own
	// hardcoded default (defaultPAMKeyIDTemplate) rather than silently
	// inheriting the user template.
	KeyIDTemplate string `mapstructure:"key_id_template"`
}

// LifetimePolicy configures certificate issuance duration based on tiered
// groups and source network policies — see docs/certificate-lifetime-policy.md.
// Empty configuration (all fields at their zero values) means all certificates
// receive ValidDuration from the enclosing CertOptions* struct.
type LifetimePolicy struct {
	// DefaultDuration is the duration applied when no tier's group matches.
	// If zero, the enclosing CertOptions*.ValidDuration is used instead.
	DefaultDuration time.Duration `mapstructure:"default_duration,string"`

	// Tiers are evaluated in order; the first tier whose group appears in the
	// approver's OIDC groups wins. An empty tiers list means DefaultDuration
	// is always used (or ValidDuration if DefaultDuration is zero).
	Tiers []LifetimePolicyTier `mapstructure:"tiers"`

	// SourcePolicy restricts certificate lifetime based on the request's
	// source IP address. Longest prefix match wins; ties resolve to the
	// stricter rule. Entries are intersected with the tier-determined
	// duration, and the final effective lifetime is clamped to the ceiling
	// set by the enclosing CertOptions*.ValidDuration.
	//
	// See docs/certificate-lifetime-policy.md section "Which address"
	// for why the server-observed source IP is used, and why
	// RequestedOptions.SourceAddresses is never consulted.
	SourcePolicy []SourcePolicyEntry `mapstructure:"source_policy"`
}

// LifetimePolicyTier is one OIDC group matching rule in LifetimePolicy.Tiers.
type LifetimePolicyTier struct {
	// Group is the OIDC group whose membership triggers this tier.
	Group string `mapstructure:"group"`

	// MaxDuration is the longest lifetime certificates in this tier can receive.
	MaxDuration time.Duration `mapstructure:"max_duration,string"`
}

// SourcePolicyEntry restricts certificate lifetime and options based on the
// source IP address of the request. See docs/certificate-lifetime-policy.md
// section "Source-network policy" for the full semantics.
type SourcePolicyEntry struct {
	// CIDR is the IPv4 or IPv6 network this rule applies to, in CIDR notation
	// (e.g., "10.0.0.0/8" or "2001:db8::/32").
	CIDR string `mapstructure:"cidr"`

	// MaxDuration is the longest lifetime certificates from this network can receive.
	// The final effective duration is min(tier_duration, source_rule_max_duration, type_ceiling).
	MaxDuration time.Duration `mapstructure:"max_duration,string"`

	// Extensions restricts the SSH certificate extensions to this set. The effective
	// set is the intersection with the type's configured extensions. An empty list
	// means no extensions (equivalent to an explicit "no extensions" policy);
	// omit this field to apply no extension restriction.
	Extensions []string `mapstructure:"extensions"`

	// PinSourceAddress, when true, adds a critical "source-address" SSH option
	// pinning the certificate to this network. Valid only for service certificates;
	// ignored for user certificates (see docs/certificate-lifetime-policy.md
	// "Not for user certificates"). The network must be narrow enough to actually
	// restrict — a /0 or ::/0 with PinSourceAddress=true is a warning sign (the
	// certificate can be used anywhere, pinning is meaningless). Narrowing is
	// enforced by the intersectExtensions helper (source-address is not an extension).
	PinSourceAddress bool `mapstructure:"pin_source_address"`
}
