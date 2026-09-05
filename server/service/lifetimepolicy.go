package service

import (
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// isLifetimePolicyConfigured checks if a LifetimePolicy has any non-zero fields.
func isLifetimePolicyConfigured(p config.LifetimePolicy) bool {
	return p.DefaultDuration > 0 || len(p.Tiers) > 0 || len(p.SourcePolicy) > 0 ||
		len(p.DefaultExtensions) > 0 || p.DefaultEnrollmentDuration > 0
}

// lifetimePolicyEngine evaluates certificate lifetime and options based on
// tiered identity conditions and source network restrictions — see
// https://mnestor.github.io/ssoossh/operations/certificate-policy/.
type lifetimePolicyEngine struct {
	// Parsed/validated policies per type. Each remains nil if no lifetime
	// policy is configured for that type.
	userPolicy    *parsedLifetimePolicy
	servicePolicy *parsedLifetimePolicy
	pamPolicy     *parsedLifetimePolicy
	consolePolicy *parsedLifetimePolicy
}

// parsedLifetimePolicy is the validated, parsed form of a config.LifetimePolicy,
// ready to evaluate.
type parsedLifetimePolicy struct {
	defaultDuration time.Duration
	// defaultExtensions is what the grant axis falls back to when no tier
	// matches or the winning tier states no grants. The axis is active only
	// when tiers are configured; without tiers the type's extensions
	// ceiling alone bounds a request.
	defaultExtensions         []string
	defaultEnrollmentDuration time.Duration
	tiers                     []parsedTier
	sourceRules               []parsedSourceRule
}

// parsedTier is one condition-matching rule, parsed from
// config.LifetimePolicyTier.
type parsedTier struct {
	name                  string
	when                  parsedCondition
	maxDuration           time.Duration
	grantExtensions       []string
	maxEnrollmentDuration time.Duration
}

// parsedSourceRule is one CIDR rule, parsed from config.SourcePolicyEntry.
// It holds the parsed netip.Prefix for fast matching and prefix-length for
// tie-breaking (longer prefix wins).
type parsedSourceRule struct {
	prefix            netip.Prefix
	maxDuration       time.Duration
	removedExtensions []string
	pinSourceAddress  bool
	prefixLen         int // cached prefix length for tie-breaking
}

// policyParseContext carries the per-type facts parseLifetimePolicy
// validates against: the declared extra-claim names, the type's extensions
// ceiling, and whether enrollment-duration keys are meaningful.
type policyParseContext struct {
	label             string
	declaredClaims    map[string]string
	allowedExtensions []string
	isService         bool
}

// newLifetimePolicyEngine parses and validates the policies for each
// certificate type, failing startup if config contains parse errors.
// declaredClaims is authentication.fields.extra, so a condition referencing
// an undeclared claim is caught before it becomes a runtime surprise.
// Returns nil engines for types with no policy configured.
func newLifetimePolicyEngine(opts config.CertificateOptions, declaredClaims map[string]string) (*lifetimePolicyEngine, error) {
	engine := &lifetimePolicyEngine{}

	types := []struct {
		policy config.LifetimePolicy
		ctx    policyParseContext
		slot   **parsedLifetimePolicy
	}{
		{opts.User.LifetimePolicy, policyParseContext{"cert_options.user.lifetime_policy", declaredClaims, opts.User.Extensions, false}, &engine.userPolicy},
		{opts.Service.LifetimePolicy, policyParseContext{"cert_options.service.lifetime_policy", declaredClaims, opts.Service.Extensions, true}, &engine.servicePolicy},
		{opts.PAM.LifetimePolicy, policyParseContext{"cert_options.pam.lifetime_policy", declaredClaims, opts.PAM.Extensions, false}, &engine.pamPolicy},
		{opts.Console.LifetimePolicy, policyParseContext{"cert_options.console.lifetime_policy", declaredClaims, opts.Console.Extensions, false}, &engine.consolePolicy},
	}
	for _, t := range types {
		if !isLifetimePolicyConfigured(t.policy) {
			continue
		}
		parsed, err := parseLifetimePolicy(t.policy, t.ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", t.ctx.label, err)
		}
		*t.slot = parsed
	}

	return engine, nil
}

// parseLifetimePolicy parses a config.LifetimePolicy and validates it
// against ctx, failing on any violation (fail-closed — bad config is a
// startup error, not a runtime degradation). Called only for configured
// policies, so a required-but-zero default_duration is always an error
// here.
func parseLifetimePolicy(lp config.LifetimePolicy, ctx policyParseContext) (*parsedLifetimePolicy, error) {
	// A configured policy with no default_duration used to yield a
	// zero-second certificate the signer rejected, several layers from the
	// config line that caused it. Fail at the config line instead.
	if lp.DefaultDuration <= 0 {
		return nil, fmt.Errorf("default_duration is required when a lifetime policy is configured: without it, an identity matching no tier would receive a zero-second certificate")
	}

	// The key documents the fail-closed posture; no other posture exists.
	if lp.OnAbsentClaim != "" && lp.OnAbsentClaim != "floor" {
		return nil, fmt.Errorf("on_absent_claim only accepts %q: an absent claim must never mean \"skip this condition\"", "floor")
	}

	if !ctx.isService && lp.DefaultEnrollmentDuration != 0 {
		return nil, fmt.Errorf("default_enrollment_duration only applies to service certificates")
	}

	parsed := &parsedLifetimePolicy{
		defaultDuration:           lp.DefaultDuration,
		defaultExtensions:         lp.DefaultExtensions,
		defaultEnrollmentDuration: lp.DefaultEnrollmentDuration,
	}

	// Nothing a tier (or the default) states may exceed the type's
	// extensions ceiling: a grant outside it is a startup error rather
	// than a silent trim, one rule for both axes.
	for _, ext := range lp.DefaultExtensions {
		if !slices.Contains(ctx.allowedExtensions, ext) {
			return nil, fmt.Errorf("default_extensions grants %q, which is outside the type's extensions ceiling", ext)
		}
	}

	for i, t := range lp.Tiers {
		tier, err := parseTier(t, i, ctx)
		if err != nil {
			return nil, err
		}
		parsed.tiers = append(parsed.tiers, *tier)
	}

	// Parse and validate CIDRs.
	for _, entry := range lp.SourcePolicy {
		prefix, err := netip.ParsePrefix(entry.CIDR)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", entry.CIDR, err)
		}

		rule := parsedSourceRule{
			prefix:            prefix,
			maxDuration:       entry.MaxDuration,
			removedExtensions: entry.RemovedExtensions,
			pinSourceAddress:  entry.PinSourceAddress,
			prefixLen:         prefix.Bits(),
		}
		parsed.sourceRules = append(parsed.sourceRules, rule)
	}

	return parsed, nil
}

