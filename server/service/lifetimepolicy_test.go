package service

import (
	"bytes"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// groupTier builds the config form of a plain group tier, the shape that
// replaced the pre-conditions `group:` key.
func groupTier(name, group string, d time.Duration) config.LifetimePolicyTier {
	return config.LifetimePolicyTier{
		Name:        name,
		When:        config.PolicyCondition{Group: group},
		MaxDuration: d,
	}
}

// parsedGroupTier is groupTier's already-parsed equivalent, for tests that
// build an engine directly rather than through config parsing.
func parsedGroupTier(name, group string, d time.Duration) parsedTier {
	return parsedTier{
		name:        name,
		when:        parsedCondition{kind: condGroup, group: group, rendered: `group "` + group + `"`},
		maxDuration: d,
	}
}

// claimTiers is the declared-claims map every numeric-condition test parses
// against; a condition naming an undeclared claim is a startup error.
var claimTiers = map[string]string{"loc": "level_of_confidence"}

func float64Ptr(f float64) *float64 { return &f }

func TestParseLifetimePolicy(t *testing.T) {
	baseCtx := policyParseContext{
		label:             "cert_options.user.lifetime_policy",
		declaredClaims:    claimTiers,
		allowedExtensions: []string{"permit-pty", "permit-port-forwarding"},
	}

	tests := []struct {
		name    string
		policy  config.LifetimePolicy
		ctx     *policyParseContext
		wantErr bool
		check   func(t *testing.T, p *parsedLifetimePolicy)
	}{
		{
			name: "valid tiers",
			policy: config.LifetimePolicy{
				DefaultDuration: 10 * time.Hour,
				Tiers: []config.LifetimePolicyTier{
					groupTier("admins", "admin", 24*time.Hour),
					groupTier("users", "users", 8*time.Hour),
				},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if p.defaultDuration != 10*time.Hour {
					t.Errorf("expected 10h defaultDuration, got %v", p.defaultDuration)
				}
				if len(p.tiers) != 2 {
					t.Fatalf("expected 2 tiers, got %d", len(p.tiers))
				}
				if p.tiers[0].name != "admins" || p.tiers[0].maxDuration != 24*time.Hour {
					t.Errorf("unexpected tier[0]: %+v", p.tiers[0])
				}
			},
		},
		{
			name: "numeric claim tier",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				Tiers: []config.LifetimePolicyTier{{
					Name:        "cleared",
					When:        config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(40)},
					MaxDuration: 8 * time.Hour,
				}},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if p.tiers[0].when.kind != condClaim {
					t.Errorf("expected a claim condition, got kind %v", p.tiers[0].when.kind)
				}
			},
		},
		{
			name: "valid IPv4 CIDR",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				SourcePolicy: []config.SourcePolicyEntry{
					{CIDR: "10.0.0.0/8", MaxDuration: 10 * time.Hour},
					{CIDR: "192.168.0.0/16", MaxDuration: 4 * time.Hour},
				},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if len(p.sourceRules) != 2 {
					t.Fatalf("expected 2 sourceRules, got %d", len(p.sourceRules))
				}
				if p.sourceRules[0].prefix != netip.MustParsePrefix("10.0.0.0/8") {
					t.Errorf("unexpected prefix: %v", p.sourceRules[0].prefix)
				}
				if p.sourceRules[0].prefixLen != 8 {
					t.Errorf("expected prefixLen 8, got %d", p.sourceRules[0].prefixLen)
				}
			},
		},
		{
			name: "valid IPv6 CIDR",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				SourcePolicy:    []config.SourcePolicyEntry{{CIDR: "2001:db8::/32", MaxDuration: 4 * time.Hour}},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if p.sourceRules[0].prefixLen != 32 {
					t.Errorf("expected prefixLen 32, got %d", p.sourceRules[0].prefixLen)
				}
			},
		},
		{
			name: "invalid CIDR",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				SourcePolicy:    []config.SourcePolicyEntry{{CIDR: "not-a-cidr", MaxDuration: 10 * time.Hour}},
			},
			wantErr: true,
		},
		{
			name: "source rule with removed extensions",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				SourcePolicy: []config.SourcePolicyEntry{{
					CIDR:              "10.0.0.0/8",
					MaxDuration:       10 * time.Hour,
					RemovedExtensions: []string{"permit-port-forwarding"},
				}},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if len(p.sourceRules[0].removedExtensions) != 1 {
					t.Errorf("expected 1 removed extension, got %v", p.sourceRules[0].removedExtensions)
				}
			},
		},
		{
			name: "source rule with pin_source_address",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				SourcePolicy: []config.SourcePolicyEntry{{
					CIDR: "10.0.0.0/8", MaxDuration: 10 * time.Hour, PinSourceAddress: true,
				}},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if !p.sourceRules[0].pinSourceAddress {
					t.Error("expected pinSourceAddress=true")
				}
			},
		},
		{
			name: "grant within the extensions ceiling",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				Tiers: []config.LifetimePolicyTier{{
					Name:            "cleared",
					When:            config.PolicyCondition{Group: "admin"},
					MaxDuration:     time.Hour,
					GrantExtensions: []string{"permit-pty"},
				}},
			},
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if len(p.tiers[0].grantExtensions) != 1 {
					t.Errorf("expected the grant to survive parsing, got %v", p.tiers[0].grantExtensions)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := baseCtx
			if tt.ctx != nil {
				ctx = *tt.ctx
			}
			p, err := parseLifetimePolicy(tt.policy, ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error: got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

// The engine used to return a zero-second certificate when tiers were
// configured, no default_duration was set, and nothing matched — the signer
// then rejected it with an error about certificate validity, several layers
// from the config line that caused it (finding F3). It is now a startup
// error naming the setting.
func TestParseLifetimePolicy_ShouldRejectAConfiguredPolicyWithNoDefaultDuration(t *testing.T) {
	t.Parallel()

	_, err := parseLifetimePolicy(config.LifetimePolicy{
		Tiers: []config.LifetimePolicyTier{groupTier("admins", "admin", 24*time.Hour)},
	}, policyParseContext{declaredClaims: claimTiers})

	if err == nil {
		t.Fatal("parseLifetimePolicy() error = nil, want an error naming default_duration")
	}
	if !strings.Contains(err.Error(), "default_duration") {
		t.Errorf("error %q should name default_duration", err)
	}
}

// A grant outside the type's extensions ceiling is a startup error rather
// than a silent trim: nothing a tier states may exceed the ceiling, and an
// operator who wrote the grant should learn it has no effect.
func TestParseLifetimePolicy_ShouldRejectGrantsOutsideTheExtensionsCeiling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy config.LifetimePolicy
	}{
		{
			name: "tier grant",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				Tiers: []config.LifetimePolicyTier{{
					Name:            "cleared",
					When:            config.PolicyCondition{Group: "admin"},
					MaxDuration:     time.Hour,
					GrantExtensions: []string{"permit-port-forwarding"},
				}},
			},
		},
		{
			name: "default grant",
			policy: config.LifetimePolicy{
				DefaultDuration:   time.Hour,
				DefaultExtensions: []string{"permit-port-forwarding"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseLifetimePolicy(tt.policy, policyParseContext{
				declaredClaims:    claimTiers,
				allowedExtensions: []string{"permit-pty"},
			})
			if err == nil {
				t.Fatal("parseLifetimePolicy() error = nil, want a ceiling violation")
			}
			if !strings.Contains(err.Error(), "ceiling") {
				t.Errorf("error %q should mention the ceiling", err)
			}
		})
	}
}

