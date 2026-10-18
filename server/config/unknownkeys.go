package config

import (
	"log/slog"
	"reflect"
	"sort"
	"strings"
)

// UnknownKey is one key the config file sets that no config struct
// declares, so it has no effect: a typo, or a key from a newer version.
type UnknownKey struct {
	// Key is the dotted path as written, lowercased the way viper reads
	// every key: ldap.fields.groups.atribute.
	Key string

	// Suggestion is the closest key the server does know at the same
	// place, or empty when nothing is close.
	Suggestion string
}

// UnknownKeys returns the keys NewConfig found in the config file that no
// config struct declares, sorted by key. Nil for a configuration built any
// other way.
func (c *Config) UnknownKeys() []UnknownKey {
	return c.unknownKeys
}

// findUnknownKeys turns mapstructure's unused-key metadata into
// UnknownKeys with suggestions.
//
// The metadata is the decoder's own account of what it did not consume,
// so there is no key list to keep in step with the structs. A key under a
// map-typed field (ldap.fields, authentication.fields.extra) is a name the
// operator chose and is never reported; a field misspelt inside such a
// map's struct value (ldap.fields.groups.atribute) is.
func findUnknownKeys(unused []string) []UnknownKey {
	if len(unused) == 0 {
		return nil
	}
	root := reflect.TypeFor[Config]()
	out := make([]UnknownKey, 0, len(unused))
	for _, raw := range unused {
		segments := keySegments(raw)
		out = append(out, UnknownKey{
			Key:        joinSegments(segments),
			Suggestion: suggestKey(root, segments),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// warnUnknownKeys logs one warning per unknown key. A warning rather than
// an error, because a file written for a newer version must still load.
func warnUnknownKeys(log *slog.Logger, file string, keys []UnknownKey) {
	for _, k := range keys {
		attrs := []any{"key", k.Key, "file", file}
		if k.Suggestion != "" {
			attrs = append(attrs, "did_you_mean", k.Suggestion)
		}
		log.Warn("config key is not recognised and has no effect: a typo, or a key from a newer ssoosshd", attrs...)
	}
}

// keySegments splits a mapstructure field name into config path segments:
// "a.b[groups].c[0]" becomes a, b, groups, c, [0]. A map key becomes a
// plain segment, which is how the operator wrote it; a slice index keeps
// its brackets so it stays apart from a key named "0".
func keySegments(name string) []string {
	var out []string
	for _, part := range strings.Split(name, ".") {
		for part != "" {
			open := strings.IndexByte(part, '[')
			if open < 0 {
				out = append(out, part)
				break
			}
			if open > 0 {
				out = append(out, part[:open])
			}
			end := strings.IndexByte(part[open:], ']')
			if end < 0 {
				// not covered: mapstructure always closes what it opens.
				out = append(out, part[open:])
				break
			}
			inner := part[open+1 : open+end]
			if isIndex(inner) {
				out = append(out, "["+inner+"]")
			} else {
				out = append(out, inner)
			}
			part = part[open+end+1:]
		}
	}
	return out
}

// isIndex reports whether s is a slice index: decimal digits only.
func isIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// suggestKey walks segments down from t to the struct holding the last
// one, and returns the full key with the last segment replaced by the
// nearest field name there. Empty when the walk cannot follow the path or
// nothing is close enough to be a likely typo.
func suggestKey(t reflect.Type, segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	parent := t
	for _, seg := range segments[:len(segments)-1] {
		next, ok := childType(parent, seg)
		if !ok {
			return ""
		}
		parent = next
	}
	for parent.Kind() == reflect.Pointer {
		parent = parent.Elem()
	}
	if parent.Kind() != reflect.Struct {
		return ""
	}

	last := segments[len(segments)-1]
	best, bestDist := "", -1
	for _, name := range structKeys(parent) {
		d := editDistance(last, name)
		if bestDist < 0 || d < bestDist {
			best, bestDist = name, d
		}
	}
	// Close means a slip of the keyboard: at most two edits, and fewer
	// than half the word, so "tls" is never offered for "url".
	if best == "" || bestDist > 2 || bestDist*2 >= len(last) {
		return ""
	}
	return joinSegments(append(append([]string{}, segments[:len(segments)-1]...), best))
}

// joinSegments is the inverse of keySegments for display: segments joined
// by dots, with a slice index attached to the key before it, as in
// searches[0].value.
func joinSegments(segments []string) string {
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 && !strings.HasPrefix(seg, "[") {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}

// childType follows one path segment down from t: a struct field by key,
// a map's value for any key, a slice's element for an index.
func childType(t reflect.Type, seg string) (reflect.Type, bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Map:
		return t.Elem(), true
	case reflect.Slice, reflect.Array:
		if strings.HasPrefix(seg, "[") {
			return t.Elem(), true
		}
		return nil, false
	case reflect.Struct:
		return structField(t, seg)
	default:
		return nil, false
	}
}

// structField finds the field whose config key is seg, looking through
// squashed embeds the way the decoder does. Keys compare case-insensitively
// because viper lowercases every key it reads.
func structField(t reflect.Type, seg string) (reflect.Type, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, squash, skip := fieldKey(f)
		if skip {
			continue
		}
		if squash {
			if ft, ok := structField(derefType(f.Type), seg); ok {
				return ft, true
			}
			continue
		}
		if strings.EqualFold(name, seg) {
			return f.Type, true
		}
	}
	return nil, false
}

// structKeys lists the config keys of t's fields, squashed embeds
// flattened into it.
func structKeys(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, squash, skip := fieldKey(f)
		switch {
		case skip:
		case squash:
			out = append(out, structKeys(derefType(f.Type))...)
		default:
			out = append(out, strings.ToLower(name))
		}
	}
	return out
}

// derefType strips pointers from t.
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// editDistance is the optimal string alignment distance between a and b:
// insertions, deletions, substitutions and adjacent transpositions each
// cost one, so "schmea" is one edit from "schema".
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range rb {
		d[0][j+1] = j + 1
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