// parseTier validates one tier against ctx: it must be named (the
// explanation calls it by name), carry a condition, state a positive
// duration, and grant nothing outside the type's extensions ceiling.
// index names an unnamed tier in the error, since it has no name to use.
func parseTier(t config.LifetimePolicyTier, index int, ctx policyParseContext) (*parsedTier, error) {
	if t.Name == "" {
		return nil, fmt.Errorf("tiers[%d] has no name: the name is what the recorded policy explanation calls the tier", index)
	}
	if t.When.IsZero() {
		return nil, fmt.Errorf("tier %q has no when condition (group tiers moved from `group: <name>` to `when: {group: <name>}`)", t.Name)
	}
	when, err := parseCondition(&t.When, ctx.declaredClaims, false)
	if err != nil {
		return nil, fmt.Errorf("tier %q: %w", t.Name, err)
	}
	if t.MaxDuration <= 0 {
		return nil, fmt.Errorf("tier %q needs a max_duration greater than zero", t.Name)
	}
	for _, ext := range t.GrantExtensions {
		if !slices.Contains(ctx.allowedExtensions, ext) {
			return nil, fmt.Errorf("tier %q grants extension %q, which is outside the type's extensions ceiling", t.Name, ext)
		}
	}
	if !ctx.isService && t.MaxEnrollmentDuration != 0 {
		return nil, fmt.Errorf("tier %q sets max_enrollment_duration, which only applies to service certificates", t.Name)
	}
	return &parsedTier{
		name:                  t.Name,
		when:                  *when,
		maxDuration:           t.MaxDuration,
		grantExtensions:       t.GrantExtensions,
		maxEnrollmentDuration: t.MaxEnrollmentDuration,
	}, nil
}