// Enrollment-code tiering is a service-only clock. Setting it elsewhere is
// a mistake worth naming at startup rather than silently ignoring.
func TestParseLifetimePolicy_ShouldRejectEnrollmentKeysOnNonServiceTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy config.LifetimePolicy
	}{
		{
			name: "default_enrollment_duration",
			policy: config.LifetimePolicy{
				DefaultDuration:           time.Hour,
				DefaultEnrollmentDuration: 720 * time.Hour,
			},
		},
		{
			name: "tier max_enrollment_duration",
			policy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				Tiers: []config.LifetimePolicyTier{{
					Name:                  "cleared",
					When:                  config.PolicyCondition{Group: "admin"},
					MaxDuration:           time.Hour,
					MaxEnrollmentDuration: 720 * time.Hour,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseLifetimePolicy(tt.policy, policyParseContext{declaredClaims: claimTiers})
			if err == nil {
				t.Fatal("parseLifetimePolicy() error = nil, want a service-only error")
			}
			if !strings.Contains(err.Error(), "service") {
				t.Errorf("error %q should say the key is service-only", err)
			}
		})
	}
}

// on_absent_claim exists to state the fail-closed posture in config. Any
// other value would read as a request for a posture that must not exist,
// so it is rejected rather than ignored.
func TestParseLifetimePolicy_ShouldRejectAnyOnAbsentClaimBesidesFloor(t *testing.T) {
	t.Parallel()

	_, err := parseLifetimePolicy(config.LifetimePolicy{
		DefaultDuration: time.Hour,
		OnAbsentClaim:   "ignore",
	}, policyParseContext{declaredClaims: claimTiers})

	if err == nil {
		t.Fatal("parseLifetimePolicy() error = nil, want a rejection of on_absent_claim: ignore")
	}
}

