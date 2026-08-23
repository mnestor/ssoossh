package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// missingPlaceholder is what an extra field renders as in a key ID when it
// has no value — the configured claim was absent at login, or the template
// references an extra name that was never configured. Key IDs should show
// an auditable gap rather than silently collapse (see
// docs/certificate-keyid-template.md).
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
}

// scalarExtra wraps a scalar claim value.
func scalarExtra(s string) extraValue { return extraValue{scalar: s} }

// listExtra wraps a list claim value.
func listExtra(l []string) extraValue { return extraValue{list: l, isList: true} }

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
