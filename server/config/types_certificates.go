package config

import (
	"fmt"
	"net/netip"
	"time"
)

// CertificateOptions groups the certificate-issuance options for each
// SSH certificate type ssoosshd can sign.
type CertificateOptions struct {
	User    CertOptionsUser    `mapstructure:"user"`
	Service CertOptionsService `mapstructure:"service"`
	PAM     CertOptionsPAM     `mapstructure:"pam"`
	Console CertOptionsConsole `mapstructure:"console"`

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
	return SigningGraceFor(c.ClientTimeout)
}

// SigningGraceFor is SigningGrace's arithmetic against an arbitrary budget,
// so a type carrying its own client_timeout (see
// CertOptionsConsole.ClientTimeout) splits it the same way the global one
// is split. One definition, so the two cannot drift.
func SigningGraceFor(budget time.Duration) time.Duration {
	return budget / signingShare
}

// ApprovalTTL is the human's share of ClientTimeout: how long a pending
// request stays valid for approval before it is treated as expired.
//
// This is the deployment-wide value, derived from the global budget. It is
// no longer the only one: a type may shorten its own budget (today only
// cert_options.console.client_timeout does), and a request on such a type
// expires on ApprovalTTLFor(that budget) instead. The global stays the
// ceiling, which is what lets everything derived from it here — the
// stranded-request sweep's cutoff, the resolved-outcome cache's eviction
// age, the sweep interval — keep computing from the longest possible
// budget and stay correct for a request on a shorter one.
//
// Whatever is left after reserving the two signing shares the worst case
// spends, so ApprovalTTL + 2*SigningGrace == ClientTimeout.
func (c *CertificateOptions) ApprovalTTL() time.Duration {
	return ApprovalTTLFor(c.ClientTimeout)
}

// ApprovalTTLFor is ApprovalTTL's arithmetic against an arbitrary budget.
// See SigningGraceFor.
func ApprovalTTLFor(budget time.Duration) time.Duration {
	return budget - 2*SigningGraceFor(budget)
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
	// A per-type budget may only shorten the global one. Everything derived
	// from the global — the stranded-request sweep's cutoff, the
	// resolved-outcome cache's eviction age, the sweep interval — is
	// computed from the longest possible budget and stays correct for a
	// request on a shorter one. A type allowed to exceed it would break
	// that: the sweep could fail a request still legitimately in flight.
	if c.Console.ClientTimeout > c.ClientTimeout {
		return fmt.Errorf("cert_options.console.client_timeout (%s) must not exceed cert_options.client_timeout (%s): the global budget is the ceiling every per-type budget is measured against, and a type may only shorten it", c.Console.ClientTimeout, c.ClientTimeout)
	}
	// Same floor and the same reason as the global: SigningGraceFor
	// divides by signingShare, so a smaller budget rounds the machine's
	// share down to nothing.
	if c.Console.ClientTimeout != 0 && c.Console.ClientTimeout < signingShare {
		return fmt.Errorf("cert_options.console.client_timeout must be greater than zero (the default is 2m), or unset to inherit cert_options.client_timeout")
	}
	// Refusing an unparseable CIDR at startup is the only place that can
	// name the setting. A gate that silently matched nothing would leave
	// the operator believing console logins were restricted when every one
	// of them was being refused, or — worse, if the failure were read the
	// other way — permitted.
	for i, cidr := range c.Console.AllowedNetworks {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("cert_options.console.allowed_networks[%d]: %q is not a CIDR network (e.g. 10.20.0.0/16): %w", i, cidr, err)
		}
	}

	// Zero here would mint enrollment codes that are already expired, which
	// surfaces as `service retrieve` reporting an unknown code rather than
	// anything pointing at the configuration. Rejecting it at startup is the
	// only place that can name the setting.
	if c.Service.EnrollmentDuration <= 0 {
		return fmt.Errorf("cert_options.service.enrollment_duration must be greater than zero (the default is 8760h): a zero lifetime expires every enrollment code the moment it is issued")
	}

	// Replaced keys fail loudly rather than being silently ignored: a
	// require_group that stopped applying would widen who may approve, and
	// a source-rule extensions list that stopped applying would widen what
	// a certificate carries.
	for path, val := range map[string]string{
		"cert_options.user.require_group":    c.User.RequireGroup,
		"cert_options.service.require_group": c.Service.RequireGroup,
		"cert_options.pam.require_group":     c.PAM.RequireGroup,
	} {
		if val != "" {
			return fmt.Errorf("%s has been replaced by require: move `require_group: %q` to `require: {group: %q}`", path, val, val)
		}
	}
	for path, lp := range map[string]LifetimePolicy{
		"cert_options.user.lifetime_policy":    c.User.LifetimePolicy,
		"cert_options.service.lifetime_policy": c.Service.LifetimePolicy,
		"cert_options.pam.lifetime_policy":     c.PAM.LifetimePolicy,
		"cert_options.console.lifetime_policy": c.Console.LifetimePolicy,
	} {
		for i, rule := range lp.SourcePolicy {
			if len(rule.Extensions) > 0 {
				return fmt.Errorf("%s.source_policy[%d].extensions has been replaced by removed_extensions: source rules now subtract extensions instead of intersecting them", path, i)
			}
		}
	}
	return nil
}

