package service

import (
	"fmt"
	"log/slog"
	"strconv"
)

// This file is how ID token claims become an Identity: one reader per
// field shape, and identityFromClaims, which applies them per the
// configured mapping. A login (HandleCallback) and the claims echo
// (BuildEchoReport) both call these, so what the echo says a login would
// conclude is what a login does conclude.

// identityFromClaims maps verified ID token claims onto an Identity per
// mapping, or returns the error that fails a login: a subject or username
// claim that is absent, empty or the wrong shape. Every other field is
// optional and resolves to empty.
//
// mapping is AuthService.ClaimMapping, so the email claim already carries
// its "email" fallback.
func identityFromClaims(claims map[string]any, mapping ClaimMapping) (*Identity, error) {
	// The account identifier, from whichever claim the operator named. Not
	// the token's own Subject: that is always the literal "sub", and "sub"
	// is not the stable identifier everywhere. Entra ID issues a
	// per-application one and puts the tenant-stable value in "oid". A
	// login with no identifier cannot be keyed to a user row at all, so an
	// absent or empty claim fails the login rather than inventing one.
	subjectID, ok := scalarClaim(claims, mapping.Subject)
	if !ok || subjectID == "" {
		return nil, fmt.Errorf("OIDC ID token is missing the configured subject claim %q", mapping.Subject)
	}

	username, ok := usernameClaim(claims, mapping.Username)
	if !ok {
		return nil, fmt.Errorf("OIDC ID token is missing the configured username claim %q", mapping.Username)
	}

	// The human-readable name is optional in every direction: an
	// unconfigured claim, an absent one, or one of the wrong shape all
	// leave it empty, and nothing downstream requires it. It is display
	// only.
	displayName, _ := scalarClaim(claims, mapping.Name)

	// Absence of email is not an error either.
	email, _ := emailClaim(claims, mapping.Email)

	return &Identity{
		Subject:         subjectID,
		Username:        username,
		Email:           email,
		DisplayName:     displayName,
		Groups:          stringSliceClaim(claims, mapping.Groups),
		OtherAccounts:   stringSliceClaim(claims, mapping.OtherAccounts),
		ServiceAccounts: stringSliceClaim(claims, mapping.ServiceAccounts),
		Extra:           extraClaims(claims, mapping.Extra),
	}, nil
}

// scalarClaim reads key from claims as a single string, coercing the scalar
// JSON shapes the way extraClaims does: a string is itself, a bool and a
// number render as text. An unconfigured key, an absent claim, or a
// composite value (array, object) reports false.
//
// Numbers are accepted because an account identifier is not always a
// string: a provider fronting a database commonly issues a numeric user
// id, and encoding/json decodes every JSON number as float64.
func scalarClaim(claims map[string]any, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	return scalarValue(claims[key])
}

// scalarValue renders one scalar JSON value as text, reporting false for
// anything else (null, an array, an object).
func scalarValue(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		// -1 precision renders integral values without a decimal point.
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return "", false
	}
}

// usernameClaim reads the username: a non-empty string and nothing else,
// since it becomes the default certificate principal and a number there is
// almost certainly the wrong claim.
func usernameClaim(claims map[string]any, key string) (string, bool) {
	username, ok := claims[key].(string)
	return username, ok && username != ""
}

// emailClaim reads the email address: a string, or nothing. key is the
// mapping's email claim, which ClaimMapping already defaults to "email".
func emailClaim(claims map[string]any, key string) (string, bool) {
	email, ok := claims[key].(string)
	return email, ok
}

// stringSliceValue reads key from claims as a list, keeping its string
// items and dropping the rest. ok is false for an unconfigured key, an
// absent claim, or a value that is not a JSON array.
func stringSliceValue(claims map[string]any, key string) ([]string, bool) {
	if key == "" {
		return nil, false
	}
	raw, ok := claims[key].([]any)
	if !ok {
		return nil, false
	}
	values := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			values = append(values, s)
		}
	}
	return values, true
}

// stringSliceClaim reads an optional list field, returning nil when key is
// empty (the field is unconfigured) and warning, then returning nil, when
// a configured claim is absent or not a list: the operator asked for it,
// so its absence is worth a log line, but never a failed login.
func stringSliceClaim(claims map[string]any, key string) []string {
	values, ok := stringSliceValue(claims, key)
	if !ok && key != "" {
		slog.Warn("OIDC ID token is missing the configured claim", slog.String("claim", key))
	}
	return values
}

// extraClaimValue converts one extra claim's raw value: scalars (string,
// bool, number) coerce to strings and an array keeps its string items as a
// list. ok is false for a missing, null or unsupported-shape value.
func extraClaimValue(raw any) (extraValue, bool) {
	if s, ok := scalarValue(raw); ok {
		return scalarExtra(s), true
	}
	if list, ok := raw.([]any); ok {
		values := make([]string, 0, len(list))
		for _, e := range list {
			if s, ok := e.(string); ok {
				values = append(values, s)
			}
		}
		return listExtra(values), true
	}
	return scalarExtra(""), false
}

// extraClaims extracts the operator-configured extra fields (mapping:
// template name -> claim name, see config.OAuthFields.Extra) from claims.
// A missing, null, or unsupported-shape claim warns and stores empty, the
// same optional-field posture as stringSliceClaim, which key ID templates
// later render as MISSING. Returns nil when no extras are configured.
func extraClaims(claims map[string]any, mapping map[string]string) map[string]extraValue {
	if len(mapping) == 0 {
		return nil
	}

	extras := make(map[string]extraValue, len(mapping))
	for name, claim := range mapping {
		raw, present := claims[claim]
		value, ok := extraClaimValue(raw)
		switch {
		case !present || raw == nil:
			slog.Warn("OIDC ID token is missing the configured extra claim",
				slog.String("field", name), slog.String("claim", claim))
		case !ok:
			slog.Warn("configured extra claim has an unsupported shape",
				slog.String("field", name), slog.String("claim", claim))
		}
		extras[name] = value
	}
	return extras
}
