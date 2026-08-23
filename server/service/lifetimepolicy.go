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
	return p.DefaultDuration > 0 || len(p.Tiers) > 0 || len(p.SourcePolicy) > 0
}

// lifetimePolicyEngine evaluates certificate lifetime and options based on
// tiered group membership and source network restrictions — see
// docs/certificate-lifetime-policy.md.
type lifetimePolicyEngine struct {
	// userPolicy and servicePolicy are the parsed/validated policies for each
	// type. They remain nil if no lifetime policy is configured for that type.
	userPolicy    *parsedLifetimePolicy
	servicePolicy *parsedLifetimePolicy
}

// parsedLifetimePolicy is the validated, parsed form of a config.LifetimePolicy,
// ready to evaluate.
type parsedLifetimePolicy struct {
	defaultDuration time.Duration
	tiers           []parsedTier
	sourceRules     []parsedSourceRule
}

// parsedTier is one group-matching rule, parsed from config.LifetimePolicyTier.
type parsedTier struct {
	group       string
	maxDuration time.Duration
}

// parsedSourceRule is one CIDR rule, parsed from config.SourcePolicyEntry.
// It holds the parsed netip.Prefix for fast matching and prefix-length for
// tie-breaking (longer prefix wins).
type parsedSourceRule struct {
	prefix           netip.Prefix
	maxDuration      time.Duration
	extensions       []string
	pinSourceAddress bool
	prefixLen        int // cached prefix length for tie-breaking
}

// newLifetimePolicyEngine parses and validates the policies for each
// certificate type, failing startup if config contains parse errors.
// Returns nil engines for types with no policy configured.
func newLifetimePolicyEngine(opts config.CertificateOptions) (*lifetimePolicyEngine, error) {
	engine := &lifetimePolicyEngine{}

	if isLifetimePolicyConfigured(opts.User.LifetimePolicy) {
		parsed, err := parseLifetimePolicy(opts.User.LifetimePolicy)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user lifetime policy: %w", err)
		}
		engine.userPolicy = parsed
	}

	if isLifetimePolicyConfigured(opts.Service.LifetimePolicy) {
		parsed, err := parseLifetimePolicy(opts.Service.LifetimePolicy)
		if err != nil {
			return nil, fmt.Errorf("failed to parse service lifetime policy: %w", err)
		}
		engine.servicePolicy = parsed
	}

	return engine, nil
}

// parseLifetimePolicy parses a config.LifetimePolicy's CIDR rules and validates
// their syntax, failing if any CIDR is unparseable (fail-closed — bad config
// is a startup error, not a runtime degradation).
func parseLifetimePolicy(lp config.LifetimePolicy) (*parsedLifetimePolicy, error) {
	parsed := &parsedLifetimePolicy{
		defaultDuration: lp.DefaultDuration,
	}

	// Parse tiers as-is; they're just strings and groups, no validation needed.
	for _, t := range lp.Tiers {
		parsed.tiers = append(parsed.tiers, parsedTier{
			group:       t.Group,
			maxDuration: t.MaxDuration,
		})
	}

	// Parse and validate CIDRs.
	for _, entry := range lp.SourcePolicy {
		prefix, err := netip.ParsePrefix(entry.CIDR)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", entry.CIDR, err)
		}

		rule := parsedSourceRule{
			prefix:           prefix,
			maxDuration:      entry.MaxDuration,
			extensions:       entry.Extensions,
			pinSourceAddress: entry.PinSourceAddress,
			prefixLen:        prefix.Bits(),
		}
		parsed.sourceRules = append(parsed.sourceRules, rule)
	}

	return parsed, nil
}

