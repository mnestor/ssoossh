package service

import (
	"slices"
	"strings"
	"testing"
)

// TestClaimSuggestions_ShouldSuggestConfigForUnmappedClaims is the reason
// the echo is a tool rather than a curiosity.
func TestClaimSuggestions_ShouldSuggestConfigForUnmappedClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		claims     map[string]any
		mapping    ClaimMapping
		wantClaim  string
		wantYAML   string
		wantAbsent string
	}{
		{
			name:      "should suggest a string claim nothing reads",
			claims:    map[string]any{"employee_type": "staff"},
			mapping:   ClaimMapping{Username: "preferred_username"},
			wantClaim: "employee_type",
			wantYAML:  "employee_type: employee_type",
		},
		{
			name:      "should keep a namespaced claim's last segment",
			claims:    map[string]any{"https://idp.example.com/department": "platform"},
			wantClaim: "https://idp.example.com/department",
			wantYAML:  `department: "https://idp.example.com/department"`,
		},
		{
			name:       "should not suggest a claim the configuration already reads",
			claims:     map[string]any{"preferred_username": "alice"},
			mapping:    ClaimMapping{Username: "preferred_username"},
			wantAbsent: "preferred_username",
		},
		{
			name:       "should not suggest an extra field's claim again",
			claims:     map[string]any{"employee_type": "staff"},
			mapping:    ClaimMapping{Extra: map[string]string{"emp": "employee_type"}},
			wantAbsent: "employee_type",
		},
		{
			name:       "should not suggest protocol claims",
			claims:     map[string]any{"iss": "https://idp.example.com", "exp": float64(1), "sub": "sub-1"},
			wantAbsent: "iss",
		},
		{
			name:       "should not suggest an object, which no extra field can read",
			claims:     map[string]any{"realm_access": map[string]any{"roles": []any{"a"}}},
			wantAbsent: "realm_access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := claimSuggestions(tt.claims, tt.mapping)

			if tt.wantAbsent != "" {
				for _, s := range got {
					if s.Claim == tt.wantAbsent {
						t.Fatalf("claim %q was suggested and should not have been", tt.wantAbsent)
					}
				}
				return
			}

			var found bool
			for _, s := range got {
				if s.Claim != tt.wantClaim {
					continue
				}
				found = true
				if !strings.Contains(s.YAML, tt.wantYAML) || s.Field != SuggestFieldExtra {
					t.Errorf("suggestion = %+v, want an extra field with yaml containing %q", s, tt.wantYAML)
				}
			}
			if !found {
				t.Errorf("claim %q was not suggested; got %+v", tt.wantClaim, got)
			}
		})
	}
}