// PolicyExplanation is the structured record of one policy evaluation: the
// winning tier and the condition it matched, the source rule, the ceilings,
// and what they computed to. It is written as a JSON document on the
// approval's decision record — structure over a flat reason string, so the
// shape can grow an axis without a migration.
type PolicyExplanation struct {
	// V versions the document shape, 1 from day one, so a future change
	// never has to guess what it is reading.
	V        int    `json:"v"`
	CertType string `json:"cert_type"`
	// PolicyConfigured is false when the type has no lifetime policy at
	// all, in which case only the ceiling applies.
	PolicyConfigured bool                   `json:"policy_configured"`
	Tier             *TierExplanation       `json:"tier,omitempty"`
	NoTierMatched    bool                   `json:"no_tier_matched,omitempty"`
	DefaultDuration  string                 `json:"default_duration,omitempty"`
	SourceRule       *SourceRuleExplanation `json:"source_rule,omitempty"`
	// Ceiling is the type's valid_duration; EffectiveDuration is what the
	// certificate actually received after every narrowing.
	Ceiling           string                 `json:"ceiling"`
	EffectiveDuration string                 `json:"effective_duration"`
	Enrollment        *EnrollmentExplanation `json:"enrollment,omitempty"`
	Extensions        *ExtensionsExplanation `json:"extensions,omitempty"`
}

// TierExplanation names the winning tier and the condition that matched.
type TierExplanation struct {
	Name        string `json:"name"`
	Condition   string `json:"condition"`
	MaxDuration string `json:"max_duration"`
}

// SourceRuleExplanation records the source rule that narrowed the result.
type SourceRuleExplanation struct {
	CIDR              string   `json:"cidr"`
	MaxDuration       string   `json:"max_duration,omitempty"`
	RemovedExtensions []string `json:"removed_extensions,omitempty"`
	PinSourceAddress  bool     `json:"pin_source_address,omitempty"`
}

// EnrollmentExplanation records how the enrollment code's lifetime was
// computed (service certificates only).
type EnrollmentExplanation struct {
	Ceiling   string `json:"ceiling"`
	Effective string `json:"effective"`
}

// ExtensionsExplanation records each stage of the extensions algebra:
// requested & ceiling & granted - removed.
type ExtensionsExplanation struct {
	Requested []string `json:"requested"`
	Granted   []string `json:"granted,omitempty"`
	// GrantSource says where Granted came from: "tier", "default", or
	// empty when the grant axis is inactive (no tiers configured).
	GrantSource string   `json:"grant_source,omitempty"`
	Removed     []string `json:"removed,omitempty"`
	Effective   []string `json:"effective"`
}

// policyOutcome is one full policy evaluation for one approval: every
// narrowing the engine decided, plus the explanation describing why.
type policyOutcome struct {
	duration time.Duration
	// enrollmentDuration is the enrollment code's lifetime, already clamped
	// to its ceiling. Only meaningful for service certificates.
	enrollmentDuration time.Duration
	// grantActive marks the extension-grant axis in force (tiers are
	// configured); granted is then the tier's or default grant set, and
	// grantSource says which of the two it came from.
	grantActive bool
	granted     []string
	grantSource string
	removed     []string
	pinSource   bool
	explanation PolicyExplanation
}

