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

	// ClientTimeout is the whole budget: the longest a client waiting on a
	// certificate request can go before the server hands it a terminal
	// answer. Everything else about request timing is derived from it, by
	// ApprovalTTL and SigningGrace below.
	//
	// The server owns this deadline rather than the client, and measures it
	// from the request's creation (see
	// the request's creation), so a client reconnecting to its
	// event stream re-attaches to the original deadline instead of
	// extending it. If the client is gone for good, the approval and the
	// signature it was waiting for are moot anyway.
	ClientTimeout time.Duration `mapstructure:"client_timeout,string" default:"5m"`
}

// signingShare is the fraction of ClientTimeout reserved for the machine
// half of the wait. The rest belongs to the human.
//
// A healthy signature takes milliseconds, so this only has to cover a slow
// signing backend (an HSM under load); a tenth of the budget is generous
// for that and leaves the approver the bulk of it. Two of these shares are
// spent in the worst case — one for the stranded cutoff and one for the
// sweep interval that detects it — which is why ApprovalTTL subtracts twice.
const signingShare = 10

// SigningGrace is the machine's share of ClientTimeout: how long an
// approved request may sit awaiting signature before the stranded-request
// sweep fails it (see docs/internals/signing-pipeline.md), how often that sweep runs,
// and how long `service retrieve` blocks waiting for its certificate.
//
// Not measured from approval — nothing records when a request entered
// signing — but from creation, offset by ApprovalTTL, so the sweep can
// never cancel a request that might still be in flight. See the sweep's doc
// comment for the arithmetic.
func (c *CertificateOptions) SigningGrace() time.Duration {
	return c.ClientTimeout / signingShare
}

// ApprovalTTL is the human's share of ClientTimeout: how long a pending
// request stays valid for approval before it is treated as expired. Shared
// across the certificate types — it is "how stale can an unapproved request
// get", not a per-type concept like ValidDuration (the issued certificate's
// own lifetime).
//
// Whatever is left after reserving the two signing shares the worst case
// spends, so ApprovalTTL + 2*SigningGrace == ClientTimeout.
func (c *CertificateOptions) ApprovalTTL() time.Duration {
	return c.ClientTimeout - 2*c.SigningGrace()
}

