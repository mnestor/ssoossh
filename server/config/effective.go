package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Redacted stands in for a configured secret in the effective-config view.
// The same text internal/tracelog uses, so an operator sees one spelling
// across the client's trace output and the server's config screen.
const Redacted = "[redacted]"

// maxWalkDepth bounds the recursion in appendSettings. Config nests about
// six levels at its deepest (cert_options.user.lifetime_policy.tiers[0].when
// .all_of[0]), so this is headroom rather than a limit anything reaches; it
// exists so a pointer cycle introduced later truncates instead of hanging
// the request that walks into it.
const maxWalkDepth = 24

// Setting is one leaf of the server's effective configuration.
type Setting struct {
	// Key is the dotted path of mapstructure keys from the root of Config,
	// e.g. "http.tls.min_version". Squashed structs contribute their fields
	// at the parent's level and slice elements carry an index, so a key is
	// the path an operator would write in their own config file.
	Key string

	// Value is what is in effect, rendered as text. Empty means unset: an
	// empty string, a nil pointer, or an empty list or map.
	Value string

	// Secret marks a key whose value is never rendered. Value is Redacted
	// when a secret is configured and empty when none is, so "is the client
	// secret set?" stays answerable without disclosing it.
	Secret bool
}

// Effective walks the configuration and returns every leaf in it, in
// declaration order, with secrets redacted.
//
// It reflects over the struct rather than listing keys by hand: a view whose
// job is to state what is actually in effect is wrong the moment a new
// config key is added and nobody remembers to add it here, and an operator
// reading the screen has no way to tell an unset key from an unlisted one.
// Sensitive fields carry a `secret:"true"` tag at their declaration, which
// is the only place someone adding one is guaranteed to be looking.
func (c *Config) Effective() []Setting {
	out := make([]Setting, 0, 256)
	appendSettings(&out, "", reflect.ValueOf(c).Elem(), false, 0)
	return out
}

// appendSettings renders rv into out under key: a leaf appends one Setting,
// a composite recurses with an extended key. secret is inherited, so tagging
// a struct field redacts everything beneath it.
func appendSettings(out *[]Setting, key string, rv reflect.Value, secret bool, depth int) {
	if depth > maxWalkDepth {
		return
	}

	if value, ok := leafValue(rv); ok {
		*out = append(*out, newSetting(key, value, secret))
		return
	}

	switch rv.Kind() {
	case reflect.Pointer:
		// Non-nil by now: leafValue renders a nil pointer as unset.
		appendSettings(out, key, rv.Elem(), secret, depth+1)

	case reflect.Struct:
		appendStruct(out, key, rv, secret, depth)

	case reflect.Map:
		// An empty map still names a key that exists, so it appears as
		// unset rather than vanishing from the view.
		if rv.Len() == 0 {
			*out = append(*out, newSetting(key, "", secret))
			return
		}
		for _, mapKey := range sortedKeys(rv) {
			segment := fmt.Sprint(mapKey.Interface())
			appendSettings(out, joinKey(key, segment), rv.MapIndex(mapKey), secret, depth+1)
		}

	case reflect.Slice, reflect.Array:
		// Only slices of composites reach here; leafValue joins the
		// scalar ones onto a single line.
		for i := range rv.Len() {
			appendSettings(out, fmt.Sprintf("%s[%d]", key, i), rv.Index(i), secret, depth+1)
		}

	default:
		// not covered: unreachable. leafValue renders every remaining
		// kind, so the switch above sees composites only; kept so a kind
		// it ever stops handling is dropped rather than mis-rendered.
	}
}

// appendStruct walks the exported fields of rv in declaration order, taking
// each field's key from its mapstructure tag.
func appendStruct(out *[]Setting, prefix string, rv reflect.Value, secret bool, depth int) {
	rt := rv.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}

		name, squash, skip := fieldKey(field)
		if skip {
			continue
		}

		// A squashed field's own name never appears: mapstructure lifts
		// its contents into the parent, which is how SignerConfig's keys
		// stay top-level in the YAML an operator writes.
		childKey := prefix
		if !squash {
			childKey = joinKey(prefix, name)
		}

		appendSettings(out, childKey, rv.Field(i), secret || isSecret(field), depth+1)
	}
}

