package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The rotation keys are read off timberjack.Logger rather than listed here,
// so this pins the shape of what that produces: the names an operator
// writes, in declaration order, with the deprecated one left out.
func TestEmbeddedKeys_ShouldNameTheRotationKeysInDeclarationOrder(t *testing.T) {
	t.Parallel()

	keys, err := embeddedKeys()
	if err != nil {
		t.Fatalf("failed to read the embedded keys: %v", err)
	}

	got := keys["Logger"]
	want := []string{
		"filename", "maxsize", "maxage", "maxbackups", "localtime",
		"compression", "rotationinterval", "backuptimeformat",
		"rotateatminutes", "rotateat", "appendtimeafterext", "filemode",
	}

	var names []string
	for _, k := range got {
		names = append(names, k.Key)
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", names, want)
	}
}

// The doc comment on each embedded field names the same keys in the same
// order, and it is what an operator reads above them. A key added or
// renamed upstream reaches defaults.yaml on its own; the prose does not,
// so this is what says the two have drifted.
func TestEmbeddedKeys_ShouldMatchTheProseAboveThem(t *testing.T) {
	t.Parallel()

	keys, err := embeddedKeys()
	if err != nil {
		t.Fatalf("failed to read the embedded keys: %v", err)
	}

	src, err := os.ReadFile("../../../server/config/types_logging.go")
	if err != nil {
		t.Fatalf("failed to read the logging types: %v", err)
	}

	for _, k := range keys["Logger"] {
		if !strings.Contains(string(src), k.Key) {
			t.Errorf("server/config/types_logging.go never mentions the rotation key %q", k.Key)
		}
	}
}

// A placeholder of the wrong type is a line an operator uncomments and the
// server then refuses, so the mapping covers every type the config can hold
// and errors rather than guessing at one it cannot.
func TestReflectType_ShouldMapEveryConfigFacingType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "should map a string", in: "", want: "string"},
		{name: "should map a bool", in: false, want: "bool"},
		{name: "should map an int", in: 0, want: "int"},
		{name: "should map a file mode as an int", in: os.FileMode(0), want: "int"},
		{name: "should map a float as a number", in: 0.0, want: "number"},
		{name: "should map a duration by type, not by kind", in: time.Duration(0), want: "duration"},
		{name: "should map a slice as a list", in: []string{}, want: "list"},
		{name: "should map a map", in: map[string]string{}, want: "map"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := reflectType(reflect.TypeOf(tt.in))
			if err != nil {
				t.Fatalf("failed to map %T: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReflectType_ShouldRefuseATypeItCannotRender(t *testing.T) {
	t.Parallel()

	if _, err := reflectType(reflect.TypeOf(struct{}{})); err == nil {
		t.Error("expected an unhandled type to be an error rather than a guess")
	}
}

// The unexported machinery on timberjack.Logger is not configuration, and a
// key an operator cannot set has no business in the shipped file.
func TestReflectKeys_ShouldSkipUnexportedAndSkippedFields(t *testing.T) {
	t.Parallel()

	type sample struct {
		Kept    string
		Dropped string
		hidden  string //nolint:unused // present so the walk has an unexported field to skip
	}

	got, err := reflectKeys(reflect.TypeOf(sample{}), map[string]bool{"Dropped": true})
	if err != nil {
		t.Fatalf("failed to read the keys: %v", err)
	}
	if len(got) != 1 || got[0].Key != "kept" {
		t.Errorf("got %v, want just kept", got)
	}
}