// TestClaimSuggestionReason_ShouldNameTheValueShape matters because the
// shape decides whether a claim can gate a policy condition or only decorate
// a key ID.
func TestClaimSuggestionReason_ShouldNameTheValueShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "should say a number can gate a policy", value: float64(42), want: "numeric"},
		{name: "should say a list is captured as a list", value: []any{"a", "b"}, want: "list"},
		{name: "should call a string simply unmapped", value: "staff", want: "unmapped"},
		{name: "should say how a boolean is captured", value: true, want: "boolean"},
		{name: "should say anything else stores empty", value: nil, want: "not a scalar or list"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := claimSuggestionReason(tt.value); !strings.Contains(got, tt.want) {
				t.Errorf("reason = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

// TestSuggestedExtraName_ShouldReadAsATemplateField covers the conversion
// that makes a suggestion reachable as {{.Extra.<name>}}.
func TestSuggestedExtraName_ShouldReadAsATemplateField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		claim string
		want  string
	}{
		{claim: "employee_type", want: "employee_type"},
		{claim: "employeeType", want: "employeetype"},
		{claim: "https://idp.example.com/department", want: "department"},
		{claim: "urn:example:cost-center", want: "cost_center"},
	}

	for _, tt := range tests {
		t.Run(tt.claim, func(t *testing.T) {
			t.Parallel()

			if got := suggestedExtraName(tt.claim); got != tt.want {
				t.Errorf("suggestedExtraName(%q) = %q, want %q", tt.claim, got, tt.want)
			}
		})
	}
}

// echoField finds one field's resolution in a report.
func echoField(t *testing.T, report *EchoReport, field string) EchoField {
	t.Helper()
	for _, f := range report.Fields {
		if f.Field == field {
			return f
		}
	}
	t.Fatalf("no resolution for field %q in %+v", field, report.Fields)
	return EchoField{}
}

func TestBuildEchoReport_ShouldResolveEachFieldAsALoginReadsIt(t *testing.T) {
	t.Parallel()

	claims := map[string]any{
		"sub":                "sub-alice",
		"preferred_username": "",
		"name":               float64(7),
		"groups":             "not-a-list",
		"email":              "alice@example.com",
		"dept":               []any{"platform", float64(1)},
	}
	mapping := ClaimMapping{
		Subject: "sub", Username: "preferred_username", Name: "name", Groups: "groups",
		OtherAccounts: "alt", Email: "email",
		Extra: map[string]string{"dept": "dept", "cost": "cost_center"},
	}
	report := BuildEchoReport(&EchoTokens{Claims: claims}, mapping)

	tests := []struct {
		field      string
		wantStatus EchoFieldStatus
		wantValue  string
		wantValues []string
	}{
		{field: "subject", wantStatus: EchoFieldResolved, wantValue: "sub-alice"},
		{field: "username", wantStatus: EchoFieldEmpty},
		{field: "name", wantStatus: EchoFieldResolved, wantValue: "7"},
		{field: "groups", wantStatus: EchoFieldWrongShape},
		{field: "other_accounts", wantStatus: EchoFieldMissing},
		{field: "service_accounts", wantStatus: EchoFieldUnconfigured},
		{field: "email", wantStatus: EchoFieldResolved, wantValue: "alice@example.com"},
		{field: "extra.cost", wantStatus: EchoFieldMissing},
		{field: "extra.dept", wantStatus: EchoFieldResolved, wantValues: []string{"platform"}},
	}
	for _, tt := range tests {
		t.Run("should resolve "+tt.field, func(t *testing.T) {
			t.Parallel()

			got := echoField(t, report, tt.field)
			if got.Status != tt.wantStatus || got.Value != tt.wantValue || !slices.Equal(got.Values, tt.wantValues) {
				t.Errorf("got %+v, want status %q value %q values %v", got, tt.wantStatus, tt.wantValue, tt.wantValues)
			}
		})
	}
}

func TestBuildEchoReport_ShouldGiveTheLoginsOwnVerdict(t *testing.T) {
	t.Parallel()

	mapping := ClaimMapping{Subject: "sub", Username: "preferred_username"}
	tests := []struct {
		name        string
		claims      map[string]any
		wantAllowed bool
		wantReason  string
	}{
		{name: "should allow a token with a subject and a username", claims: map[string]any{"sub": "s", "preferred_username": "alice"}, wantAllowed: true},
		{name: "should name the missing username claim", claims: map[string]any{"sub": "s"}, wantReason: `username claim "preferred_username"`},
		{name: "should name the missing subject claim", claims: map[string]any{"preferred_username": "alice"}, wantReason: `subject claim "sub"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildEchoReport(&EchoTokens{Claims: tt.claims}, mapping).SignIn
			_, loginErr := identityFromClaims(tt.claims, mapping)
			if got.Allowed != tt.wantAllowed || !strings.Contains(got.Reason, tt.wantReason) || got.Allowed != (loginErr == nil) {
				t.Errorf("got %+v (login error %v), want allowed=%t reason containing %q", got, loginErr, tt.wantAllowed, tt.wantReason)
			}
		})
	}
}

// suggestionFor finds the suggestion of field for claim.
func suggestionFor(report *EchoReport, field, claim string) (ClaimSuggestion, bool) {
	for _, s := range report.Suggestions {
		if s.Field == field && s.Claim == claim {
			return s, true
		}
	}
	return ClaimSuggestion{}, false
}

func TestBuildEchoReport_ShouldProposeFieldCandidates(t *testing.T) {
	t.Parallel()

	claims := map[string]any{
		"sub": "per-app", "oid": "tenant-stable", "upn": "alice@example.com",
		"preferred_username": "alice", "roles": []any{"ssh-admins"}, "aud": []any{"client-1"},
		"wids": []any{},
	}
	report := BuildEchoReport(&EchoTokens{Claims: claims}, ClaimMapping{Subject: "sub", Username: "preferred_username"})

	tests := []struct {
		name  string
		field string
		claim string
		want  bool
	}{
		{name: "should offer oid for the subject", field: SuggestFieldSubject, claim: "oid", want: true},
		{name: "should not offer the subject already configured", field: SuggestFieldSubject, claim: "sub", want: false},
		{name: "should offer upn for the username", field: SuggestFieldUsername, claim: "upn", want: true},
		{name: "should offer a string list for groups", field: SuggestFieldGroups, claim: "roles", want: true},
		{name: "should not offer the audience for groups", field: SuggestFieldGroups, claim: "aud", want: false},
		{name: "should not offer an empty list for groups", field: SuggestFieldGroups, claim: "wids", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, found := suggestionFor(report, tt.field, tt.claim)
			if found != tt.want {
				t.Fatalf("found = %t, want %t; suggestions %+v", found, tt.want, report.Suggestions)
			}
			if found && !strings.Contains(s.YAML, tt.field+": "+tt.claim) {
				t.Errorf("yaml = %q, want it to set %s", s.YAML, tt.field)
			}
		})
	}
}

func TestBuildEchoReport_ShouldOfferTheGroupsAsAdminGroupCandidates(t *testing.T) {
	t.Parallel()

	report := BuildEchoReport(&EchoTokens{Claims: map[string]any{"groups": []any{"b", "a", "b"}}}, ClaimMapping{Groups: "groups"})

	if !slices.Equal(report.AdminGroupCandidates, []string{"b", "a"}) {
		t.Errorf("candidates = %v, want each group once, in claim order", report.AdminGroupCandidates)
	}
}

func TestBuildEchoReport_ShouldFlagNestedClaimsAsNotMappable(t *testing.T) {
	t.Parallel()

	tokens := &EchoTokens{
		Claims: map[string]any{
			"realm_access": map[string]any{"roles": []any{"admin"}},
			"entitlements": []any{map[string]any{"id": 1}},
			"groups":       []any{"a"},
		},
		AccessTokenClaims: map[string]any{"groups": []any{"from-access"}},
	}
	report := BuildEchoReport(tokens, ClaimMapping{})

	tests := []struct {
		name   string
		claim  string
		source string
		want   bool
	}{
		{name: "should flag an object", claim: "realm_access", source: UnmappableSourceIDToken, want: true},
		{name: "should flag a list of objects", claim: "entitlements", source: UnmappableSourceIDToken, want: true},
		{name: "should not flag a string list", claim: "groups", source: UnmappableSourceIDToken, want: false},
		{name: "should list the access token's claims", claim: "groups", source: UnmappableSourceAccessToken, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found := slices.ContainsFunc(report.NotMappable, func(u UnmappableClaim) bool {
				return u.Claim == tt.claim && u.Source == tt.source
			})
			if found != tt.want {
				t.Errorf("found = %t, want %t; not mappable %+v", found, tt.want, report.NotMappable)
			}
		})
	}
}

func TestBuildEchoReport_ShouldGroupUnreadProtocolClaimsApart(t *testing.T) {
	t.Parallel()

	report := BuildEchoReport(&EchoTokens{Claims: map[string]any{"iss": "x", "sub": "s", "nonce": "n", "email": "e"}}, ClaimMapping{Subject: "sub"})

	if !slices.Equal(report.ProtocolClaims, []string{"iss", "nonce"}) {
		t.Errorf("protocol claims = %v, want the unread ones only, sorted", report.ProtocolClaims)
	}
}

func TestUnverifiedJWTClaims_ShouldDecodeOnlyAJWT(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  any
	}{
		{name: "should decode a JWT payload", token: "eyJhbGciOiJub25lIn0.eyJncm91cHMiOlsiYSJdfQ.sig", want: "a"},
		{name: "should ignore an opaque token", token: "opaque-access-token", want: nil},
		{name: "should ignore a payload that is not base64url", token: "a.!!!.c", want: nil},
		{name: "should ignore a payload that is not JSON", token: "a.bm90IGpzb24.c", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			claims := unverifiedJWTClaims(tt.token)
			var got any
			if groups, ok := claims["groups"].([]any); ok && len(groups) > 0 {
				got = groups[0]
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