func TestEvaluate_Tiers(t *testing.T) {
	tests := []struct {
		name     string
		policy   *parsedLifetimePolicy
		groups   []string
		ceiling  time.Duration
		expected time.Duration
	}{
		{
			name:     "no tiers configured",
			policy:   &parsedLifetimePolicy{defaultDuration: 10 * time.Hour},
			groups:   []string{"anyone"},
			ceiling:  24 * time.Hour,
			expected: 10 * time.Hour,
		},
		{
			name: "first tier matches",
			policy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				tiers: []parsedTier{
					parsedGroupTier("admins", "admin", 24*time.Hour),
					parsedGroupTier("users", "users", 8*time.Hour),
				},
			},
			groups:   []string{"admin", "users"},
			ceiling:  24 * time.Hour,
			expected: 24 * time.Hour,
		},
		{
			name: "second tier matches",
			policy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				tiers: []parsedTier{
					parsedGroupTier("admins", "admin", 24*time.Hour),
					parsedGroupTier("users", "users", 8*time.Hour),
				},
			},
			groups:   []string{"users"},
			ceiling:  24 * time.Hour,
			expected: 8 * time.Hour,
		},
		{
			name: "no tier matches, use default",
			policy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				tiers:           []parsedTier{parsedGroupTier("admins", "admin", 24*time.Hour)},
			},
			groups:   []string{"guests"},
			ceiling:  24 * time.Hour,
			expected: time.Hour,
		},
		{
			name: "ceiling clamps the tier",
			policy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				tiers:           []parsedTier{parsedGroupTier("admins", "admin", 24*time.Hour)},
			},
			groups:   []string{"admin"},
			ceiling:  5 * time.Hour,
			expected: 5 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &lifetimePolicyEngine{userPolicy: tt.policy}
			got := engine.evaluate(model.CertificateTypeUser, &Identity{Groups: tt.groups}, "10.0.0.1", tt.ceiling, 0)
			if got.duration != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got.duration)
			}
		})
	}
}

// The whole point of the typed numeric accessor: compared as strings, "9"
// sorts above "40", so a score of 9 would take the tier meant for 40 and
// up. These cases are the ones that would pass under string comparison and
// must not.
func TestEvaluate_ShouldCompareClaimsNumericallyNotLexicographically(t *testing.T) {
	t.Parallel()

	policy := &parsedLifetimePolicy{
		defaultDuration: 15 * time.Minute,
		tiers: []parsedTier{{
			name:        "cleared",
			when:        parsedCondition{kind: condClaim, claim: "loc", atLeast: float64Ptr(40), rendered: "claim loc >= 40"},
			maxDuration: 8 * time.Hour,
		}},
	}
	engine := &lifetimePolicyEngine{userPolicy: policy}

	tests := []struct {
		name     string
		score    string
		expected time.Duration
	}{
		{name: "single digit does not out-rank the threshold", score: "9", expected: 15 * time.Minute},
		{name: "below the threshold falls to default", score: "39", expected: 15 * time.Minute},
		{name: "at the threshold is inclusive", score: "40", expected: 8 * time.Hour},
		{name: "above the threshold matches", score: "100", expected: 8 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identity := &Identity{Extra: map[string]extraValue{"loc": scalarExtra(tt.score)}}
			got := engine.evaluate(model.CertificateTypeUser, identity, "10.0.0.1", 24*time.Hour, 0)
			if got.duration != tt.expected {
				t.Errorf("score %q: expected %v, got %v", tt.score, tt.expected, got.duration)
			}
		})
	}
}

