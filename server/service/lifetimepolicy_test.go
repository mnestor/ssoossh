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

func TestParseLifetimePolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  config.LifetimePolicy
		wantErr bool
		check   func(t *testing.T, p *parsedLifetimePolicy)
	}{
		{
			name:    "empty config",
			policy:  config.LifetimePolicy{},
			wantErr: false,
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if p.defaultDuration != 0 {
					t.Errorf("expected zero defaultDuration, got %v", p.defaultDuration)
				}
				if len(p.tiers) != 0 {
					t.Errorf("expected zero tiers, got %d", len(p.tiers))
				}
				if len(p.sourceRules) != 0 {
					t.Errorf("expected zero sourceRules, got %d", len(p.sourceRules))
				}
			},
		},
		{
			name: "valid tiers",
			policy: config.LifetimePolicy{
				DefaultDuration: 10 * time.Hour,
				Tiers: []config.LifetimePolicyTier{
					{Group: "admin", MaxDuration: 24 * time.Hour},
					{Group: "users", MaxDuration: 8 * time.Hour},
				},
			},
			wantErr: false,
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if p.defaultDuration != 10*time.Hour {
					t.Errorf("expected 10h defaultDuration, got %v", p.defaultDuration)
				}
				if len(p.tiers) != 2 {
					t.Errorf("expected 2 tiers, got %d", len(p.tiers))
				}
				if p.tiers[0].group != "admin" || p.tiers[0].maxDuration != 24*time.Hour {
					t.Errorf("unexpected tier[0]: %+v", p.tiers[0])
				}
			},
		},
		{
			name: "valid IPv4 CIDR",
			policy: config.LifetimePolicy{
				SourcePolicy: []config.SourcePolicyEntry{
					{CIDR: "10.0.0.0/8", MaxDuration: 10 * time.Hour},
					{CIDR: "192.168.0.0/16", MaxDuration: 4 * time.Hour},
				},
			},
			wantErr: false,
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if len(p.sourceRules) != 2 {
					t.Errorf("expected 2 sourceRules, got %d", len(p.sourceRules))
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
				SourcePolicy: []config.SourcePolicyEntry{
					{CIDR: "2001:db8::/32", MaxDuration: 4 * time.Hour},
				},
			},
			wantErr: false,
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if len(p.sourceRules) != 1 {
					t.Errorf("expected 1 sourceRule, got %d", len(p.sourceRules))
				}
				if p.sourceRules[0].prefixLen != 32 {
					t.Errorf("expected prefixLen 32, got %d", p.sourceRules[0].prefixLen)
				}
			},
		},
		{
			name: "invalid CIDR",
			policy: config.LifetimePolicy{
				SourcePolicy: []config.SourcePolicyEntry{
					{CIDR: "not-a-cidr", MaxDuration: 10 * time.Hour},
				},
			},
			wantErr: true,
		},
		{
			name: "source rule with extensions",
			policy: config.LifetimePolicy{
				SourcePolicy: []config.SourcePolicyEntry{
					{
						CIDR:        "10.0.0.0/8",
						MaxDuration: 10 * time.Hour,
						Extensions:  []string{"permit-pty", "permit-port-forwarding"},
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if len(p.sourceRules) != 1 {
					t.Errorf("expected 1 sourceRule, got %d", len(p.sourceRules))
				}
				if len(p.sourceRules[0].extensions) != 2 {
					t.Errorf("expected 2 extensions, got %d", len(p.sourceRules[0].extensions))
				}
			},
		},
		{
			name: "source rule with pin_source_address",
			policy: config.LifetimePolicy{
				SourcePolicy: []config.SourcePolicyEntry{
					{
						CIDR:             "10.0.0.0/8",
						MaxDuration:      10 * time.Hour,
						PinSourceAddress: true,
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, p *parsedLifetimePolicy) {
				if !p.sourceRules[0].pinSourceAddress {
					t.Errorf("expected pinSourceAddress=true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parseLifetimePolicy(tt.policy)
			if (err != nil) != tt.wantErr {
				t.Errorf("unexpected error: got %v, wantErr %v", err, tt.wantErr)
				return
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

func TestEvaluateTier(t *testing.T) {
	tests := []struct {
		name     string
		policy   *parsedLifetimePolicy
		groups   []string
		expected time.Duration
	}{
		{
			name:     "no tiers configured",
			policy:   &parsedLifetimePolicy{defaultDuration: 10 * time.Hour},
			groups:   []string{"anyone"},
			expected: 10 * time.Hour,
		},
		{
			name: "first tier matches",
			policy: &parsedLifetimePolicy{
				defaultDuration: 1 * time.Hour,
				tiers: []parsedTier{
					{group: "admin", maxDuration: 24 * time.Hour},
					{group: "users", maxDuration: 8 * time.Hour},
				},
			},
			groups:   []string{"admin", "users"},
			expected: 24 * time.Hour,
		},
		{
			name: "second tier matches",
			policy: &parsedLifetimePolicy{
				defaultDuration: 1 * time.Hour,
				tiers: []parsedTier{
					{group: "admin", maxDuration: 24 * time.Hour},
					{group: "users", maxDuration: 8 * time.Hour},
				},
			},
			groups:   []string{"users"},
			expected: 8 * time.Hour,
		},
		{
			name: "no tier matches, use default",
			policy: &parsedLifetimePolicy{
				defaultDuration: 1 * time.Hour,
				tiers: []parsedTier{
					{group: "admin", maxDuration: 24 * time.Hour},
					{group: "users", maxDuration: 8 * time.Hour},
				},
			},
			groups:   []string{"guests"},
			expected: 1 * time.Hour,
		},
		{
			name: "empty groups",
			policy: &parsedLifetimePolicy{
				defaultDuration: 2 * time.Hour,
				tiers: []parsedTier{
					{group: "admin", maxDuration: 24 * time.Hour},
				},
			},
			groups:   []string{},
			expected: 2 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &lifetimePolicyEngine{userPolicy: tt.policy}
			got := engine.evaluateTier(tt.policy, tt.groups)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestEvaluateSourceRule(t *testing.T) {
	tests := []struct {
		name           string
		policy         *parsedLifetimePolicy
		sourceIP       string
		expectedDur    time.Duration
		expectedReason string
	}{
		{
			name:           "no source rules configured",
			policy:         &parsedLifetimePolicy{},
			sourceIP:       "10.0.0.1",
			expectedDur:    0,
			expectedReason: "",
		},
		{
			name: "direct IPv4 match",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/8"),
						maxDuration: 10 * time.Hour,
						prefixLen:   8,
					},
				},
			},
			sourceIP:    "10.1.2.3",
			expectedDur: 10 * time.Hour,
		},
		{
			name: "IPv4-mapped IPv6 normalized to IPv4",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/8"),
						maxDuration: 10 * time.Hour,
						prefixLen:   8,
					},
				},
			},
			sourceIP:    "::ffff:10.1.2.3",
			expectedDur: 10 * time.Hour,
		},
		{
			name: "IPv6 match",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("2001:db8::/32"),
						maxDuration: 4 * time.Hour,
						prefixLen:   32,
					},
				},
			},
			sourceIP:    "2001:db8::1",
			expectedDur: 4 * time.Hour,
		},
		{
			name: "longest prefix wins",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/8"),
						maxDuration: 10 * time.Hour,
						prefixLen:   8,
					},
					{
						prefix:      netip.MustParsePrefix("10.1.0.0/16"),
						maxDuration: 5 * time.Hour,
						prefixLen:   16,
					},
				},
			},
			sourceIP:    "10.1.2.3",
			expectedDur: 5 * time.Hour,
		},
		{
			name: "tie goes to stricter (shorter) duration",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/24"),
						maxDuration: 10 * time.Hour,
						prefixLen:   24,
					},
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/24"),
						maxDuration: 5 * time.Hour,
						prefixLen:   24,
					},
				},
			},
			sourceIP:    "10.0.0.5",
			expectedDur: 5 * time.Hour,
		},
		{
			name: "no match returns zero",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/8"),
						maxDuration: 10 * time.Hour,
						prefixLen:   8,
					},
				},
			},
			sourceIP:    "192.168.1.1",
			expectedDur: 0,
		},
		{
			name: "invalid source IP returns zero",
			policy: &parsedLifetimePolicy{
				sourceRules: []parsedSourceRule{
					{
						prefix:      netip.MustParsePrefix("10.0.0.0/8"),
						maxDuration: 10 * time.Hour,
						prefixLen:   8,
					},
				},
			},
			sourceIP:    "invalid-ip",
			expectedDur: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &lifetimePolicyEngine{userPolicy: tt.policy}
			got, _ := engine.evaluateSourceRule(tt.policy, tt.sourceIP)
			if got != tt.expectedDur {
				t.Errorf("expected %v, got %v", tt.expectedDur, got)
			}
		})
	}
}