// leafValue renders rv as a single value, reporting false when rv is a
// composite the caller has to walk into instead.
func leafValue(rv reflect.Value) (string, bool) {
	if text, ok := stringerValue(rv); ok {
		return text, true
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return "", true
		}
		return leafValue(rv.Elem())

	case reflect.String:
		return rv.String(), true

	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true

	case reflect.Float32, reflect.Float64:
		// -1 precision so a whole number reads as "10" rather than
		// "10.000000": every float in the configuration is a rate or a
		// claim bound an operator typed as an integer.
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), true

	case reflect.Slice, reflect.Array:
		return listValue(rv)

	default:
		// Structs and maps: the caller walks them.
		return "", false
	}
}

// listValue renders a list of scalars onto one line, reporting false for a
// list of composites so the caller indexes into it instead.
func listValue(rv reflect.Value) (string, bool) {
	if rv.Len() == 0 {
		return "", true
	}

	parts := make([]string, 0, rv.Len())
	for i := range rv.Len() {
		part, ok := leafValue(rv.Index(i))
		if !ok {
			return "", false
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", "), true
}

// stringerValue renders rv through fmt.Stringer when it has one, so a type
// with a canonical text form (time.Duration, *PolicyCondition) reads as that
// form rather than as its fields spread over several keys.
func stringerValue(rv reflect.Value) (string, bool) {
	if !rv.IsValid() || !rv.CanInterface() {
		return "", false
	}

	// A nil pointer takes the unset path in leafValue rather than calling
	// String on a nil receiver, which most implementations do not survive.
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return "", false
	}

	if s, ok := rv.Interface().(fmt.Stringer); ok {
		return s.String(), true
	}

	// A String method on the pointer receiver, which is the common
	// declaration, is only reachable through an addressable value. Struct
	// fields and slice elements are addressable here because the walk
	// starts from a *Config; map values are not, and fall through to being
	// walked field by field.
	if rv.CanAddr() {
		if s, ok := rv.Addr().Interface().(fmt.Stringer); ok {
			return s.String(), true
		}
	}

	return "", false
}

// newSetting builds one Setting, replacing a configured secret's value with
// Redacted. An unset secret keeps its empty value: whether a secret is
// configured is itself an operational answer, and it discloses nothing.
func newSetting(key, value string, secret bool) Setting {
	if secret && value != "" {
		value = Redacted
	}
	return Setting{Key: key, Value: value, Secret: secret}
}

// fieldKey reads a field's configuration key from its mapstructure tag,
// reporting whether the field is squashed into its parent or skipped.
func fieldKey(field reflect.StructField) (name string, squash, skip bool) {
	tag, tagged := field.Tag.Lookup("mapstructure")

	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "squash" {
			squash = true
		}
	}

	if tagged && name == "-" {
		return "", false, true
	}

	// An untagged embedded struct is squashed: viper's decoder sets
	// Squash, which is why the timberjack logger's keys sit directly under
	// `logging:` and CertificateInfo's under `http.tls:` rather than under
	// a level named for the embedded type.
	if name == "" && field.Anonymous {
		return "", true, false
	}

	if name == "" {
		// Untagged and named: mapstructure matches such a field
		// case-insensitively against its own name, so the lowercased name
		// is the key an operator would write.
		return strings.ToLower(field.Name), squash, false
	}

	return name, squash, false
}

// isSecret reports whether a field is tagged as holding a secret.
func isSecret(field reflect.StructField) bool {
	return field.Tag.Get("secret") == "true"
}

// joinKey appends one segment to a dotted key. The root has no prefix, so
// its own fields are not preceded by a dot.
func joinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// sortedKeys orders a map's keys by their rendered text, so the same
// configuration produces the same view on every request.
func sortedKeys(rv reflect.Value) []reflect.Value {
	keys := rv.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface())
	})
	return keys
}