// Validate rejects certificate options the rest of the server cannot derive
// a bound from. Called at startup so a bad value stops the process, rather
// than surfacing much later as a sweep that fails live requests or a cache
// that grows without limit.
func (c *CertificateOptions) Validate() error {
	// A non-positive budget has no coherent meaning downstream: the
	// stranded-request sweep would have no cutoff to work from and would
	// treat every signing row as stranded, and the resolved-outcome cache
	// would have no age at which an entry is safe to evict.
	//
	// The floor is signingShare rather than zero because SigningGrace
	// divides by it: a smaller budget rounds the machine's share down to
	// nothing, which would give the sweep a zero interval and `service
	// retrieve` a timer that fires immediately.
	if c.ClientTimeout < signingShare {
		return fmt.Errorf("cert_options.client_timeout must be greater than zero (the default is 5m): a disabled timeout leaves pending requests unbounded and gives the stranded-request sweep no cutoff")
	}
	// Zero here would mint enrollment codes that are already expired, which
	// surfaces as `service retrieve` reporting an unknown code rather than
	// anything pointing at the configuration. Rejecting it at startup is the
	// only place that can name the setting.
	if c.Service.EnrollmentDuration <= 0 {
		return fmt.Errorf("cert_options.service.enrollment_duration must be greater than zero (the default is 8760h): a zero lifetime expires every enrollment code the moment it is issued")
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
	// at approval: the binding answers "is this your request", this answers
	// "are you allowed certificates at all".
	RequireGroup string `mapstructure:"require_group" default:""`

	// ValidDuration is the ceiling on how long an issued user certificate is
	// valid. Any lifetime policy only ever narrows from here.
	ValidDuration time.Duration `mapstructure:"valid_duration,string" default:"30s"`

	// Extensions are the SSH certificate extensions a user certificate may
	// carry, e.g. permit-pty and permit-user-rc. A request is narrowed to
	// the intersection of what it asked for and this list.
	Extensions []string `mapstructure:"extensions" default:"[permit-pty, permit-user-rc]"`

	// KeyIDTemplate is the fallback for the service and PAM templates when
	// either is empty, since
	// user certificates are the common case. See
	// docs/guide/features.md (key ID templating).
	KeyIDTemplate string `mapstructure:"key_id_template" default:""`

	// LifetimePolicy configures tiered certificate duration based on OIDC
	// group membership and source network narrowing — see
	// docs/operations/certificate-lifetime-policy.md.
	LifetimePolicy LifetimePolicy `mapstructure:"lifetime_policy"`
}

// CertOptionsService configures issuance of service certificates: the OIDC
// group required to request one, how long they're valid for, and which SSH
// certificate extensions to grant.
type CertOptionsService struct {
	// RequireGroup is the OIDC group a requester must belong to in order to
	// enroll a service certificate. Empty means any authenticated user may.
	RequireGroup string `mapstructure:"require_group" default:""`

	// ValidDuration is the ceiling on how long each certificate produced
	// from an enrollment is valid, bounding every redemption of the code
	// rather than the code itself.
	ValidDuration time.Duration `mapstructure:"valid_duration,string" default:"8760h"`

	// Extensions are the SSH certificate extensions a service certificate
	// may carry. A request is narrowed to the intersection of what it asked
	// for and this list.
	Extensions []string `mapstructure:"extensions" default:"[]"`

	// EnrollmentDuration is how long the enrollment code minted at approval
	// stays redeemable — deliberately independent of ValidDuration, which
	// bounds each certificate the code produces.
	//
	// The two answer different questions. ValidDuration is "how long may one
	// issued certificate be used", and wants to be short. EnrollmentDuration
	// is "how long may this service keep asking for a fresh one", and wants
	// to be long: the code is reusable precisely so an unattended job can run
	// it from cron. Deriving one from the
	// other collapsed that — shortening a service certificate's lifetime also
	// killed the code within the same span, so a cron job re-enrolled by hand
	// on every run.
	//
	// Each redemption gets ValidDuration afresh, measured from the redemption,
	// so a long code never yields a long certificate.
	EnrollmentDuration time.Duration `mapstructure:"enrollment_duration,string" default:"8760h"`

	// KeyIDTemplate is the key ID written into service certificates; see
	// docs/guide/features.md (key ID templating). Empty falls back to
	// cert_options.user.key_id_template.
	KeyIDTemplate string `mapstructure:"key_id_template" default:""`

	// LifetimePolicy configures tiered certificate duration based on OIDC
	// group membership and source network narrowing — see
	// docs/operations/certificate-lifetime-policy.md.
	LifetimePolicy LifetimePolicy `mapstructure:"lifetime_policy"`
}

// CertOptionsPAM configures issuance of PAM certificates: short-lived
// certificates a pam_ssoossh-authenticated local operation (e.g. `sudo`)
// validates once and discards. Structurally identical to the user options,
// but its defaults and fallback behavior deliberately diverge — see each
// field's comment.
type CertOptionsPAM struct {
	// RequireGroup is an optional OIDC group an approver must belong to for
	// a PAM certificate to be issued, and behaves exactly like
	// cert_options.user.require_group: empty means no group restriction.
	//
	// It is an extra filter an operator may apply, not the authorization
	// itself. Whether the local operation is permitted is the host's own
	// decision — pam_ssoossh authenticates the user, and the local PAM
	// stack and sudoers policy authorize them.
	RequireGroup string `mapstructure:"require_group" default:""`

	// ValidDuration should be seconds, not hours: a PAM certificate is
	// validated once, in-process, and discarded — it never enters an agent
	// and is never reused. Pick this together with the client's skew
	// tolerance (see pam_ssoossh/checks.go, check 4).
	ValidDuration time.Duration `mapstructure:"valid_duration,string" default:"30s"`

	// Extensions should default to empty. permit-pty and friends are
	// meaningless for a certificate that authenticates a single local
	// operation and is then thrown away.
	Extensions []string `mapstructure:"extensions" default:"[]"`

	// KeyIDTemplate is the key ID written into PAM certificates. Unlike the
	// service template, it does NOT fall back to
	// cert_options.user.key_id_template: a sudo and a login by the same
	// person must stay distinguishable in an sshd or sudo audit log, so PAM
	// has its own built-in default instead of silently inheriting the user
	// template.
	KeyIDTemplate string `mapstructure:"key_id_template" default:""`
}

// LifetimePolicy configures certificate issuance duration based on tiered
// groups and source network policies — see docs/operations/certificate-lifetime-policy.md.
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
	// See docs/operations/certificate-lifetime-policy.md section "Which address"
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
// source IP address of the request. See docs/operations/certificate-lifetime-policy.md
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
	// ignored for user certificates (see docs/operations/certificate-lifetime-policy.md
	// "Not for user certificates"). The network must be narrow enough to actually
	// restrict — a /0 or ::/0 with PinSourceAddress=true is a warning sign (the
	// certificate can be used anywhere, pinning is meaningless). Narrowing is
	// enforced by the intersectExtensions helper (source-address is not an extension).
	PinSourceAddress bool `mapstructure:"pin_source_address"`
}