func TestEvaluateDuration(t *testing.T) {
	tests := []struct {
		name     string
		engine   *lifetimePolicyEngine
		certType model.CertificateType
		identity *Identity
		sourceIP string
		ceiling  time.Duration
		expected time.Duration
	}{
		{
			name: "no policy configured, use ceiling",
			engine: &lifetimePolicyEngine{
				userPolicy:    nil,
				servicePolicy: nil,
			},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"users"}},
			sourceIP: "10.0.0.1",
			ceiling:  10 * time.Hour,
			expected: 10 * time.Hour,
		},
		{
			name: "tier policy only, no source rules",
			engine: &lifetimePolicyEngine{
				userPolicy: &parsedLifetimePolicy{
					defaultDuration: 1 * time.Hour,
					tiers: []parsedTier{
						{group: "admin", maxDuration: 24 * time.Hour},
					},
				},
			},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"admin"}},
			sourceIP: "10.0.0.1",
			ceiling:  10 * time.Hour,
			expected: 10 * time.Hour, // clamped to ceiling
		},
		{
			name: "tier policy only, tier doesn't match",
			engine: &lifetimePolicyEngine{
				userPolicy: &parsedLifetimePolicy{
					defaultDuration: 1 * time.Hour,
					tiers: []parsedTier{
						{group: "admin", maxDuration: 24 * time.Hour},
					},
				},
			},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"users"}},
			sourceIP: "10.0.0.1",
			ceiling:  10 * time.Hour,
			expected: 1 * time.Hour, // uses defaultDuration
		},
		{
			name: "source rule restricts tier",
			engine: &lifetimePolicyEngine{
				userPolicy: &parsedLifetimePolicy{
					defaultDuration: 1 * time.Hour,
					tiers: []parsedTier{
						{group: "admin", maxDuration: 24 * time.Hour},
					},
					sourceRules: []parsedSourceRule{
						{
							prefix:      netip.MustParsePrefix("10.0.0.0/8"),
							maxDuration: 2 * time.Hour,
							prefixLen:   8,
						},
					},
				},
			},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"admin"}},
			sourceIP: "10.1.2.3",
			ceiling:  24 * time.Hour,
			expected: 2 * time.Hour, // min(tier 24h, source 2h, ceiling 24h)
		},
		{
			name: "ceiling clamps result",
			engine: &lifetimePolicyEngine{
				userPolicy: &parsedLifetimePolicy{
					defaultDuration: 1 * time.Hour,
					tiers: []parsedTier{
						{group: "admin", maxDuration: 24 * time.Hour},
					},
				},
			},
			certType: model.CertificateTypeUser,
			identity: &Identity{Groups: []string{"admin"}},
			sourceIP: "10.0.0.1",
			ceiling:  5 * time.Hour,
			expected: 5 * time.Hour, // clamped to ceiling
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := tt.engine.evaluateDuration(tt.certType, tt.identity, tt.sourceIP, tt.ceiling)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestNarrowRequestedOptionsWithPolicy(t *testing.T) {
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
			engine:   &lifetimePolicyEngine{userPolicy: nil},
			certType: model.CertificateTypeUser,
			identity: &Identity{},
			sourceIP: "10.0.0.1",
			input: RequestedOptions{
				Extensions: []string{"permit-pty", "permit-port-forwarding"},
			},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.Extensions) != 2 {
					t.Errorf("expected 2 extensions, got %d", len(result.Extensions))
				}
			},
		},
		{
			name: "user cert with extension restriction",
			engine: &lifetimePolicyEngine{
				userPolicy: &parsedLifetimePolicy{
					sourceRules: []parsedSourceRule{
						{
							prefix:      netip.MustParsePrefix("0.0.0.0/0"),
							maxDuration: 15 * time.Minute,
							extensions:  []string{"permit-pty"},
							prefixLen:   0,
						},
					},
				},
			},
			certType: model.CertificateTypeUser,
			identity: &Identity{},
			sourceIP: "192.168.1.1",
			input: RequestedOptions{
				Extensions: []string{"permit-pty", "permit-port-forwarding"},
			},
			check: func(t *testing.T, result RequestedOptions) {
				if len(result.Extensions) != 1 || result.Extensions[0] != "permit-pty" {
					t.Errorf("expected [permit-pty], got %v", result.Extensions)
				}
			},
		},
		{
			name: "service cert with source address pinning",
			engine: &lifetimePolicyEngine{
				servicePolicy: &parsedLifetimePolicy{
					sourceRules: []parsedSourceRule{
						{
							prefix:           netip.MustParsePrefix("0.0.0.0/0"),
							maxDuration:      15 * time.Minute,
							pinSourceAddress: true,
							prefixLen:        0,
						},
					},
				},
			},
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
			engine: &lifetimePolicyEngine{
				userPolicy: &parsedLifetimePolicy{
					sourceRules: []parsedSourceRule{
						{
							prefix:           netip.MustParsePrefix("0.0.0.0/0"),
							maxDuration:      15 * time.Minute,
							pinSourceAddress: true,
							prefixLen:        0,
						},
					},
				},
			},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.engine.narrowRequestedOptionsWithPolicy(tt.certType, tt.identity, tt.sourceIP, tt.input)
			if tt.check != nil {
				tt.check(t, result)
			}
		})
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
			name:    "no policy configured",
			opts:    config.CertificateOptions{},
			wantErr: false,
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.userPolicy != nil {
					t.Errorf("expected nil userPolicy")
				}
				if engine.servicePolicy != nil {
					t.Errorf("expected nil servicePolicy")
				}
			},
		},
		{
			name: "valid user policy",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{
					LifetimePolicy: config.LifetimePolicy{
						DefaultDuration: 10 * time.Hour,
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.userPolicy == nil {
					t.Errorf("expected non-nil userPolicy")
				}
			},
		},
		{
			name: "invalid CIDR in user policy",
			opts: config.CertificateOptions{
				User: config.CertOptionsUser{
					LifetimePolicy: config.LifetimePolicy{
						SourcePolicy: []config.SourcePolicyEntry{
							{CIDR: "invalid-cidr"},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid service policy",
			opts: config.CertificateOptions{
				Service: config.CertOptionsService{
					LifetimePolicy: config.LifetimePolicy{
						DefaultDuration: 10 * time.Hour,
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, engine *lifetimePolicyEngine) {
				if engine.servicePolicy == nil {
					t.Errorf("expected non-nil servicePolicy")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := newLifetimePolicyEngine(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("unexpected error: got %v, wantErr %v", err, tt.wantErr)
				return
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
				SourcePolicy: []config.SourcePolicyEntry{
					{CIDR: "10.0.0.0/8", MaxDuration: time.Hour},
				},
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
			engine, err := newLifetimePolicyEngine(tt.opts)
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
					SourcePolicy: []config.SourcePolicyEntry{
						{CIDR: "10.0.0.0/8", MaxDuration: time.Hour},
					},
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
