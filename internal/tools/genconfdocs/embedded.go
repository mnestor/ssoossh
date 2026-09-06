package main

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/DeRuina/timberjack"

	"github.com/mnestor/ssoossh/internal/tools/confdocs"
)

// deprecatedTimberjackKeys are fields of timberjack.Logger that a new
// configuration should not use. compress is superseded by compression, which
// takes the algorithm rather than a bool, and listing both in the shipped
// file would be an invitation to set the one that is on its way out.
var deprecatedTimberjackKeys = map[string]bool{"Compress": true}

// embeddedKeys names the keys of every third-party struct embedded in the
// config, so defaults.yaml can list them under the group's prose.
//
// The names come from the dependency itself rather than from a list kept
// here. The rotation surface is timberjack's to change, and a list written
// out by hand would go stale silently: the generated file would keep
// offering keys the server no longer accepts, or keep quiet about ones it
// does, and nothing in the build would notice either way.
func embeddedKeys() (map[string][]confdocs.EmbeddedKey, error) {
	rotation, err := reflectKeys(reflect.TypeOf(timberjack.Logger{}), deprecatedTimberjackKeys)
	if err != nil {
		return nil, fmt.Errorf("timberjack.Logger: %w", err)
	}
	return map[string][]confdocs.EmbeddedKey{"Logger": rotation}, nil
}

// reflectKeys reads the exported fields of t in declaration order, skipping
// the names in skip.
//
// The key is the field name lowercased: the embed is squashed into its
// parent with no mapstructure tags of its own, viper lowercases every key it
// reads, and mapstructure then matches the field name case-insensitively.
func reflectKeys(t reflect.Type, skip map[string]bool) ([]confdocs.EmbeddedKey, error) {
	var out []confdocs.EmbeddedKey
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() || skip[f.Name] {
			continue
		}
		kind, err := reflectType(f.Type)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		out = append(out, confdocs.EmbeddedKey{Key: strings.ToLower(f.Name), Type: kind})
	}
	return out, nil
}

// reflectType maps a Go type onto the config-facing type name confdocs uses.
// An unrecognised type is an error rather than a guess, for the same reason
// the AST walk refuses one: a placeholder of the wrong type is a line an
// operator uncomments and the server then rejects.
func reflectType(t reflect.Type) (string, error) {
	if t == reflect.TypeOf(time.Duration(0)) {
		return "duration", nil
	}
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "bool", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int", nil
	case reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Slice, reflect.Array:
		return "list", nil
	case reflect.Map:
		return "map", nil
	default:
		return "", fmt.Errorf("unhandled config field type %s", t)
	}
}