// An absent claim must never be neutral: it resolves to the floor on every
// axis, so a missing score can never be the most generous outcome.
func TestEvaluate_ShouldResolveAnAbsentOrUnparseableClaimToTheFloor(t *testing.T) {
	t.Parallel()

	policy := &parsedLifetimePolicy{
		defaultDuration: 15 * time.Minute,
		tiers: []parsedTier{{
			name:        "cleared",
			when:        parsedCondition{kind: condClaim, claim: "loc", atLeast: float64Ptr(40), rendered: "claim loc >= 40"},
			maxDuration: 8 * time.Hour,
		}},
	}
	engine := &lifetimePolicyEngine{userPolicy: policy}

	tests := []struct {
		name     string
		identity *Identity
	}{
		{name: "claim was never captured", identity: &Identity{}},
		{name: "claim captured empty", identity: &Identity{Extra: map[string]extraValue{"loc": scalarExtra("")}}},
		{name: "claim is not a number", identity: &Identity{Extra: map[string]extraValue{"loc": scalarExtra("high")}}},
		{name: "claim is a list", identity: &Identity{Extra: map[string]extraValue{"loc": listExtra([]string{"40"})}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := engine.evaluate(model.CertificateTypeUser, tt.identity, "10.0.0.1", 24*time.Hour, 0)
			if got.duration != 15*time.Minute {
				t.Errorf("expected the floor (15m), got %v", got.duration)
			}
		})
	}
}

func TestEvaluate_SourceRules(t *testing.T) {
	tests := []struct {
		name        string
		policy      *parsedLifetimePolicy
		sourceIP    string
		expectedDur time.Duration
	}{
		{
			name:        "no source rules configured",
			policy:      &parsedLifetimePolicy{defaultDuration: 10 * time.Hour},
			sourceIP:    "10.0.0.1",
			expectedDur: 10 * time.Hour,
		},
		{
			name: "direct IPv4 match narrows",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("10.0.0.0/8"), maxDuration: 2 * time.Hour, prefixLen: 8},
				},
			},
			sourceIP:    "10.1.2.3",
			expectedDur: 2 * time.Hour,
		},
		{
			name: "IPv4-mapped IPv6 normalized to IPv4",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("10.0.0.0/8"), maxDuration: 2 * time.Hour, prefixLen: 8},
				},
			},
			sourceIP:    "::ffff:10.1.2.3",
			expectedDur: 2 * time.Hour,
		},
		{
			name: "IPv6 match",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("2001:db8::/32"), maxDuration: 4 * time.Hour, prefixLen: 32},
				},
			},
			sourceIP:    "2001:db8::1",
			expectedDur: 4 * time.Hour,
		},
		{
			name: "longest prefix wins",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("10.0.0.0/8"), maxDuration: 10 * time.Hour, prefixLen: 8},
					{prefix: netip.MustParsePrefix("10.1.0.0/16"), maxDuration: 5 * time.Hour, prefixLen: 16},
				},
			},
			sourceIP:    "10.1.2.3",
			expectedDur: 5 * time.Hour,
		},
		{
			name: "tie goes to stricter (shorter) duration",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("10.0.0.0/24"), maxDuration: 10 * time.Hour, prefixLen: 24},
					{prefix: netip.MustParsePrefix("10.0.0.0/24"), maxDuration: 5 * time.Hour, prefixLen: 24},
				},
			},
			sourceIP:    "10.0.0.5",
			expectedDur: 5 * time.Hour,
		},
		{
			name: "no match leaves the tier duration",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("10.0.0.0/8"), maxDuration: 2 * time.Hour, prefixLen: 8},
				},
			},
			sourceIP:    "192.168.1.1",
			expectedDur: 10 * time.Hour,
		},
		{
			name: "invalid source IP matches nothing",
			policy: &parsedLifetimePolicy{
				defaultDuration: 10 * time.Hour,
				sourceRules: []parsedSourceRule{
					{prefix: netip.MustParsePrefix("10.0.0.0/8"), maxDuration: 2 * time.Hour, prefixLen: 8},
				},
			},
			sourceIP:    "invalid-ip",
			expectedDur: 10 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &lifetimePolicyEngine{userPolicy: tt.policy}
			got := engine.evaluate(model.CertificateTypeUser, &Identity{}, tt.sourceIP, 24*time.Hour, 0)
			if got.duration != tt.expectedDur {
				t.Errorf("expected %v, got %v", tt.expectedDur, got.duration)
			}
		})
	}
}

