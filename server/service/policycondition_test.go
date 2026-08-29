package service

// Test methodology: table-driven unit tests for the condition grammar's two
// halves — parsing (which is where the grammar is closed and every startup
// error lives) and evaluation (where the fail-closed posture on an absent
// claim has to hold on every path).

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
)

// declared is the authentication.fields.extra map conditions parse against.
var declared = map[string]string{
	"loc":      "level_of_confidence",
	"dept":     "departmentNumber",
	"projects": "projectList",
}

func stringPtr(s string) *string { return &s }

func TestParseCondition_ShouldAcceptEveryFormInTheGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cond config.PolicyCondition
		want string
	}{
		{
			name: "group membership",
			cond: config.PolicyCondition{Group: "SSH Users"},
			want: `group "SSH Users"`,
		},
		{
			name: "at_least",
			cond: config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(40)},
			want: "claim loc >= 40",
		},
		{
			name: "at_most",
			cond: config.PolicyCondition{Claim: "loc", AtMost: float64Ptr(10)},
			want: "claim loc <= 10",
		},
		{
			name: "at_least and at_most form a range",
			cond: config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(20), AtMost: float64Ptr(40)},
			want: "claim loc in [20, 40]",
		},
		{
			name: "exactly",
			cond: config.PolicyCondition{Claim: "loc", Exactly: float64Ptr(25)},
			want: "claim loc == 25",
		},
		{
			name: "equals",
			cond: config.PolicyCondition{Claim: "dept", Equals: stringPtr("eng")},
			want: `claim dept equals "eng"`,
		},
		{
			name: "one_of",
			cond: config.PolicyCondition{Claim: "dept", OneOf: []string{"eng", "ops"}},
			want: "claim dept one_of [eng, ops]",
		},
		{
			name: "contains",
			cond: config.PolicyCondition{Claim: "projects", Contains: "apollo"},
			want: `claim projects contains "apollo"`,
		},
		{
			name: "all_of",
			cond: config.PolicyCondition{AllOf: []config.PolicyCondition{
				{Group: "SSH Users"},
				{Claim: "loc", AtLeast: float64Ptr(20)},
			}},
			want: `all_of(group "SSH Users", claim loc >= 20)`,
		},
		{
			name: "any_of",
			cond: config.PolicyCondition{AnyOf: []config.PolicyCondition{
				{Group: "admin"},
				{Claim: "loc", AtLeast: float64Ptr(40)},
			}},
			want: `any_of(group "admin", claim loc >= 40)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseCondition(&tt.cond, declared, false)
			if err != nil {
				t.Fatalf("parseCondition() error = %v", err)
			}
			if parsed.String() != tt.want {
				t.Errorf("rendered %q, want %q", parsed.String(), tt.want)
			}
		})
	}
}

// The grammar is closed on purpose: every rejection below is a config
// mistake that would otherwise surface as a condition quietly failing (or
// quietly passing) on every evaluation.
func TestParseCondition_ShouldRejectMalformedConditions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cond     config.PolicyCondition
		wantText string
	}{
		{
			name:     "empty condition",
			cond:     config.PolicyCondition{},
			wantText: "empty",
		},
		{
			name:     "group and claim together",
			cond:     config.PolicyCondition{Group: "admin", Claim: "loc", AtLeast: float64Ptr(40)},
			wantText: "exactly one of",
		},
		{
			name:     "comparator with no claim",
			cond:     config.PolicyCondition{Group: "admin", AtLeast: float64Ptr(40)},
			wantText: "comparator but no claim",
		},
		{
			name:     "undeclared claim",
			cond:     config.PolicyCondition{Claim: "nope", AtLeast: float64Ptr(40)},
			wantText: "not declared",
		},
		{
			name:     "claim with no comparator",
			cond:     config.PolicyCondition{Claim: "loc"},
			wantText: "exactly one comparator",
		},
		{
			name:     "two comparator families",
			cond:     config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(40), Equals: stringPtr("high")},
			wantText: "exactly one comparator",
		},
		{
			name:     "empty equals can never match",
			cond:     config.PolicyCondition{Claim: "dept", Equals: stringPtr("")},
			wantText: "empty equals",
		},
		{
			name: "nesting past one level",
			cond: config.PolicyCondition{AllOf: []config.PolicyCondition{
				{AnyOf: []config.PolicyCondition{{Group: "admin"}}},
			}},
			wantText: "one level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseCondition(&tt.cond, declared, false)
			if err == nil {
				t.Fatal("parseCondition() error = nil, want a rejection")
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error %q should mention %q", err, tt.wantText)
			}
		})
	}
}

// exactly is shorthand for an inclusive [n, n] range rather than a second
// comparison path, so there is only ever one numeric comparator to reason
// about.
func TestParseCondition_ShouldDesugarExactlyToAnInclusiveRange(t *testing.T) {
	t.Parallel()

	parsed, err := parseCondition(&config.PolicyCondition{Claim: "loc", Exactly: float64Ptr(25)}, declared, false)
	if err != nil {
		t.Fatalf("parseCondition() error = %v", err)
	}
	if parsed.atLeast == nil || parsed.atMost == nil {
		t.Fatal("expected exactly to desugar into both bounds")
	}
	if *parsed.atLeast != 25 || *parsed.atMost != 25 {
		t.Errorf("bounds = [%v, %v], want [25, 25]", *parsed.atLeast, *parsed.atMost)
	}
}

func TestCondition_Evaluate(t *testing.T) {
	t.Parallel()

	identity := &Identity{
		Subject: "sub-alice",
		Groups:  []string{"SSH Users", "contractors"},
		Extra: map[string]extraValue{
			"loc":      scalarExtra("40"),
			"dept":     scalarExtra("eng"),
			"projects": listExtra([]string{"apollo", "gemini"}),
		},
	}

	tests := []struct {
		name string
		cond config.PolicyCondition
		want bool
	}{
		{name: "group present", cond: config.PolicyCondition{Group: "SSH Users"}, want: true},
		{name: "group absent", cond: config.PolicyCondition{Group: "admin"}, want: false},
		{name: "at_least is inclusive at the bound", cond: config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(40)}, want: true},
		{name: "at_least above the value", cond: config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(41)}, want: false},
		{name: "at_most is inclusive at the bound", cond: config.PolicyCondition{Claim: "loc", AtMost: float64Ptr(40)}, want: true},
		{name: "at_most below the value", cond: config.PolicyCondition{Claim: "loc", AtMost: float64Ptr(39)}, want: false},
		{name: "range contains the value", cond: config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(20), AtMost: float64Ptr(50)}, want: true},
		{name: "range excludes the value", cond: config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(10), AtMost: float64Ptr(20)}, want: false},
		{name: "exactly matches", cond: config.PolicyCondition{Claim: "loc", Exactly: float64Ptr(40)}, want: true},
		{name: "exactly misses", cond: config.PolicyCondition{Claim: "loc", Exactly: float64Ptr(39)}, want: false},
		{name: "equals matches", cond: config.PolicyCondition{Claim: "dept", Equals: stringPtr("eng")}, want: true},
		{name: "equals misses", cond: config.PolicyCondition{Claim: "dept", Equals: stringPtr("ops")}, want: false},
		{name: "one_of matches", cond: config.PolicyCondition{Claim: "dept", OneOf: []string{"eng", "ops"}}, want: true},
		{name: "one_of misses", cond: config.PolicyCondition{Claim: "dept", OneOf: []string{"sales", "ops"}}, want: false},
		{name: "contains matches a list member", cond: config.PolicyCondition{Claim: "projects", Contains: "apollo"}, want: true},
		{name: "contains misses", cond: config.PolicyCondition{Claim: "projects", Contains: "voyager"}, want: false},
		{
			name: "all_of needs every child",
			cond: config.PolicyCondition{AllOf: []config.PolicyCondition{
				{Group: "SSH Users"}, {Claim: "loc", AtLeast: float64Ptr(40)},
			}},
			want: true,
		},
		{
			name: "all_of fails on one child",
			cond: config.PolicyCondition{AllOf: []config.PolicyCondition{
				{Group: "SSH Users"}, {Claim: "loc", AtLeast: float64Ptr(41)},
			}},
			want: false,
		},
		{
			name: "any_of needs one child",
			cond: config.PolicyCondition{AnyOf: []config.PolicyCondition{
				{Group: "admin"}, {Claim: "loc", AtLeast: float64Ptr(40)},
			}},
			want: true,
		},
		{
			name: "any_of fails when no child passes",
			cond: config.PolicyCondition{AnyOf: []config.PolicyCondition{
				{Group: "admin"}, {Claim: "loc", AtLeast: float64Ptr(41)},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseCondition(&tt.cond, declared, false)
			if err != nil {
				t.Fatalf("parseCondition() error = %v", err)
			}
			if got := parsed.evaluate(identity); got != tt.want {
				t.Errorf("evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The invariant the whole design rests on: an absent claim is never
// neutral. Every comparator must take the floor rather than skipping the
// condition, on every shape of missing or unusable value. Each case makes
// the claim *under test* the unusable one, so a pass can only come from the
// comparator itself rather than from some other claim on the identity.
func TestCondition_ShouldFailClosedOnAnAbsentOrUnusableClaim(t *testing.T) {
	t.Parallel()

	numeric := config.PolicyCondition{Claim: "loc", AtLeast: float64Ptr(40)}
	atMost := config.PolicyCondition{Claim: "loc", AtMost: float64Ptr(40)}
	exactly := config.PolicyCondition{Claim: "loc", Exactly: float64Ptr(40)}
	equals := config.PolicyCondition{Claim: "dept", Equals: stringPtr("eng")}
	oneOf := config.PolicyCondition{Claim: "dept", OneOf: []string{"eng"}}
	contains := config.PolicyCondition{Claim: "projects", Contains: "apollo"}

	// A value that would satisfy each comparator if it were usable, so a
	// failure below is the absent path and not a plain mismatch.
	tests := []struct {
		name     string
		cond     config.PolicyCondition
		identity *Identity
	}{
		{name: "at_least/never captured", cond: numeric, identity: &Identity{Subject: "s"}},
		{name: "at_least/captured empty", cond: numeric, identity: extras("loc", scalarExtra(""))},
		{name: "at_least/not a number", cond: numeric, identity: extras("loc", scalarExtra("high"))},
		{name: "at_least/list under a numeric comparator", cond: numeric, identity: extras("loc", listExtra([]string{"40"}))},

		{name: "at_most/never captured", cond: atMost, identity: &Identity{Subject: "s"}},
		{name: "at_most/not a number", cond: atMost, identity: extras("loc", scalarExtra("low"))},

		{name: "exactly/never captured", cond: exactly, identity: &Identity{Subject: "s"}},
		{name: "exactly/not a number", cond: exactly, identity: extras("loc", scalarExtra("forty"))},

		{name: "equals/never captured", cond: equals, identity: &Identity{Subject: "s"}},
		{name: "equals/captured empty", cond: equals, identity: extras("dept", scalarExtra(""))},
		{name: "equals/list under a scalar comparator", cond: equals, identity: extras("dept", listExtra([]string{"eng"}))},

		{name: "one_of/never captured", cond: oneOf, identity: &Identity{Subject: "s"}},
		{name: "one_of/captured empty", cond: oneOf, identity: extras("dept", scalarExtra(""))},
		{name: "one_of/list under a scalar comparator", cond: oneOf, identity: extras("dept", listExtra([]string{"eng"}))},

		{name: "contains/never captured", cond: contains, identity: &Identity{Subject: "s"}},
		{name: "contains/scalar under a list comparator", cond: contains, identity: extras("projects", scalarExtra("apollo"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseCondition(&tt.cond, declared, false)
			if err != nil {
				t.Fatalf("parseCondition() error = %v", err)
			}
			if parsed.evaluate(tt.identity) {
				t.Error("expected the condition to fail closed, got a pass")
			}
		})
	}
}

// extras builds an identity carrying exactly one extra claim.
func extras(name string, v extraValue) *Identity {
	return &Identity{Subject: "sub-test", Extra: map[string]extraValue{name: v}}
}

// A `contains` against a scalar claim indicates a config mismatch worth
// surfacing, so a scalar deliberately does not degrade into a
// one-element list that would quietly match.
func TestCondition_ShouldNotDegradeAScalarIntoAListForContains(t *testing.T) {
	t.Parallel()

	parsed, err := parseCondition(&config.PolicyCondition{Claim: "projects", Contains: "apollo"}, declared, false)
	if err != nil {
		t.Fatalf("parseCondition() error = %v", err)
	}

	identity := &Identity{Subject: "sub-a", Extra: map[string]extraValue{"projects": scalarExtra("apollo")}}
	if parsed.evaluate(identity) {
		t.Error("a scalar claim must not satisfy contains, even when the value matches")
	}
}

// ReferencedClaims is what startup validation walks to catch a typo, so it
// has to reach into nested conditions rather than only the top level.
func TestReferencedClaims_ShouldCollectNestedClaimNames(t *testing.T) {
	t.Parallel()

	cond := config.PolicyCondition{AllOf: []config.PolicyCondition{
		{Group: "SSH Users"},
		{Claim: "loc", AtLeast: float64Ptr(20)},
		{Claim: "dept", Equals: stringPtr("eng")},
	}}

	got := cond.ReferencedClaims(nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 referenced claims, got %v", got)
	}
	if got[0] != "loc" || got[1] != "dept" {
		t.Errorf("referenced claims = %v, want [loc dept]", got)
	}
}