// CertOptionsUser configures issuance of user certificates: who may approve
// one, how long they're valid for, and which SSH certificate extensions to
// grant.
type CertOptionsUser struct {
	// RequireGroup has been replaced by require, which expresses the same
	// group check as `require: {group: <name>}` alongside claim conditions.
	// Setting it is a startup error rather than a silent no-op, because a
	// gate that silently stopped applying would widen issuance.
	RequireGroup string `mapstructure:"require_group" default:""`

	// Require is the condition an approver's identity must satisfy to
	// approve a user certificate at all. Unset means any authenticated user
	// may approve, which is the behavior every deployment has had so far.
	//
	// Worth setting even though approval is already bound to the requester
	// at approval: the binding answers "is this your request", this answers
	// "are you allowed certificates at all".
	Require *PolicyCondition `mapstructure:"require"`

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
	// RequireGroup has been replaced by require (see
	// cert_options.user.require_group). Setting it is a startup error.
	RequireGroup string `mapstructure:"require_group" default:""`

	// Require is the condition an approver's identity must satisfy in order
	// to approve a service enrollment. Unset means any authenticated user
	// may. Evaluated against the approver — the human vouching — not the
	// service account receiving the certificate.
	Require *PolicyCondition `mapstructure:"require"`

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
	// RequireGroup has been replaced by require (see
	// cert_options.user.require_group). Setting it is a startup error.
	RequireGroup string `mapstructure:"require_group" default:""`

	// Require is an optional condition an approver's identity must satisfy
	// for a PAM certificate to be issued — a minimum score to authenticate a
	// local operation, say sudo behind `claim: loc, at_least: 40`. Unset
	// means no restriction.
	//
	// It is an extra filter an operator may apply, not the authorization
	// itself. Whether the local operation is permitted is the host's own
	// decision — pam_ssoossh authenticates the user, and the local PAM
	// stack and sudoers policy authorize them.
	Require *PolicyCondition `mapstructure:"require"`

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

	// LifetimePolicy takes the same grammar as the other types, though in
	// practice only the require gate matters for PAM: duration tiers against
	// a 30-second, validated-once certificate and extension grants against
	// an empty extensions ceiling have nothing to do. The expected
	// configuration is require alone, tiers unused.
	LifetimePolicy LifetimePolicy `mapstructure:"lifetime_policy"`
}