func TestNarrowOptions(t *testing.T) {
	tests := []struct {
		name     string
		engine   *lifetimePolicyEngine
		certType model.CertificateType
		identity *Identity
		sourceIP string
		input    RequestedOptions
		check    func(t *testing.T, result RequestedOptions)
	}{
		{
			name:     "no policy, no narrowing",
			engine:   &lifetimePolicyEngine{},
			certType: model.CertificateTypeUser,
			identity: &Identity{},
			sourceIP: "10.0.0.1",
			input:    RequestedOptions{Extensions: []string{"permit-pty", "permit-port-forwarding"}},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.Extensions) != 2 {
					t.Errorf("expected 2 extensions, got %v", result.Extensions)
				}
			},
		},
		{
			name: "source rule removes an extension",
			engine: &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				sourceRules: []parsedSourceRule{{
					prefix:            netip.MustParsePrefix("0.0.0.0/0"),
					maxDuration:       15 * time.Minute,
					removedExtensions: []string{"permit-port-forwarding"},
				}},
			}},
			certType: model.CertificateTypeUser,
			identity: &Identity{},
			sourceIP: "192.168.1.1",
			input:    RequestedOptions{Extensions: []string{"permit-pty", "permit-port-forwarding"}},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.Extensions) != 1 || result.Extensions[0] != "permit-pty" {
					t.Errorf("expected [permit-pty], got %v", result.Extensions)
				}
			},
		},
		{
			name: "service cert with source address pinning",
			engine: &lifetimePolicyEngine{servicePolicy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				sourceRules: []parsedSourceRule{{
					prefix:           netip.MustParsePrefix("0.0.0.0/0"),
					maxDuration:      15 * time.Minute,
					pinSourceAddress: true,
				}},
			}},
			certType: model.CertificateTypeService,
			identity: &Identity{},
			sourceIP: "192.168.1.5",
			input:    RequestedOptions{},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.SourceAddresses) != 1 || result.SourceAddresses[0] != "192.168.1.5" {
					t.Errorf("expected [192.168.1.5], got %v", result.SourceAddresses)
				}
			},
		},
		{
			name: "user cert ignores source address pinning",
			engine: &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				sourceRules: []parsedSourceRule{{
					prefix:           netip.MustParsePrefix("0.0.0.0/0"),
					maxDuration:      15 * time.Minute,
					pinSourceAddress: true,
				}},
			}},
			certType: model.CertificateTypeUser,
			identity: &Identity{},
			sourceIP: "192.168.1.5",
			input:    RequestedOptions{},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.SourceAddresses) != 0 {
					t.Errorf("expected empty SourceAddresses for user cert, got %v", result.SourceAddresses)
				}
			},
		},
		{
			name: "tier grant bounds what a request may carry",
			engine: &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				tiers: []parsedTier{{
					name:            "cleared",
					when:            parsedCondition{kind: condGroup, group: "admin", rendered: `group "admin"`},
					maxDuration:     time.Hour,
					grantExtensions: []string{"permit-pty"},
				}},
			}},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"admin"}},
			sourceIP: "10.0.0.1",
			input:    RequestedOptions{Extensions: []string{"permit-pty", "permit-port-forwarding"}},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.Extensions) != 1 || result.Extensions[0] != "permit-pty" {
					t.Errorf("expected only the granted [permit-pty], got %v", result.Extensions)
				}
			},
		},
		{
			name: "an unmatched tier falls to default_extensions, which grants nothing",
			engine: &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				tiers: []parsedTier{{
					name:            "cleared",
					when:            parsedCondition{kind: condGroup, group: "admin", rendered: `group "admin"`},
					maxDuration:     time.Hour,
					grantExtensions: []string{"permit-pty"},
				}},
			}},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"contractors"}},
			sourceIP: "10.0.0.1",
			input:    RequestedOptions{Extensions: []string{"permit-pty"}},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.Extensions) != 0 {
					t.Errorf("expected no extensions granted, got %v", result.Extensions)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := tt.engine.evaluate(tt.certType, tt.identity, tt.sourceIP, 24*time.Hour, 0)
			result := outcome.narrowOptions(tt.input, tt.sourceIP)
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

// A source rule's empty extensions list used to skip the intersection
// entirely and apply no restriction, while an empty list at the type level
// denied everything — one len() == 0 check carrying two opposite meanings
// (finding F6). The subtractive key makes the two spellings agree.
func TestNarrowOptions_ShouldTreatEmptyAndOmittedRemovalsAlike(t *testing.T) {
	t.Parallel()

	requested := RequestedOptions{Extensions: []string{"permit-pty", "permit-agent-forwarding"}}

	tests := []struct {
		name    string
		removed []string
	}{
		{name: "omitted", removed: nil},
		{name: "empty", removed: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			engine := &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
				defaultDuration: time.Hour,
				sourceRules: []parsedSourceRule{{
					prefix:            netip.MustParsePrefix("0.0.0.0/0"),
					maxDuration:       time.Hour,
					removedExtensions: tt.removed,
				}},
			}}

			outcome := engine.evaluate(model.CertificateTypeUser, &Identity{}, "10.0.0.1", 24*time.Hour, 0)
			got := outcome.narrowOptions(requested, "10.0.0.1")
			if len(got.Extensions) != 2 {
				t.Errorf("%s removals should remove nothing, got %v", tt.name, got.Extensions)
			}
		})
	}
}

