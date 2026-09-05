package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
)

// missingPlaceholder is what an extra field renders as in a key ID when it
// has no value — the configured claim was absent at login, or the template
// references an extra name that was never configured. Key IDs should show
// an auditable gap rather than silently collapse (see
// https://mnestor.github.io/ssoossh/operations/key-id-templates/).
const missingPlaceholder = "MISSING"

// extraValue is one configured extra field's value, sourced from an OIDC
// claim at login (see config.OAuthFields.Extra): either a scalar string or
// a list of strings, JSON-encoded as such on model.User.ExtraFields. Its
// zero value renders as missingPlaceholder, which is what makes
// text/template's "missingkey=zero" option print MISSING for lookups of
// unconfigured names (templates print via fmt, honoring Stringer).
type extraValue struct {
	scalar string
	list   []string
	isList bool
	// num is the scalar parsed as a finite float64, decided once here at
	// construction rather than at every policy evaluation. It exists
	// because comparing scores as strings orders them lexicographically —
	// "9" >= "40" — which would grant the longest lifetimes to the lowest
	// scores. See Number.
	num   float64
	isNum bool
}

// scalarExtra wraps a scalar claim value. A value that parses as a finite
// number additionally carries its numeric form, whichever path it arrived
// by (a JSON number in the ID token, a numeric string from the IdP, or
// re-hydration from the users row).
func scalarExtra(s string) extraValue {
	v := extraValue{scalar: s}
	if s == "" {
		return v
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		v.num, v.isNum = f, true
	}
	return v
}

// listExtra wraps a list claim value.
func listExtra(l []string) extraValue { return extraValue{list: l, isList: true} }

// Number returns the value as a finite float64. ok is false for lists,
// empty scalars, and scalars that do not parse as a number — the absent
// path a numeric policy condition resolves to the floor.
func (v extraValue) Number() (f float64, ok bool) { return v.num, v.isNum && !v.isList }

// List returns the value's list form, or nil for scalars — used by
// membership conditions, which deliberately do not degrade a scalar into a
// one-element list (a scalar claim under `contains` indicates a config
// mismatch worth surfacing, not matching).
func (v extraValue) List() ([]string, bool) {
	if !v.isList {
		return nil, false
	}
	return v.list, true
}

// Scalar returns the scalar string form, empty for lists. ok is false when
// there is no usable scalar value — the absent path.
func (v extraValue) Scalar() (string, bool) {
	if v.isList || v.scalar == "" {
		return "", false
	}
	return v.scalar, true
}

// String renders the value for direct template interpolation: the scalar,
// a comma-joined list, or missingPlaceholder when empty either way.
func (v extraValue) String() string { return v.Join(",") }

// Join renders the value like String but with an explicit list separator —
// exposed to key ID templates as the "join" function.
func (v extraValue) Join(sep string) string {
	if v.isList {
		if len(v.list) == 0 {
			return missingPlaceholder
		}
		return strings.Join(v.list, sep)
	}
	if v.scalar == "" {
		return missingPlaceholder
	}
	return v.scalar
}

// decodeExtraFields decodes a model.User.ExtraFields column into the map
// key ID templates consume. Rows predating the extra_fields migration hold
// "" (the column default), which is simply no extras. Malformed JSON warns
// and returns nil rather than failing the approval — upsertUser only ever
// writes valid JSON, and a template renders MISSING for absent extras, so
// degrading beats blocking issuance over one corrupt audit-adjacent field.
func decodeExtraFields(raw string) map[string]extraValue {
	if raw == "" {
		return nil
	}
	var extras map[string]extraValue
	if err := json.Unmarshal([]byte(raw), &extras); err != nil {
		slog.Warn("failed to decode the user's stored extra fields", slog.String("error", err.Error()))
		return nil
	}
	return extras
}

// MarshalJSON encodes the value as what it is: a JSON string for scalars,
// a JSON array of strings for lists.
func (v extraValue) MarshalJSON() ([]byte, error) {
	if v.isList {
		if v.list == nil {
			return json.Marshal([]string{})
		}
		return json.Marshal(v.list)
	}
	return json.Marshal(v.scalar)
}

// UnmarshalJSON accepts only the two shapes MarshalJSON produces. Anything
// else is an error: extraction (extraClaims) coerces claims before values
// are ever persisted, so other shapes in the database indicate corruption.
func (v *extraValue) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = scalarExtra(s)
		return nil
	}
	var l []string
	if err := json.Unmarshal(data, &l); err == nil {
		*v = listExtra(l)
		return nil
	}
	return fmt.Errorf("extra field value must be a string or an array of strings, got %s", data)
}
