package service

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The claims echo's report: an echoed ID token annotated against the
// configuration. It answers the setup questions a raw claim dump leaves
// open: which claim each field reads and what it resolves to, whether this
// token would sign in, which claims could fill a field that does not
// resolve, and which ones nothing could ever map.
//
// Resolution runs through the same readers a login uses (claims.go), never
// a copy of them, so the report cannot drift from what a login concludes.

// EchoFieldStatus is how one configured field resolved against the token.
type EchoFieldStatus string

const (
	// EchoFieldResolved: the claim is there and has the field's shape.
	EchoFieldResolved EchoFieldStatus = "resolved"
	// EchoFieldEmpty: the claim is there and the right shape, but empty.
	// For subject and username that fails a login.
	EchoFieldEmpty EchoFieldStatus = "empty"
	// EchoFieldMissing: the token has no such claim, or it is null.
	EchoFieldMissing EchoFieldStatus = "missing"
	// EchoFieldWrongShape: the claim is there but the field cannot read
	// it, such as an object where a list was expected.
	EchoFieldWrongShape EchoFieldStatus = "wrong_shape"
	// EchoFieldUnconfigured: no claim is configured for the field.
	EchoFieldUnconfigured EchoFieldStatus = "unconfigured"
)

// EchoField is one configured field's resolution: the claim it reads and
// what came of reading it.
type EchoField struct {
	// Field is the config key under authentication.fields: "subject",
	// "username", ..., or "extra.<name>".
	Field string
	// Claim is the configured claim name, empty when unconfigured.
	Claim string
	// Required is set for the two fields whose failure fails a login.
	Required bool
	Status   EchoFieldStatus
	// Value is the resolved scalar, and Values the resolved list, when
	// Status is resolved; List says which of the two the field holds.
	Value  string
	Values []string
	List   bool
}

// EchoSignIn says whether a login with this token would get past the claim
// mapping, and if not, the error the login would return. It cannot say
// more: whether the account is disabled, or what the directory would add,
// is decided against the database, which an echo does not touch.
type EchoSignIn struct {
	Allowed bool
	Reason  string
}

// Suggestion fields: which config key a ClaimSuggestion is for.
const (
	SuggestFieldSubject  = "subject"
	SuggestFieldUsername = "username"
	SuggestFieldGroups   = "groups"
	SuggestFieldExtra    = "extra"
)

// ClaimSuggestion is the config line that would put one claim to use.
// Suggestions, not decisions: each names the key a value would land under.
type ClaimSuggestion struct {
	// Field is the authentication.fields key the claim would fill, one of
	// the SuggestField constants.
	Field  string
	Claim  string
	Reason string
	YAML   string
}

// Unmappable sources: where an UnmappableClaim was found.
const (
	UnmappableSourceIDToken     = "id_token"
	UnmappableSourceAccessToken = "access_token"
)

// UnmappableClaim is a claim no authentication.fields entry can read: one
// holding a nested object (the mapper reads top-level claims only), or one
// in the access token, which a login never reads.
type UnmappableClaim struct {
	Claim  string
	Source string
	Value  any
	Reason string
}

// EchoReport is one claims echo, annotated.
type EchoReport struct {
	// Claims is what a login maps: the verified ID token, in full, with
	// what UserInfo filled in.
	Claims map[string]any
	// ClaimSources says where each claim came from (ClaimSourceIDToken or
	// ClaimSourceUserInfo).
	ClaimSources map[string]string
	// UserInfo is what happened to the UserInfo call.
	UserInfo UserInfoStatus
	// Mapping is the configured claim names.
	Mapping ClaimMapping
	// Fields resolves each configured field, reserved fields first in
	// config order, then the extras by name.
	Fields []EchoField
	// SignIn is the verdict of the login's own claim mapping.
	SignIn EchoSignIn
	// Suggestions are candidates for subject, username and groups, and the
	// extra fields that would capture the claims nothing reads.
	Suggestions []ClaimSuggestion
	// AdminGroupCandidates are the resolved groups claim's values, the
	// names admin.require_group can be set to. Empty when groups does not
	// resolve.
	AdminGroupCandidates []string
	// NotMappable lists the claims no field could read.
	NotMappable []UnmappableClaim
	// ProtocolClaims names the token's JWT and OIDC mechanics that no field
	// reads, so a page can group them apart from identity.
	ProtocolClaims []string
}

// BuildEchoReport annotates tokens against mapping.
func BuildEchoReport(tokens *EchoTokens, mapping ClaimMapping) *EchoReport {
	claims := tokens.Claims
	report := &EchoReport{
		Claims:       claims,
		ClaimSources: tokens.Sources,
		UserInfo:     tokens.UserInfo,
		Mapping:      mapping,
		Fields:       echoFields(claims, mapping),
		Suggestions:  append(fieldCandidates(claims, mapping), claimSuggestions(claims, mapping)...),
		NotMappable:  notMappable(tokens),
	}

	// The login's own mapping, run for its verdict only. The identity it
	// builds is dropped: an echo establishes nothing.
	if _, err := identityFromClaims(claims, mapping); err != nil {
		report.SignIn = EchoSignIn{Reason: err.Error()}
	} else {
		report.SignIn = EchoSignIn{Allowed: true}
	}

	if groups, ok := stringSliceValue(claims, mapping.Groups); ok {
		report.AdminGroupCandidates = dedupe(groups)
	}

	consumed := consumedClaims(mapping)
	for _, name := range sortedClaimNames(claims) {
		if isProtocolClaim(name) && !consumed[name] {
			report.ProtocolClaims = append(report.ProtocolClaims, name)
		}
	}
	return report
}