// The enrollment code's own lifetime is the lever against a code outliving
// the conditions that authorized it, so it tiers alongside the certificate
// and clamps to the service type's enrollment_duration ceiling.
func TestEvaluate_ShouldTierTheEnrollmentCodeLifetime(t *testing.T) {
	t.Parallel()

	engine := &lifetimePolicyEngine{servicePolicy: &parsedLifetimePolicy{
		defaultDuration:           time.Hour,
		defaultEnrollmentDuration: 720 * time.Hour,
		tiers: []parsedTier{{
			name:                  "cleared-owner",
			when:                  parsedCondition{kind: condGroup, group: "cleared", rendered: `group "cleared"`},
			maxDuration:           24 * time.Hour,
			maxEnrollmentDuration: 8760 * time.Hour,
		}},
	}}

	tests := []struct {
		name     string
		groups   []string
		ceiling  time.Duration
		expected time.Duration
	}{
		{name: "matched tier takes its own code lifetime", groups: []string{"cleared"}, ceiling: 8760 * time.Hour, expected: 8760 * time.Hour},
		{name: "unmatched falls to the default", groups: []string{"other"}, ceiling: 8760 * time.Hour, expected: 720 * time.Hour},
		{name: "ceiling clamps the tier", groups: []string{"cleared"}, ceiling: 1000 * time.Hour, expected: 1000 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := engine.evaluate(model.CertificateTypeService, &Identity{Groups: tt.groups}, "10.0.0.1", 24*time.Hour, tt.ceiling)
			if got.enrollmentDuration != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got.enrollmentDuration)
			}
		})
	}
}