// evaluateDuration resolves the effective certificate duration for a given
// certificate type, identity (for tier matching), source IP, and type ceiling
// (ValidDuration from config). Returns the computed duration and any relevant
// narrowing rule(s) that applied. The final result is always clamped to ceiling.
//
// Returned narrowingReason describes which rule(s) applied (tier and/or source).
// It may be used for logging or to display on the approval UI.
func (e *lifetimePolicyEngine) evaluateDuration(
	certType model.CertificateType,
	identity *Identity,
	sourceIP string,
	typeCeiling time.Duration,
) (duration time.Duration, narrowingReason string, err error) {
	// No policy configured for this type — use type ceiling unchanged.
	if !e.hasPolicy(certType) {
		return typeCeiling, "", nil
	}

	policy := e.policyFor(certType)
	if policy == nil {
		return typeCeiling, "", nil
	}

	// Step 1: Evaluate tier duration.
	tierDuration := e.evaluateTier(policy, identity.Groups)
	var reason string
	if tierDuration != policy.defaultDuration {
		// A tier matched; record that fact.
		matchedTier := e.matchedTier(policy, identity.Groups)
		reason = fmt.Sprintf("tier %q: %v", matchedTier, tierDuration)
	} else if len(policy.tiers) > 0 {
		// Tiers were configured but none matched; record the fallback.
		reason = fmt.Sprintf("no tier matched, default: %v", tierDuration)
	}

	// Step 2: Evaluate source rule duration.
	sourceRuleDuration, sourceReason := e.evaluateSourceRule(policy, sourceIP)
	if sourceReason != "" {
		if reason != "" {
			reason += "; " + sourceReason
		} else {
			reason = sourceReason
		}
	}

	// Step 3: Compute final duration as min of tier, source rule, and ceiling.
	effective := tierDuration
	if sourceRuleDuration > 0 && sourceRuleDuration < effective {
		effective = sourceRuleDuration
	}
	if effective > typeCeiling {
		effective = typeCeiling
	}

	return effective, reason, nil
}

// evaluateTier returns the max duration from the first tier whose group appears
// in groups. If no tier matches, returns policy.defaultDuration (which may be
// zero, in which case the caller uses typeCeiling).
func (e *lifetimePolicyEngine) evaluateTier(policy *parsedLifetimePolicy, groups []string) time.Duration {
	for _, tier := range policy.tiers {
		if slices.Contains(groups, tier.group) {
			return tier.maxDuration
		}
	}
	return policy.defaultDuration
}

// matchedTier returns the group of the first tier whose group appears in groups,
// or empty string if none match. Used for logging/UI display only.
func (e *lifetimePolicyEngine) matchedTier(policy *parsedLifetimePolicy, groups []string) string {
	for _, tier := range policy.tiers {
		if slices.Contains(groups, tier.group) {
			return tier.group
		}
	}
	return ""
}

// evaluateSourceRule finds the longest-prefix matching rule for sourceIP and
// returns its max duration and a string describing it for logging/UI purposes.
// If no rule matches, returns 0 duration and empty string.
func (e *lifetimePolicyEngine) evaluateSourceRule(policy *parsedLifetimePolicy, sourceIP string) (time.Duration, string) {
	if len(policy.sourceRules) == 0 {
		return 0, ""
	}

	addr, err := netip.ParseAddr(sourceIP)
	if err != nil {
		// Invalid source IP — treat as no match. This should not happen in
		// practice (SourceIP comes from g.ClientIP() or SetTrustedProxies),
		// but err-on-the-side-of-permissive rather than crashing.
		slog.Warn("failed to parse source IP for lifetime policy evaluation",
			"source_ip", sourceIP, "error", err)
		return 0, ""
	}

	// Normalize IPv4-mapped IPv6 addresses to their IPv4 equivalent, so
	// ::ffff:10.0.0.1 matches 10.0.0.0/8 — see docs/certificate-lifetime-policy.md.
	addr = addr.Unmap()

	// Find the longest-prefix match; ties go to the stricter rule.
	var bestMatch *parsedSourceRule
	for i := range policy.sourceRules {
		rule := &policy.sourceRules[i]
		if !rule.prefix.Contains(addr) {
			continue
		}

		if bestMatch == nil {
			bestMatch = rule
		} else if rule.prefixLen > bestMatch.prefixLen {
			// Longer prefix wins (more specific).
			bestMatch = rule
		} else if rule.prefixLen == bestMatch.prefixLen && rule.maxDuration < bestMatch.maxDuration {
			// Tie in prefix length; stricter rule (shorter duration) wins.
			bestMatch = rule
		}
	}

	if bestMatch == nil {
		return 0, ""
	}

	return bestMatch.maxDuration, fmt.Sprintf("source %s: %v", bestMatch.prefix, bestMatch.maxDuration)
}

