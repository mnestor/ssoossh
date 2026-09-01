package config

// Test methodology: table-driven unit tests over the PolicyCondition
// helpers. String feeds the effective-config view and policy error
// messages, and ReferencedClaims feeds startup validation of claim names —
// a wrong rendering misleads an operator, and a missed claim reference
// turns a typo into a condition that silently fails on every evaluation.

import (
	"reflect"
	"testing"
)

// f is a shorthand for the *float64 bounds the grammar uses.
func f(v float64) *float64 { return &v }

// s is a shorthand for the *string comparator.
func s(v string) *string { return &v }

func TestPolicyCondition_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cond *PolicyCondition
		want bool
	}{
		{name: "a nil condition", cond: nil, want: true},
		{name: "an empty condition", cond: &PolicyCondition{}, want: true},
		{name: "a group", cond: &PolicyCondition{Group: "admins"}, want: false},
		{name: "a bare claim name", cond: &PolicyCondition{Claim: "loc"}, want: false},
		{name: "a lower bound alone", cond: &PolicyCondition{AtLeast: f(1)}, want: false},
		{name: "an upper bound alone", cond: &PolicyCondition{AtMost: f(1)}, want: false},
		{name: "an exact bound alone", cond: &PolicyCondition{Exactly: f(1)}, want: false},
		{name: "a string equality alone", cond: &PolicyCondition{Equals: s("x")}, want: false},
		{name: "a one_of set alone", cond: &PolicyCondition{OneOf: []string{"x"}}, want: false},
		{name: "a contains value alone", cond: &PolicyCondition{Contains: "x"}, want: false},
		{name: "an all_of list alone", cond: &PolicyCondition{AllOf: []PolicyCondition{{Group: "g"}}}, want: false},
		{name: "an any_of list alone", cond: &PolicyCondition{AnyOf: []PolicyCondition{{Group: "g"}}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.cond.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPolicyCondition_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cond PolicyCondition
		want string
	}{
		{
			name: "a group membership",
			cond: PolicyCondition{Group: "SSH Users"},
			want: `group "SSH Users"`,
		},
		{
			// Exactly renders as equality even though it desugars to a
			// range internally; the operator wrote ==, so they read ==.
			name: "an exact numeric claim",
			cond: PolicyCondition{Claim: "level", Exactly: f(40)},
			want: "claim level == 40",
		},
		{
			name: "a numeric range",
			cond: PolicyCondition{Claim: "loc", AtLeast: f(20), AtMost: f(30)},
			want: "claim loc in [20, 30]",
		},
		{
			name: "a lower bound alone",
			cond: PolicyCondition{Claim: "loc", AtLeast: f(20)},
			want: "claim loc >= 20",
		},
		{
			name: "an upper bound alone",
			cond: PolicyCondition{Claim: "loc", AtMost: f(30)},
			want: "claim loc <= 30",
		},
		{
			// Bounds format like claim values: integral values carry no
			// trailing ".0", fractional values keep their digits.
			name: "a fractional bound keeps its digits",
			cond: PolicyCondition{Claim: "score", AtLeast: f(39.5)},
			want: "claim score >= 39.5",
		},
		{
			name: "a string equality",
			cond: PolicyCondition{Claim: "region", Equals: s("eu-west")},
			want: `claim region equals "eu-west"`,
		},
		{
			name: "a one_of set",
			cond: PolicyCondition{Claim: "region", OneOf: []string{"eu-west", "eu-north"}},
			want: "claim region one_of [eu-west, eu-north]",
		},
		{
			name: "a list membership",
			cond: PolicyCondition{Claim: "entitlements", Contains: "ssh"},
			want: `claim entitlements contains "ssh"`,
		},
		{
			name: "a conjunction over nested conditions",
			cond: PolicyCondition{AllOf: []PolicyCondition{
				{Group: "SSH Users"},
				{Claim: "loc", AtLeast: f(20)},
			}},
			want: `all_of(group "SSH Users", claim loc >= 20)`,
		},
		{
			name: "a disjunction over nested conditions",
			cond: PolicyCondition{AnyOf: []PolicyCondition{
				{Group: "admins"},
				{Claim: "region", Equals: s("hq")},
			}},
			want: `any_of(group "admins", claim region equals "hq")`,
		},
		{
			// The effective-config view prints conditions unconditionally;
			// an unset gate must render as nothing, not as noise.
			name: "an empty condition renders empty",
			cond: PolicyCondition{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.cond.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPolicyCondition_ReferencedClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cond *PolicyCondition
		want []string
	}{
		{
			name: "a nil condition references nothing",
			cond: nil,
			want: nil,
		},
		{
			name: "a group condition references nothing",
			cond: &PolicyCondition{Group: "admins"},
			want: nil,
		},
		{
			name: "a claim condition references its claim",
			cond: &PolicyCondition{Claim: "loc", AtLeast: f(20)},
			want: []string{"loc"},
		},
		{
			// Startup validation must see every nested claim, or a typo
			// inside all_of would slip past the check the field exists for.
			name: "nested conditions contribute their claims",
			cond: &PolicyCondition{AllOf: []PolicyCondition{
				{Claim: "loc", AtLeast: f(20)},
				{Group: "admins"},
				{Claim: "region", Equals: s("hq")},
			}},
			want: []string{"loc", "region"},
		},
		{
			name: "any_of branches contribute their claims",
			cond: &PolicyCondition{AnyOf: []PolicyCondition{
				{Claim: "level", Exactly: f(3)},
				{Claim: "level", Exactly: f(4)},
			}},
			want: []string{"level", "level"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.cond.ReferencedClaims(nil)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReferencedClaims(nil) = %v, want %v", got, tt.want)
			}
		})
	}
}

// ReferencedClaims appends rather than replaces, because startup validation
// accumulates the names across every tier and certificate type in one pass.
func TestPolicyCondition_ReferencedClaimsShouldAppendToExisting(t *testing.T) {
	t.Parallel()

	cond := &PolicyCondition{Claim: "loc"}
	got := cond.ReferencedClaims([]string{"existing"})
	want := []string{"existing", "loc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReferencedClaims(existing) = %v, want %v", got, want)
	}
}