// echoFields resolves every configured field through the reader a login
// uses for it.
func echoFields(claims map[string]any, mapping ClaimMapping) []EchoField {
	scalar := func(field, claim string, required bool, read func(map[string]any, string) (string, bool)) EchoField {
		f := EchoField{Field: field, Claim: claim, Required: required}
		value, ok := read(claims, claim)
		f.Status = fieldStatus(claims, claim, ok, value == "")
		if ok {
			f.Value = value
		}
		return f
	}
	list := func(field, claim string) EchoField {
		f := EchoField{Field: field, Claim: claim, List: true}
		values, ok := stringSliceValue(claims, claim)
		f.Status = fieldStatus(claims, claim, ok, len(values) == 0)
		f.Values = values
		return f
	}

	fields := []EchoField{
		scalar("subject", mapping.Subject, true, scalarClaim),
		scalar("username", mapping.Username, true, usernameClaim),
		scalar("name", mapping.Name, false, scalarClaim),
		list("groups", mapping.Groups),
		list("other_accounts", mapping.OtherAccounts),
		list("service_accounts", mapping.ServiceAccounts),
		scalar("email", mapping.Email, false, emailClaim),
	}

	names := make([]string, 0, len(mapping.Extra))
	for name := range mapping.Extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		claim := mapping.Extra[name]
		f := EchoField{Field: "extra." + name, Claim: claim}
		value, ok := extraClaimValue(claims[claim])
		if ok {
			f.List = value.isList
			f.Value, f.Values = value.scalar, value.list
		}
		f.Status = fieldStatus(claims, claim, ok, value.scalar == "" && len(value.list) == 0)
		fields = append(fields, f)
	}
	return fields
}

// fieldStatus classifies one read: unconfigured, missing (absent or null),
// read but empty, the wrong shape, or resolved.
func fieldStatus(claims map[string]any, claim string, read, empty bool) EchoFieldStatus {
	if claim == "" {
		return EchoFieldUnconfigured
	}
	raw, present := claims[claim]
	switch {
	case !present || raw == nil:
		return EchoFieldMissing
	case raw == "":
		// Before the shape check: the username reader refuses an empty
		// string outright, and "empty" is the truer word for it.
		return EchoFieldEmpty
	case !read:
		return EchoFieldWrongShape
	case empty:
		return EchoFieldEmpty
	default:
		return EchoFieldResolved
	}
}

// subjectCandidates are the identifier claims worth naming for
// authentication.fields.subject, with why each is stable.
var subjectCandidates = []struct{ claim, reason string }{
	{"sub", "OIDC's own identifier, unique and stable per issuer; Entra ID issues a different one per application"},
	{"oid", "Entra ID's object id, the same for this account in every application of the tenant"},
}

// usernameCandidates are the claims providers commonly put an account name
// in, worth naming for authentication.fields.username.
var usernameCandidates = []string{"preferred_username", "upn", "unique_name", "username", "nickname", "email"}

// fieldCandidates proposes claims for the subject, username and groups
// fields from what this token actually carries, skipping the claim each
// field already reads.
func fieldCandidates(claims map[string]any, mapping ClaimMapping) []ClaimSuggestion {
	var out []ClaimSuggestion
	for _, c := range subjectCandidates {
		if v, ok := scalarClaim(claims, c.claim); ok && v != "" && c.claim != mapping.Subject {
			out = append(out, fieldSuggestion(SuggestFieldSubject, c.claim, c.reason))
		}
	}
	for _, claim := range usernameCandidates {
		if _, ok := usernameClaim(claims, claim); ok && claim != mapping.Username {
			out = append(out, fieldSuggestion(SuggestFieldUsername, claim,
				"a string; the account name on target hosts, and the default certificate principal"))
		}
	}

	consumed := consumedClaims(mapping)
	for _, name := range sortedClaimNames(claims) {
		if consumed[name] || isProtocolClaim(name) {
			continue
		}
		values, ok := stringSliceValue(claims, name)
		if !ok || len(values) == 0 {
			continue
		}
		out = append(out, fieldSuggestion(SuggestFieldGroups, name,
			fmt.Sprintf("a list of %d string(s); groups gate policy and roles, never a certificate", len(values))))
	}
	return out
}