// The explanation is the whole point of recording a decision: an operator
// asking "why one hour" gets the tier's name, the condition it matched, and
// the ceiling that bounded it, rather than a bare duration.
func TestEvaluate_ShouldExplainTheWinningTierAndCeiling(t *testing.T) {
	t.Parallel()

	engine := &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
		defaultDuration: 15 * time.Minute,
		tiers: []parsedTier{{
			name:        "cleared",
			when:        parsedCondition{kind: condClaim, claim: "loc", atLeast: float64Ptr(40), rendered: "claim loc >= 40"},
			maxDuration: 8 * time.Hour,
		}},
	}}

	identity := &Identity{Extra: map[string]extraValue{"loc": scalarExtra("40")}}
	got := engine.evaluate(model.CertificateTypeUser, identity, "10.0.0.1", 4*time.Hour, 0)

	if got.explanation.V != 1 {
		t.Errorf("explanation version = %d, want 1", got.explanation.V)
	}
	if got.explanation.Tier == nil {
		t.Fatal("expected a tier on the explanation")
	}
	if got.explanation.Tier.Name != "cleared" {
		t.Errorf("tier name = %q, want %q", got.explanation.Tier.Name, "cleared")
	}
	if got.explanation.Tier.Condition != "claim loc >= 40" {
		t.Errorf("tier condition = %q, want the rendered condition", got.explanation.Tier.Condition)
	}
	// The ceiling bounded an 8h tier down to 4h, and both numbers appear.
	if got.explanation.Ceiling != "4h0m0s" || got.explanation.EffectiveDuration != "4h0m0s" {
		t.Errorf("ceiling %q / effective %q, want both 4h0m0s", got.explanation.Ceiling, got.explanation.EffectiveDuration)
	}
}

// An unmatched tier list is worth recording too: "nothing matched, so the
// default applied" is a different answer from "no policy is configured".
func TestEvaluate_ShouldExplainWhenNoTierMatched(t *testing.T) {
	t.Parallel()

	engine := &lifetimePolicyEngine{userPolicy: &parsedLifetimePolicy{
		defaultDuration: 15 * time.Minute,
		tiers:           []parsedTier{parsedGroupTier("admins", "admin", 8*time.Hour)},
	}}

	got := engine.evaluate(model.CertificateTypeUser, &Identity{Groups: []string{"guests"}}, "10.0.0.1", 24*time.Hour, 0)

	if !got.explanation.NoTierMatched {
		t.Error("expected no_tier_matched on the explanation")
	}
	if got.explanation.DefaultDuration != "15m0s" {
		t.Errorf("default duration = %q, want 15m0s", got.explanation.DefaultDuration)
	}
	if got.explanation.Tier != nil {
		t.Errorf("expected no tier, got %+v", got.explanation.Tier)
	}
}

func TestNewLifetimePolicyEngine(t *testing.T) {
	tests := []struct {
		name    string
		opts    config.CertificateOptions
		wantErr bool
		check   func(t *testing.T, engine *lifetimePolicyEngine)
	}{
		{
			name: "no policy configured",
			opts: config.CertificateOptions{},
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.userPolicy != nil || engine.servicePolicy != nil || engine.pamPolicy != nil {
					t.Error("expected every policy slot to stay nil")
				}
			},
		},
		{
			name: "valid user policy",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{
					LifetimePolicy: config.LifetimePolicy{DefaultDuration: 10 * time.Hour},
				},
			},
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.userPolicy == nil {
					t.Error("expected non-nil userPolicy")
				}
			},
		},
		{
			name: "invalid CIDR in user policy",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{
					LifetimePolicy: config.LifetimePolicy{
						DefaultDuration: time.Hour,
						SourcePolicy:    []config.SourcePolicyEntry{{CIDR: "invalid-cidr"}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid service policy",
			opts: config.CertificateOptions{
				Service: config.CertOptionsService{
					LifetimePolicy: config.LifetimePolicy{DefaultDuration: 10 * time.Hour},
				},
			},
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.servicePolicy == nil {
					t.Error("expected non-nil servicePolicy")
				}
			},
		},
		{
			// PAM was left out of the engine entirely until conditions
			// arrived; the gate is the axis operators actually want on it.
			name: "valid PAM policy",
			opts: config.CertificateOptions{
				PAM: config.CertOptionsPAM{
					LifetimePolicy: config.LifetimePolicy{DefaultDuration: 30 * time.Second},
				},
			},
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.pamPolicy == nil {
					t.Error("expected non-nil pamPolicy")
				}
			},
		},
		{
			name: "condition naming an undeclared claim",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{
					LifetimePolicy: config.LifetimePolicy{
						DefaultDuration: time.Hour,
						Tiers: []config.LifetimePolicyTier{{
							Name:        "cleared",
							When:        config.PolicyCondition{Claim: "undeclared", AtLeast: float64Ptr(40)},
							MaxDuration: time.Hour,
						}},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := newLifetimePolicyEngine(tt.opts, claimTiers)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error: got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if tt.check != nil {
				tt.check(t, engine)
			}
		})
	}
}

