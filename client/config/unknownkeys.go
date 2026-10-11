package config

import (
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"github.com/spf13/viper"
)

// lockedOnlySetting is the one setting Config never decodes from the merged
// chain: only the enforce file and platform policy may set it.
const lockedOnlySetting = "forbidden_certificate_extensions"

// warnUnknownSettings warns about each setting in the config file at path
// that the client does not read. A misspelt key ("serverr") was silently
// ignored, so the setting the author meant simply did not apply.
// forbidden_certificate_extensions is known everywhere but read only from
// the enforce file, so elsewhere it gets a warning of its own.
func warnUnknownSettings(path string, keys []string, isEnforceFile bool) {
	known := knownSettings()
	for _, key := range keys {
		switch {
		case key == lockedOnlySetting && !isEnforceFile:
			slog.Warn(fmt.Sprintf("%s in %s is ignored: it is read only from the enforce file or platform policy",
				lockedOnlySetting, path))
		case key == lockedOnlySetting || known[key]:
		default:
			msg := fmt.Sprintf("unknown setting %q in %s is ignored", key, path)
			if suggestion := closestSetting(key, known); suggestion != "" {
				msg += fmt.Sprintf("; did you mean %q?", suggestion)
			}
			slog.Warn(msg)
		}
	}
}

// fileSettings returns the settings the YAML file at path sets, flattened
// the way viper names them, or nil if it cannot be read.
func fileSettings(path string) []string {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil
	}
	return v.AllKeys()
}

// knownSettings returns every setting key Config decodes, flattened the way
// viper names them ("sshkey.type"), plus the names of the nested sections.
func knownSettings() map[string]bool {
	known := map[string]bool{}
	addSettings(known, reflect.TypeFor[Config](), "")
	return known
}

// addSettings adds t's mapstructure keys under prefix to known, recursing
// into nested sections.
func addSettings(known map[string]bool, t reflect.Type, prefix string) {
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}
		key := prefix + tag
		known[key] = true
		if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeFor[time.Duration]() {
			addSettings(known, field.Type, key+".")
		}
	}
}

// closestSetting returns the known setting nearest to key by edit distance,
// if one is within two edits, for a "did you mean" hint.
func closestSetting(key string, known map[string]bool) string {
	candidates := make([]string, 0, len(known))
	for k := range known {
		candidates = append(candidates, k)
	}
	slices.Sort(candidates)

	best, bestDistance := "", 3
	for _, candidate := range candidates {
		if d := editDistance(key, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}