// fieldSuggestion builds the suggestion that sets one reserved field.
func fieldSuggestion(field, claim, reason string) ClaimSuggestion {
	return ClaimSuggestion{
		Field:  field,
		Claim:  claim,
		Reason: reason,
		YAML:   fmt.Sprintf("authentication:\n  fields:\n    %s: %s", field, yamlClaimName(claim)),
	}
}

// consumedClaims is the set of claim names some configured field reads.
func consumedClaims(mapping ClaimMapping) map[string]bool {
	consumed := map[string]bool{}
	for _, name := range []string{
		mapping.Subject, mapping.Username, mapping.Name, mapping.Groups,
		mapping.OtherAccounts, mapping.ServiceAccounts, mapping.Email,
	} {
		if name != "" {
			consumed[name] = true
		}
	}
	for _, claim := range mapping.Extra {
		consumed[claim] = true
	}
	return consumed
}

// sortedClaimNames returns the claim names in order.
func sortedClaimNames(claims map[string]any) []string {
	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// claimSuggestions turns the claims nothing reads into the config lines that
// would capture them.
//
// This is what makes the echo a tool rather than a curiosity. A claim dump
// answers "what does my IdP send"; a claim dump that names the key each
// value would land under answers "what do I write in the config file".
// Objects are left out: an extra field cannot read one (see notMappable).
func claimSuggestions(claims map[string]any, mapping ClaimMapping) []ClaimSuggestion {
	consumed := consumedClaims(mapping)

	var out []ClaimSuggestion
	for _, name := range sortedClaimNames(claims) {
		if consumed[name] || isProtocolClaim(name) {
			continue
		}
		if _, isObject := claims[name].(map[string]any); isObject {
			continue
		}
		out = append(out, ClaimSuggestion{
			Field:  SuggestFieldExtra,
			Claim:  name,
			Reason: claimSuggestionReason(claims[name]),
			YAML: fmt.Sprintf("authentication:\n  fields:\n    extra:\n      %s: %s",
				suggestedExtraName(name), yamlClaimName(name)),
		})
	}
	return out
}

// claimSuggestionReason says what the value looks like, since the shape is
// what decides whether it can gate a policy condition or only decorate a key
// ID.
func claimSuggestionReason(value any) string {
	switch v := value.(type) {
	case float64:
		return "numeric, so a policy condition could compare against it"
	case bool:
		return "boolean; captured as its string form"
	case []any:
		return fmt.Sprintf("a list of %d value(s); captured as a list", len(v))
	case string:
		return "unmapped: nothing in the configuration reads this claim"
	default:
		return "unmapped, and not a scalar or list, so an extra field would store it empty"
	}
}

// notMappable lists the claims no authentication.fields entry could read:
// the ID token's objects and lists of objects, and every access token
// claim.
func notMappable(tokens *EchoTokens) []UnmappableClaim {
	var out []UnmappableClaim
	for _, name := range sortedClaimNames(tokens.Claims) {
		switch v := tokens.Claims[name].(type) {
		case map[string]any:
			out = append(out, UnmappableClaim{
				Claim: name, Source: UnmappableSourceIDToken, Value: v,
				Reason: "an object; authentication.fields reads top-level claims only, so nothing inside it can be mapped",
			})
		case []any:
			if len(v) > 0 && !slices.ContainsFunc(v, func(e any) bool { _, ok := e.(string); return ok }) {
				out = append(out, UnmappableClaim{
					Claim: name, Source: UnmappableSourceIDToken, Value: v,
					Reason: "a list with no string items; a list field keeps only strings, so it would read as empty",
				})
			}
		}
	}
	for _, name := range sortedClaimNames(tokens.AccessTokenClaims) {
		out = append(out, UnmappableClaim{
			Claim: name, Source: UnmappableSourceAccessToken, Value: tokens.AccessTokenClaims[name],
			Reason: "in the access token (unverified); ssoossh reads only the ID token, so configure the provider to put it there",
		})
	}
	return out
}

// protocolClaims are the JWT and OIDC mechanics rather than identity, so
// suggesting a mapping for them would be noise.
var protocolClaims = []string{
	"iss", "aud", "exp", "iat", "nbf", "jti", "nonce", "azp", "at_hash",
	"c_hash", "auth_time", "sid", "typ", "sub",
}

// isProtocolClaim reports whether name is protocol mechanics.
func isProtocolClaim(name string) bool {
	return slices.Contains(protocolClaims, name)
}

// suggestedExtraName turns a claim name into an extra field name a template
// can reach with dotted syntax. A namespaced claim keeps only its last
// segment, since that is what an operator would name it anyway.
func suggestedExtraName(claim string) string {
	name := claim
	if i := strings.LastIndexAny(name, "/:#"); i >= 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r + ('a' - 'A'))
		default:
			out.WriteByte('_')
		}
	}
	return out.String()
}

// yamlClaimName quotes a claim name that YAML would otherwise misread: a
// namespaced claim URL, most often.
func yamlClaimName(claim string) string {
	if strings.ContainsAny(claim, ":#{}[],&*?|<>=!%@`\"' ") {
		return fmt.Sprintf("%q", claim)
	}
	return claim
}
