package config

import (
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// stringToKeyListHook lets a []string setting be written as a plain scalar
// as well as a YAML sequence.
//
// It exists for capubkey, which was a single string before a CA rotation
// needed two, and every configuration file and Windows registry value in
// the field still holds one. Splitting on newlines rather than commas is
// deliberate: newline-separated authorized_keys lines are the form
// /api/ca returns and the form `parseCAPublicKeys` already reads, whereas
// a comma is perfectly ordinary inside an SSH key's comment field and
// would split a single key into two unparseable halves.
//
// viper's default hook chain includes StringToSliceHookFunc(","), which
// would do exactly that, so this runs ahead of it and consumes the
// conversion first.
func stringToKeyListHook() mapstructure.DecodeHookFuncType {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to != reflect.TypeOf([]string{}) {
			return data, nil
		}

		s, ok := data.(string)
		if !ok {
			// not covered: from.Kind() is reflect.String, so the assertion
			// holds for every value that reaches here.
			return data, nil
		}

		return SplitKeyList(s), nil
	}
}

// SplitKeyList turns newline-separated authorized_keys text into one entry
// per key, dropping blank lines. A string holding no keys becomes an empty
// slice rather than a slice holding one empty string, so "is anything
// configured" stays a length check everywhere else.
//
// Exported because /api/ca's response has the same shape and the client
// splits it the same way.
func SplitKeyList(s string) []string {
	lines := strings.Split(s, "\n")
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return keys
}