// narrowRequestedOptionsWithPolicy narrows requested options based on the
// matching source policy rule (if any), for certificate types that support it.
// This applies the second half of the "narrowing only" invariant: the source
// rule can restrict extensions and (for service certs) add pin_source_address,
// but never grant them.
//
// For user certificates: restrictions apply to extensions only, never to
// source-address options (see docs/certificate-lifetime-policy.md).
//
// For service certificates: restrictions apply to both extensions and
// source-address via pin_source_address (if the source rule enables it).
func (e *lifetimePolicyEngine) narrowRequestedOptionsWithPolicy(
	certType model.CertificateType,
	identity *Identity,
	sourceIP string,
	requested RequestedOptions,
) RequestedOptions {
	// No policy configured, or no source rule matched.
	if !e.hasPolicy(certType) {
		return requested
	}

	policy := e.policyFor(certType)
	if policy == nil {
		return requested
	}

	addr, err := netip.ParseAddr(sourceIP)
	if err != nil {
		return requested
	}
	addr = addr.Unmap()

	// Find the longest-prefix matching source rule.
	var bestMatch *parsedSourceRule
	for i := range policy.sourceRules {
		rule := &policy.sourceRules[i]
		if !rule.prefix.Contains(addr) {
			continue
		}

		if bestMatch == nil {
			bestMatch = rule
		} else if rule.prefixLen > bestMatch.prefixLen {
			bestMatch = rule
		} else if rule.prefixLen == bestMatch.prefixLen && rule.maxDuration < bestMatch.maxDuration {
			bestMatch = rule
		}
	}

	if bestMatch == nil {
		return requested
	}

	// Apply narrowing from the matched rule.
	narrowed := requested

	// Restrict extensions if the rule specifies any.
	if len(bestMatch.extensions) > 0 {
		narrowed.Extensions = intersectStrings(requested.Extensions, bestMatch.extensions)
	}

	// For service certificates, add source-address pinning if enabled.
	if certType == model.CertificateTypeService && bestMatch.pinSourceAddress {
		narrowed.SourceAddresses = []string{sourceIP}
	}

	return narrowed
}

// hasPolicy returns true if a policy is configured for certType.
func (e *lifetimePolicyEngine) hasPolicy(certType model.CertificateType) bool {
	return e.policyFor(certType) != nil
}

// policyFor returns the parsed policy for certType, or nil if none is configured.
func (e *lifetimePolicyEngine) policyFor(certType model.CertificateType) *parsedLifetimePolicy {
	switch certType {
	case model.CertificateTypeUser:
		return e.userPolicy
	case model.CertificateTypeService:
		return e.servicePolicy
	default:
		return nil
	}
}

// validateStartupConfig checks that source-network policy is consistent with
// the server's reverse-proxy configuration, logging warnings if a footgun is
// detected (see docs/certificate-lifetime-policy.md section "The footgun
// this creates"). Called once at bootstrap, before policies are used to evaluate
// requests.
func (e *lifetimePolicyEngine) validateStartupConfig(trustedProxies []string) {
	// Check both user and service policies.
	for _, policy := range []*parsedLifetimePolicy{e.userPolicy, e.servicePolicy} {
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
				"See docs/certificate-lifetime-policy.md section 'The footgun this creates'.")
		}
	}
}
