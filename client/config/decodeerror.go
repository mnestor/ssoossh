package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
)

// describeDecodeError rewords a failure to decode the merged configuration
// into Config. mapstructure reports it as "decoding failed due to the
// following error(s):", a blank line, then one line per setting in Go's own
// type names ("expected type 'string', got unconvertible type
// '[]interface {}'"). This names each setting and says what kind of value it
// takes. An error it does not recognise is returned with a plain prefix.
func describeDecodeError(err error) error {
	var problems []string
	for _, decodeErr := range collectDecodeErrors(err) {
		problems = append(problems, describeSettingError(decodeErr))
	}
	if len(problems) == 0 {
		return fmt.Errorf("read configuration: %w", err)
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
}

// collectDecodeErrors finds every *mapstructure.DecodeError in err's tree,
// which mapstructure builds with errors.Join.
func collectDecodeErrors(err error) []*mapstructure.DecodeError {
	if decodeErr, ok := err.(*mapstructure.DecodeError); ok { //nolint:errorlint // the tree is walked by hand below.
		return []*mapstructure.DecodeError{decodeErr}
	}

	var found []*mapstructure.DecodeError
	switch wrapped := err.(type) { //nolint:errorlint // the tree is walked by hand.
	case interface{ Unwrap() []error }:
		for _, e := range wrapped.Unwrap() {
			found = append(found, collectDecodeErrors(e)...)
		}
	case interface{ Unwrap() error }:
		found = append(found, collectDecodeErrors(wrapped.Unwrap())...)
	}
	return found
}

// describeSettingError words one setting's decode failure.
func describeSettingError(decodeErr *mapstructure.DecodeError) string {
	var unconvertible *mapstructure.UnconvertibleTypeError
	if errors.As(decodeErr, &unconvertible) {
		return fmt.Sprintf("setting %q takes %s, not %s",
			decodeErr.Name(), describeType(unconvertible.Expected.Type()), describeValue(unconvertible.Value))
	}
	var parseErr *mapstructure.ParseError
	if errors.As(decodeErr, &parseErr) {
		return fmt.Sprintf("setting %q takes %s, and %v is not one",
			decodeErr.Name(), describeType(parseErr.Expected.Type()), parseErr.Value)
	}
	// The duration hook's failure arrives as time.ParseDuration's error,
	// which mapstructure rewraps without the value; its "time:" prefix is
	// the only sign of which setting type it was.
	inner := errors.Unwrap(decodeErr)
	if inner != nil && strings.HasPrefix(inner.Error(), "time: ") {
		return fmt.Sprintf("setting %q takes %s", decodeErr.Name(), describeType(reflect.TypeFor[time.Duration]()))
	}
	return fmt.Sprintf("setting %q: %v", decodeErr.Name(), inner)
}

// describeType names a Config field's type the way a YAML author thinks of
// it.
func describeType(t reflect.Type) string {
	if t == reflect.TypeFor[time.Duration]() {
		return "a duration such as 30s or 2m"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "a mapping of settings"
	case reflect.Pointer:
		return describeType(t.Elem())
	default:
		return t.String()
	}
}

// describeValue names the kind of value a YAML author actually wrote.
func describeValue(v any) string {
	if v == nil {
		return "an empty value"
	}
	switch reflect.TypeOf(v).Kind() { //nolint:exhaustive // the rest fall to the default.
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map:
		return "a mapping"
	case reflect.String:
		return fmt.Sprintf("the string %q", v)
	case reflect.Bool:
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