// evaluate resolves the full policy outcome for one approval: effective
// certificate duration (clamped to ceiling), enrollment-code duration
// (service; clamped to enrollmentCeiling), the extension grant/removal
// sets, and the explanation. identity must carry hydrated Extra fields —
// Approve resolves the users row before any policy evaluation.
func (e *lifetimePolicyEngine) evaluate(
	certType model.CertificateType,
	identity *Identity,
	sourceIP string,
	ceiling time.Duration,
	enrollmentCeiling time.Duration,
) policyOutcome {
	out := policyOutcome{
		duration:           ceiling,
		enrollmentDuration: enrollmentCeiling,
		explanation: PolicyExplanation{
			V:                 1,
			CertType:          string(certType),
			Ceiling:           ceiling.String(),
			EffectiveDuration: ceiling.String(),
		},
	}

	policy := e.policyFor(certType)
	if policy == nil {
		return out
	}
	out.explanation.PolicyConfigured = true

	// Tier axis: first matching condition wins; none matched falls to the
	// policy's defaults on every axis.
	tierDuration := policy.defaultDuration
	enrollmentDuration := policy.defaultEnrollmentDuration
	out.grantActive = len(policy.tiers) > 0
	out.granted = policy.defaultExtensions
	out.grantSource = "default"
	matched := false
	for i := range policy.tiers {
		tier := &policy.tiers[i]
		if !tier.when.evaluate(identity) {
			continue
		}
		matched = true
		tierDuration = tier.maxDuration
		if len(tier.grantExtensions) > 0 {
			out.granted = tier.grantExtensions
			out.grantSource = "tier"
		}
		if tier.maxEnrollmentDuration > 0 {
			enrollmentDuration = tier.maxEnrollmentDuration
		}
		out.explanation.Tier = &TierExplanation{
			Name:        tier.name,
			Condition:   tier.when.String(),
			MaxDuration: tier.maxDuration.String(),
		}
		break
	}
	if !matched {
		out.explanation.NoTierMatched = len(policy.tiers) > 0
		out.explanation.DefaultDuration = policy.defaultDuration.String()
	}
	if !out.grantActive {
		out.granted = nil
		out.grantSource = ""
	}

	// Source axis: the longest-prefix matching rule narrows duration and
	// subtracts extensions; it never grants.
	if rule := matchSourceRule(policy.sourceRules, sourceIP); rule != nil {
		out.removed = rule.removedExtensions
		out.pinSource = rule.pinSourceAddress && certType == model.CertificateTypeService
		if rule.maxDuration > 0 && rule.maxDuration < tierDuration {
			tierDuration = rule.maxDuration
		}
		out.explanation.SourceRule = &SourceRuleExplanation{
			CIDR:              rule.prefix.String(),
			RemovedExtensions: rule.removedExtensions,
			PinSourceAddress:  out.pinSource,
		}
		if rule.maxDuration > 0 {
			out.explanation.SourceRule.MaxDuration = rule.maxDuration.String()
		}
	}

	// Final durations: nothing a tier states may exceed the ceilings.
	out.duration = min(tierDuration, ceiling)
	out.explanation.EffectiveDuration = out.duration.String()

	if certType == model.CertificateTypeService {
		if enrollmentDuration <= 0 {
			enrollmentDuration = enrollmentCeiling
		}
		out.enrollmentDuration = min(enrollmentDuration, enrollmentCeiling)
		out.explanation.Enrollment = &EnrollmentExplanation{
			Ceiling:   enrollmentCeiling.String(),
			Effective: out.enrollmentDuration.String(),
		}
	}

	return out
}

// narrowOptions applies the outcome's extension algebra to options already
// narrowed to the type ceiling: requested & ceiling & granted - removed,
// plus source-address pinning for service certificates. It records the
// stages on the explanation, so the decision record shows each set that
// shaped the result.
func (o *policyOutcome) narrowOptions(narrowed RequestedOptions, sourceIP string) RequestedOptions {
	ext := &ExtensionsExplanation{Requested: narrowed.Extensions}

	if o.grantActive {
		narrowed.Extensions = intersectStrings(narrowed.Extensions, o.granted)
		ext.Granted = o.granted
		ext.GrantSource = o.grantSource
	}
	if len(o.removed) > 0 {
		narrowed.Extensions = subtractStrings(narrowed.Extensions, o.removed)
		ext.Removed = o.removed
	}
	ext.Effective = narrowed.Extensions
	o.explanation.Extensions = ext

	if o.pinSource {
		narrowed.SourceAddresses = []string{sourceIP}
	}
	return narrowed
}

