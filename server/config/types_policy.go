package config

import (
	"fmt"
	"strconv"
	"strings"
)

// PolicyCondition is one condition in the certificate policy grammar: a
// predicate over the approver's identity that a tier's `when` or a
// certificate type's `require` evaluates. The grammar is deliberately
// closed — six forms, and no more:
//
//   - group: membership in an OIDC group, exactly the behaviour group tiers
//     had before conditions existed.
//   - claim with at_least / at_most: numeric comparison against an extra
//     claim (see authentication.fields.extra). Both keys together express a
//     range; boundaries are inclusive.
//   - claim with exactly: numeric equality, shorthand for at_least and
//     at_most set to the same value.
//   - claim with equals / one_of: scalar string equality, or membership of
//     the scalar in a fixed set.
//   - claim with contains: membership of a value in a list-valued claim.
//   - all_of / any_of: conjunction or disjunction over a list of the forms
//     above. One level of nesting only — a nested condition may not itself
//     carry all_of or any_of.
//
// Exactly one of group, claim, all_of, or any_of must be set. A claim
// condition takes exactly one comparator family. Every claim name referenced
// must be declared under authentication.fields.extra, checked at startup.
//
// An absent claim is never neutral: a missing or unparseable claim value
// fails the condition (the floor), loudly, and can never widen what an
// identity receives. A claim's value is only as fresh as the subject's last
// login — see authentication.fields.extra.
type PolicyCondition struct {
	// Group names an OIDC group the identity must be a member of.
	Group string `mapstructure:"group"`

	// Claim names an extra claim (a key under authentication.fields.extra)
	// the comparator keys below test. The server attaches no meaning to the
	// claim itself; it only compares the value.
	Claim string `mapstructure:"claim"`

	// AtLeast passes when the claim's numeric value is >= this bound
	// (inclusive). May be combined with at_most to express a range.
	AtLeast *float64 `mapstructure:"at_least"`

	// AtMost passes when the claim's numeric value is <= this bound
	// (inclusive). May be combined with at_least to express a range.
	AtMost *float64 `mapstructure:"at_most"`

	// Exactly passes when the claim's numeric value equals this value. It
	// desugars to at_least and at_most of the same value, so there is no
	// second comparison path. Right for an integer-valued score; a computed
	// confidence of 39.9999 does not equal 40, so a non-integral literal
	// here draws a startup warning.
	Exactly *float64 `mapstructure:"exactly"`

	// Equals passes when the claim's scalar string value equals this string.
	Equals *string `mapstructure:"equals"`

	// OneOf passes when the claim's scalar string value appears in this set.
	OneOf []string `mapstructure:"one_of"`

	// Contains passes when this value appears in a list-valued claim. A
	// scalar claim takes the absent path — the condition fails, loudly.
	Contains string `mapstructure:"contains"`

	// AllOf passes when every listed condition passes. Listed conditions may
	// not themselves carry all_of or any_of; nesting stops at one level.
	AllOf []PolicyCondition `mapstructure:"all_of"`

	// AnyOf passes when at least one listed condition passes. Listed
	// conditions may not themselves carry all_of or any_of; nesting stops at
	// one level.
	AnyOf []PolicyCondition `mapstructure:"any_of"`
}

// IsZero reports whether no condition is expressed at all — every key
// unset. Used to distinguish "no gate configured" from a malformed one.
func (c *PolicyCondition) IsZero() bool {
	return c == nil || (c.Group == "" && c.Claim == "" &&
		c.AtLeast == nil && c.AtMost == nil && c.Exactly == nil &&
		c.Equals == nil && len(c.OneOf) == 0 && c.Contains == "" &&
		len(c.AllOf) == 0 && len(c.AnyOf) == 0)
}

// String renders the condition in a compact canonical form for the
// effective-config view, policy explanations, and error messages, e.g.
// `all_of(group "SSH Users", claim loc >= 20)`.
func (c *PolicyCondition) String() string {
	if c.IsZero() {
		return ""
	}
	switch {
	case len(c.AllOf) > 0:
		return "all_of(" + joinConditions(c.AllOf) + ")"
	case len(c.AnyOf) > 0:
		return "any_of(" + joinConditions(c.AnyOf) + ")"
	case c.Group != "":
		return fmt.Sprintf("group %q", c.Group)
	}
	switch {
	case c.Exactly != nil:
		return fmt.Sprintf("claim %s == %s", c.Claim, formatBound(*c.Exactly))
	case c.AtLeast != nil && c.AtMost != nil:
		return fmt.Sprintf("claim %s in [%s, %s]", c.Claim, formatBound(*c.AtLeast), formatBound(*c.AtMost))
	case c.AtLeast != nil:
		return fmt.Sprintf("claim %s >= %s", c.Claim, formatBound(*c.AtLeast))
	case c.AtMost != nil:
		return fmt.Sprintf("claim %s <= %s", c.Claim, formatBound(*c.AtMost))
	case c.Equals != nil:
		return fmt.Sprintf("claim %s equals %q", c.Claim, *c.Equals)
	case len(c.OneOf) > 0:
		return fmt.Sprintf("claim %s one_of [%s]", c.Claim, strings.Join(c.OneOf, ", "))
	case c.Contains != "":
		return fmt.Sprintf("claim %s contains %q", c.Claim, c.Contains)
	}
	// not covered: unreachable after IsZero — every remaining shape has a
	// case above; kept so a future grammar key cannot render as "".
	return "claim " + c.Claim
}

// joinConditions renders a nested condition list for String.
func joinConditions(conds []PolicyCondition) string {
	parts := make([]string, 0, len(conds))
	for i := range conds {
		parts = append(parts, conds[i].String())
	}
	return strings.Join(parts, ", ")
}

// formatBound renders a numeric bound without a trailing decimal point for
// integral values, matching how claim values themselves are formatted.
func formatBound(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ReferencedClaims appends every claim name this condition references to
// out, including nested conditions. Startup validation checks each against
// the declared authentication.fields.extra names, so a typo fails the
// process instead of silently failing the condition on every evaluation.
func (c *PolicyCondition) ReferencedClaims(out []string) []string {
	if c == nil {
		return out
	}
	if c.Claim != "" {
		out = append(out, c.Claim)
	}
	for i := range c.AllOf {
		out = c.AllOf[i].ReferencedClaims(out)
	}
	for i := range c.AnyOf {
		out = c.AnyOf[i].ReferencedClaims(out)
	}
	return out
}