// The footgun this warns about is silent by construction: with source-network
// policy configured and no trusted_proxies, every request carries the
// reverse proxy's address, so *everyone* matches the proxy's tier and gets
// the most generous one. Nothing fails, nothing errors -- the only signal a
// deployment ever gets is this warning, so it has to actually fire.
//
// Not parallel: it swaps the default slog logger, which is process-global.
func TestValidateStartupConfig_ShouldWarnAboutSourcePolicyWithoutTrustedProxies(t *testing.T) {
	sourcePolicy := config.CertificateOptions{
		User: config.CertOptionsUser{
			LifetimePolicy: config.LifetimePolicy{
				DefaultDuration: time.Hour,
				SourcePolicy:    []config.SourcePolicyEntry{{CIDR: "10.0.0.0/8", MaxDuration: time.Hour}},
			},
		},
	}

	tests := []struct {
		name           string
		opts           config.CertificateOptions
		trustedProxies []string
		wantWarning    bool
	}{
		{name: "source policy with no trusted proxies", opts: sourcePolicy, trustedProxies: nil, wantWarning: true},
		{name: "source policy with trusted proxies", opts: sourcePolicy, trustedProxies: []string{"10.0.0.1"}, wantWarning: false},
		{name: "no source policy at all", opts: config.CertificateOptions{}, trustedProxies: nil, wantWarning: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := newLifetimePolicyEngine(tt.opts, claimTiers)
			if err != nil {
				t.Fatalf("newLifetimePolicyEngine() error = %v", err)
			}

			var buf bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prior) })

			engine.validateStartupConfig(tt.trustedProxies)

			warned := strings.Contains(buf.String(), "http.trusted_proxies")
			if warned != tt.wantWarning {
				t.Errorf("warned = %v, want %v (log was %q)", warned, tt.wantWarning, buf.String())
			}
		})
	}
}

// ValidateStartupConfig is the exported entry point bootstrap calls, and the
// only thing that supplies the engine with the *server's* trusted_proxies.
// Reading that from the wrong config field would leave the warning firing
// (or not) independently of how the deployment is actually set up.
func TestValidateStartupConfig_ShouldPassTheServersTrustedProxies(t *testing.T) {
	cfg := &config.Config{
		CertOptions: config.CertificateOptions{
			ClientTimeout: time.Minute,
			User: config.CertOptionsUser{
				LifetimePolicy: config.LifetimePolicy{
					DefaultDuration: time.Hour,
					SourcePolicy:    []config.SourcePolicyEntry{{CIDR: "10.0.0.0/8", MaxDuration: time.Hour}},
				},
			},
		},
	}
	cfg.HTTP.TrustedProxies = nil

	svc := newTestCertRequestServiceWithConfig(t, cfg)

	var buf bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prior) })

	svc.ValidateStartupConfig()

	if !strings.Contains(buf.String(), "http.trusted_proxies") {
		t.Errorf("expected the trusted_proxies warning with none configured, log was %q", buf.String())
	}
}