// matchSourceRule finds the longest-prefix matching rule for sourceIP; ties
// go to the stricter (shorter-duration) rule. Returns nil when no rule
// matches or sourceIP is unparseable — err-on-the-side-of-permissive
// rather than crashing, since SourceIP comes from g.ClientIP() or
// SetTrustedProxies and should always parse.
func matchSourceRule(rules []parsedSourceRule, sourceIP string) *parsedSourceRule {
	if len(rules) == 0 {
		return nil
	}

	addr, err := netip.ParseAddr(sourceIP)
	if err != nil {
		slog.Warn("failed to parse source IP for lifetime policy evaluation",
			"source_ip", sourceIP, "error", err)
		return nil
	}

	// Normalize IPv4-mapped IPv6 addresses to their IPv4 equivalent, so
	// ::ffff:10.0.0.1 matches 10.0.0.0/8 — see
	// https://mnestor.github.io/ssoossh/operations/certificate-policy/.
	addr = addr.Unmap()

	var bestMatch *parsedSourceRule
	for i := range rules {
		rule := &rules[i]
		if !rule.prefix.Contains(addr) {
			continue
		}

		switch {
		case bestMatch == nil:
			bestMatch = rule
		case rule.prefixLen > bestMatch.prefixLen:
			// Longer prefix wins (more specific).
			bestMatch = rule
		case rule.prefixLen == bestMatch.prefixLen && rule.maxDuration < bestMatch.maxDuration:
			// Tie in prefix length; stricter rule (shorter duration) wins.
			bestMatch = rule
		}
	}
	return bestMatch
}

// subtractStrings returns the elements of set that do not appear in remove,
// preserving set's order.
func subtractStrings(set, remove []string) []string {
	out := make([]string, 0, len(set))
	for _, s := range set {
		if !slices.Contains(remove, s) {
			out = append(out, s)
		}
	}
	return out
}

// policyFor returns the parsed policy for certType, or nil if none is configured.
func (e *lifetimePolicyEngine) policyFor(certType model.CertificateType) *parsedLifetimePolicy {
	switch certType {
	case model.CertificateTypeUser:
		return e.userPolicy
	case model.CertificateTypeService:
		return e.servicePolicy
	case model.CertificateTypePAM:
		return e.pamPolicy
	case model.CertificateTypeConsole:
		return e.consolePolicy
	default:
		return nil
	}
}

// validateStartupConfig checks that source-network policy is consistent with
// the server's reverse-proxy configuration, logging warnings if a footgun is
// detected (see
// https://mnestor.github.io/ssoossh/operations/certificate-policy/ section
// "The footgun this creates"). Called once at bootstrap, before policies are
// used to evaluate requests.
func (e *lifetimePolicyEngine) validateStartupConfig(trustedProxies []string) {
	// Check all per-type policies.
	for _, policy := range []*parsedLifetimePolicy{e.userPolicy, e.servicePolicy, e.pamPolicy, e.consolePolicy} {
		if policy == nil || len(policy.sourceRules) == 0 {
			continue
		}

		// Source-network policy is configured. Check that trusted_proxies is
		// also configured — if not, every request looks like it came from the
		// proxy's address, silently landing everyone in the most generous tier.
		if len(trustedProxies) == 0 {
			slog.Warn("source-network policy configured without http.trusted_proxies — " +
				"all requests will appear to come from the reverse proxy's address, " +
				"silently landing all clients in the most generous tier. " +
				"See https://mnestor.github.io/ssoossh/operations/certificate-policy/ section 'The footgun this creates'.")
		}
	}
}