// CertOptionsConsole configures issuance of console certificates: the
// certificate that authenticates an interactive console login on a machine
// with no browser in front of it, where the approval travels as a short
// code the human reads off the screen and types into the web UI (see
// docs/proposals/console-login-pam.md).
//
// Deliberately its own type rather than a flag on cert_options.pam. A
// console certificate buys a whole interactive session where a PAM one
// buys a single local operation, so an operator needs to gate, time, and
// label the two separately — a `sudo` may be approvable by a colleague
// when a console login is not, and the two must stay distinguishable in an
// audit log.
type CertOptionsConsole struct {
	// Require is the condition an approver's identity must satisfy to
	// approve a console login at all. Unset means any authenticated user
	// may.
	//
	// It is not the per-host gate. Restricting which accounts may console
	// into which machine belongs in the host's own PAM stack
	// (pam_succeed_if above the ssoossh line), where it is root-owned,
	// unforgeable, and fails before any network call — a group a module
	// sent would be untrusted input that stops applying the moment
	// somebody omits it.
	Require *PolicyCondition `mapstructure:"require"`

	// AllowedNetworks refuses request creation from outside these CIDRs,
	// before a keypair is certified and before any human is asked to
	// approve anything. Empty means no network gate.
	//
	// The server's half of per-host policy rests on the source address
	// rather than the hostname because the address is observed by the
	// server and the hostname is a string an unauthenticated caller typed
	// — the same reasoning that got host certificates declined
	// (docs/project/decisions.md). Behind a reverse proxy this is only
	// meaningful with http.trusted_proxies set; without it every request
	// carries the proxy's address.
	AllowedNetworks []string `mapstructure:"allowed_networks" default:"[]"`

	// ClientTimeout is this type's whole budget: the longest a console
	// login can sit waiting for a human. Unset (zero) inherits
	// cert_options.client_timeout. A value LONGER than that global is a
	// startup error — the global is the ceiling, and a type may only
	// shorten it.
	//
	// Shorter is the point. The approval window is the attacker's working
	// time in the consent-phishing case the typed code exists to raise the
	// bar on: the attacker is the one at the unattended console, and has to
	// phone a victim, read them the code off its screen, and talk them
	// through approving it. Halving the window halves the time that call
	// has to succeed in.
	//
	// The human's share is client_timeout - 2*(client_timeout/10), so 2m
	// here gives the approver 96s, not 120s. There is a floor, and it is
	// not the technical one: below about 90s a first approval that has to
	// go through an OIDC login starts to fail, people retry, and a flow
	// people habitually retry is a flow people learn to approve without
	// reading.
	ClientTimeout time.Duration `mapstructure:"client_timeout,string" default:"2m"`

	// ValidDuration is the ceiling on how long an issued console
	// certificate is valid. Seconds, not hours, for the same reason as the
	// PAM type: it is validated once by the module and discarded, and the
	// session it authorizes outlives it by design.
	ValidDuration time.Duration `mapstructure:"valid_duration,string" default:"30s"`

	// Extensions default to empty. permit-pty and friends are meaningless
	// for a certificate that authenticates one local login and is then
	// thrown away.
	Extensions []string `mapstructure:"extensions" default:"[]"`

	// KeyIDTemplate is the key ID written into console certificates. Like
	// the PAM template and unlike the service one, it does NOT fall back
	// to cert_options.user.key_id_template: a console login and an SSH
	// login by the same person must stay distinguishable in an audit log,
	// so an unset template identifies the type instead.
	KeyIDTemplate string `mapstructure:"key_id_template" default:""`

	// LifetimePolicy takes the same grammar as the other types. As with
	// PAM, in practice only the require gate matters: duration tiers
	// against a 30-second certificate and extension grants against an
	// empty ceiling have nothing to do.
	LifetimePolicy LifetimePolicy `mapstructure:"lifetime_policy"`
}

