package service

import (
	"fmt"
	"log/slog"
	"math"
	"slices"

	"github.com/mnestor/ssoossh/server/config"
)

// parsedCondition is the validated, evaluatable form of a
// config.PolicyCondition. Parsing settles the grammar questions once at
// startup — exactly one form per condition, one comparator family per
// claim, one level of nesting, every referenced claim declared — so
// evaluation is a plain walk with no error path.
type parsedCondition struct {
	kind condKind

	// group for condGroup.
	group string

	// claim plus exactly one comparator family for condClaim. Exactly
	// desugars to atLeast+atMost at parse, so no second comparison path
	// exists here.
	claim    string
	atLeast  *float64
	atMost   *float64
	equals   *string
	oneOf    []string
	contains string

	// children for condAllOf / condAnyOf.
	children []parsedCondition

	// rendered is the canonical string form, kept for policy explanations
	// and error messages.
	rendered string
}

// condKind discriminates the four condition forms.
type condKind int

const (
	condGroup condKind = iota
	condClaim
	condAllOf
	condAnyOf
)

// parseCondition validates cond against the closed grammar and the declared
// extra-claim names, failing startup on any violation. nested marks a child
// of all_of/any_of, which may not itself nest further.
func parseCondition(cond *config.PolicyCondition, declaredClaims map[string]string, nested bool) (*parsedCondition, error) {
	if cond.IsZero() {
		return nil, fmt.Errorf("condition is empty: exactly one of group, claim, all_of, or any_of must be set")
	}

	if err := checkExactlyOneForm(cond); err != nil {
		return nil, err
	}

	out := &parsedCondition{rendered: cond.String()}

	switch {
	case len(cond.AllOf) > 0 || len(cond.AnyOf) > 0:
		if nested {
			return nil, fmt.Errorf("condition %s nests all_of/any_of inside another: nesting stops at one level", cond.String())
		}
		out.kind = condAllOf
		list := cond.AllOf
		if len(cond.AnyOf) > 0 {
			out.kind = condAnyOf
			list = cond.AnyOf
		}
		for i := range list {
			child, err := parseCondition(&list[i], declaredClaims, true)
			if err != nil {
				return nil, err
			}
			out.children = append(out.children, *child)
		}
		return out, nil

	case cond.Group != "":
		out.kind = condGroup
		out.group = cond.Group
		return out, nil
	}

	// Claim form: the name must be declared under authentication.fields.extra
	// so a typo fails the process rather than silently failing the condition
	// on every evaluation.
	if _, ok := declaredClaims[cond.Claim]; !ok {
		return nil, fmt.Errorf("condition %s references claim %q, which is not declared under authentication.fields.extra", cond.String(), cond.Claim)
	}
	out.kind = condClaim
	out.claim = cond.Claim
	if err := parseComparator(cond, out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkExactlyOneForm enforces the grammar's outer shape: one of group,
// claim, all_of, or any_of, and no comparator floating free of a claim.
func checkExactlyOneForm(cond *config.PolicyCondition) error {
	forms := 0
	for _, set := range []bool{cond.Group != "", cond.Claim != "", len(cond.AllOf) > 0, len(cond.AnyOf) > 0} {
		if set {
			forms++
		}
	}
	if forms != 1 {
		return fmt.Errorf("condition %s mixes forms: exactly one of group, claim, all_of, or any_of must be set", cond.String())
	}

	hasComparator := cond.AtLeast != nil || cond.AtMost != nil || cond.Exactly != nil ||
		cond.Equals != nil || len(cond.OneOf) > 0 || cond.Contains != ""
	if cond.Claim == "" && hasComparator {
		return fmt.Errorf("condition %s has a comparator but no claim", cond.String())
	}
	return nil
}

// parseComparator validates that cond uses exactly one comparator family
// and copies it onto out. Bounds must be finite, and `exactly` desugars to
// an inclusive range so there is only ever one numeric comparison path.
func parseComparator(cond *config.PolicyCondition, out *parsedCondition) error {
	comparators := 0
	if cond.AtLeast != nil || cond.AtMost != nil {
		comparators++
		out.atLeast, out.atMost = cond.AtLeast, cond.AtMost
	}
	if cond.Exactly != nil {
		comparators++
		out.atLeast, out.atMost = cond.Exactly, cond.Exactly
		if *cond.Exactly != math.Trunc(*cond.Exactly) {
			// The right operator for an integer-valued score and the wrong
			// one for a computed confidence: 39.9999 does not equal 40, and
			// nothing at evaluation time would say why. A warning, not an
			// error, because integer-valued claims are the normal case.
			slog.Warn("policy condition compares a claim with a non-integral exactly value; a computed score will almost never match it",
				"condition", cond.String())
		}
	}
	if cond.Equals != nil {
		comparators++
		if *cond.Equals == "" {
			return fmt.Errorf("condition on claim %q has an empty equals value; an empty scalar is the absent representation and can never match", cond.Claim)
		}
		out.equals = cond.Equals
	}
	if len(cond.OneOf) > 0 {
		comparators++
		out.oneOf = cond.OneOf
	}
	if cond.Contains != "" {
		comparators++
		out.contains = cond.Contains
	}
	if comparators != 1 {
		return fmt.Errorf("condition on claim %q must use exactly one comparator family (at_least/at_most, exactly, equals, one_of, or contains)", cond.Claim)
	}
	for _, bound := range []*float64{out.atLeast, out.atMost} {
		if bound != nil && (math.IsInf(*bound, 0) || math.IsNaN(*bound)) {
			return fmt.Errorf("condition on claim %q has a non-finite numeric bound", cond.Claim)
		}
	}
	return nil
}

// evaluate reports whether identity satisfies the condition. An absent or
// unusable claim value never satisfies anything — it resolves to the floor,
// loudly, so a missing claim can never be the most generous outcome.
func (c *parsedCondition) evaluate(identity *Identity) bool {
	switch c.kind {
	case condGroup:
		return slices.Contains(identity.Groups, c.group)

	case condAllOf:
		for i := range c.children {
			if !c.children[i].evaluate(identity) {
				return false
			}
		}
		return true

	case condAnyOf:
		for i := range c.children {
			if c.children[i].evaluate(identity) {
				return true
			}
		}
		return false
	}

	// Claim form. A lookup miss on a declared claim means the value was
	// absent at the subject's last login.
	value, present := identity.Extra[c.claim]
	if !present {
		return c.absent(identity, "claim was not captured at login")
	}
	return c.compare(identity, value)
}

// compare applies the condition's one comparator family to a claim value
// that is present but may still be unusable for this comparator.
func (c *parsedCondition) compare(identity *Identity, value extraValue) bool {
	switch {
	case c.atLeast != nil || c.atMost != nil:
		num, ok := value.Number()
		if !ok {
			return c.absent(identity, "claim value is not a finite number")
		}
		if c.atLeast != nil && num < *c.atLeast {
			return false
		}
		return c.atMost == nil || num <= *c.atMost

	case c.equals != nil:
		s, ok := value.Scalar()
		if !ok {
			return c.absent(identity, "claim value is not a scalar")
		}
		return s == *c.equals

	case len(c.oneOf) > 0:
		s, ok := value.Scalar()
		if !ok {
			return c.absent(identity, "claim value is not a scalar")
		}
		return slices.Contains(c.oneOf, s)

	default: // contains
		list, ok := value.List()
		if !ok {
			return c.absent(identity, "claim value is not a list")
		}
		return slices.Contains(list, c.contains)
	}
}

// absent is the single floor path for a claim that cannot be compared:
// always false, always logged, so an identity quietly missing a score is an
// observable event rather than a silent short certificate.
func (c *parsedCondition) absent(identity *Identity, why string) bool {
	slog.Warn("policy condition resolved to the floor over an absent claim",
		"condition", c.rendered,
		"claim", c.claim,
		"subject", identity.Subject,
		"reason", why)
	return false
}

// String returns the canonical rendering fixed at parse time.
func (c *parsedCondition) String() string { return c.rendered }