// LifetimePolicy configures certificate issuance duration and extension
// grants based on tiered conditions and source network policies — see
// docs/operations/certificate-lifetime-policy.md. Empty configuration (all
// fields at their zero values) means all certificates receive ValidDuration
// from the enclosing CertOptions* struct.
type LifetimePolicy struct {
	// DefaultDuration is the duration applied when no tier matches. It is
	// required whenever any part of the lifetime policy is configured: a
	// zero value is a startup error rather than a zero-second certificate
	// that fails at signing, several layers from the config line that
	// caused it.
	DefaultDuration time.Duration `mapstructure:"default_duration,string"`

	// OnAbsentClaim states what a missing or unparseable claim resolves to
	// during condition evaluation. The only accepted value is "floor" (the
	// default): the condition fails and the identity falls through to the
	// floor. It must never mean "skip this condition", which is how a
	// missing claim becomes the most generous outcome, so no other value
	// exists; the key is here to make the posture explicit in config.
	OnAbsentClaim string `mapstructure:"on_absent_claim" default:""`

	// DefaultExtensions are the SSH certificate extensions granted when no
	// tier matches, or when the winning tier states no grant_extensions of
	// its own. Applies only when tiers are configured: with tiers present,
	// extension grants are opt-in and start from nothing — an empty or
	// omitted list grants no extensions. Every entry must appear in the
	// enclosing type's extensions ceiling, checked at startup. With no tiers
	// configured, the grant axis is inactive and the type's extensions
	// ceiling alone bounds a request.
	DefaultExtensions []string `mapstructure:"default_extensions"`

	// DefaultEnrollmentDuration is the enrollment-code lifetime applied when
	// no tier matches, clamped to cert_options.service.enrollment_duration.
	// Service certificates only — a startup error on any other type. Zero
	// falls back to the enrollment_duration ceiling.
	DefaultEnrollmentDuration time.Duration `mapstructure:"default_enrollment_duration,string"`

	// Tiers are evaluated in order; the FIRST tier whose when condition the
	// approver's identity satisfies wins, and the list means what it says —
	// tier order is the administrator's job. Numeric thresholds are nested
	// by construction (everyone satisfying at_least 40 also satisfies
	// at_least 30), so write them in descending order; ascending order
	// silently lands every high-score identity in the shortest tier. An
	// empty tiers list means DefaultDuration is always used.
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

// LifetimePolicyTier is one condition-matching rule in LifetimePolicy.Tiers.
type LifetimePolicyTier struct {
	// Name labels the tier for the policy explanation recorded with each
	// approval decision — the answer to "why one hour".
	Name string `mapstructure:"name"`

	// When is the condition an identity must satisfy to take this tier. It
	// is required: a tier without one is a startup error. Group tiers from
	// before the condition grammar move from `group: <name>` to
	// `when: {group: <name>}`.
	When PolicyCondition `mapstructure:"when"`

	// MaxDuration is the longest lifetime certificates in this tier can
	// receive, bounded by the enclosing type's valid_duration ceiling.
	MaxDuration time.Duration `mapstructure:"max_duration,string"`

	// GrantExtensions are the SSH certificate extensions this tier grants.
	// Every entry must appear in the enclosing type's extensions ceiling —
	// a grant outside it is a startup error rather than a silent trim. An
	// empty or omitted list falls back to the policy's default_extensions.
	GrantExtensions []string `mapstructure:"grant_extensions"`

	// MaxEnrollmentDuration tiers the enrollment code's own lifetime,
	// clamped to cert_options.service.enrollment_duration — the lever
	// against a code outliving the conditions that authorized it, without
	// re-evaluating anything at retrieve. Service certificates only — a
	// startup error on any other type. Zero falls back to
	// default_enrollment_duration.
	MaxEnrollmentDuration time.Duration `mapstructure:"max_enrollment_duration,string"`
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

	// Extensions has been replaced by removed_extensions. The old key made
	// an empty list and an omitted field mean opposite things at one length
	// check; the subtractive key retires that. Setting it is a startup
	// error rather than a silently skipped narrowing.
	Extensions []string `mapstructure:"extensions"`

	// RemovedExtensions are SSH certificate extensions requests from this
	// network never receive, subtracted after the tier grant. An empty or
	// omitted list removes nothing — the two spellings agree. Subtractive
	// on purpose: identity grants, network narrows — being on the office
	// range is not a reason to receive a capability the tier withheld.
	RemovedExtensions []string `mapstructure:"removed_extensions"`

	// PinSourceAddress, when true, adds a critical "source-address" SSH option
	// pinning the certificate to this network. Valid only for service certificates;
	// ignored for user certificates (see docs/operations/certificate-lifetime-policy.md
	// "Not for user certificates"). The network must be narrow enough to actually
	// restrict — a /0 or ::/0 with PinSourceAddress=true is a warning sign (the
	// certificate can be used anywhere, pinning is meaningless). Narrowing is
	// enforced by the intersectExtensions helper (source-address is not an extension).
	PinSourceAddress bool `mapstructure:"pin_source_address"`
}
