package service

// Test methodology: Unit tests for extraValue, the scalar-or-list claim
// value stored per configured extra field. Table-driven, parallel. The
// MISSING placeholder contract (empty renders as "MISSING") lives here;
// template-level rendering is covered in keyid_test.go.

import (
	"encoding/json"
	"testing"
)

func TestExtraValueString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    extraValue
		want string
	}{
		{name: "should render MISSING when zero value", v: extraValue{}, want: "MISSING"},
		{name: "should render MISSING when scalar is empty", v: scalarExtra(""), want: "MISSING"},
		{name: "should render the scalar when set", v: scalarExtra("eng"), want: "eng"},
		{name: "should render MISSING when list is empty", v: listExtra(nil), want: "MISSING"},
		{name: "should comma-join a list", v: listExtra([]string{"a", "b"}), want: "a,b"},
		{name: "should render a single-element list without separator", v: listExtra([]string{"a"}), want: "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.v.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtraValueJoin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    extraValue
		sep  string
		want string
	}{
		{name: "should join a list with the separator", v: listExtra([]string{"a", "b", "c"}), sep: ";", want: "a;b;c"},
		{name: "should return the scalar when not a list", v: scalarExtra("eng"), sep: ";", want: "eng"},
		{name: "should render MISSING when empty", v: extraValue{}, sep: ";", want: "MISSING"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.v.Join(tt.sep); got != tt.want {
				t.Errorf("Join(%q) = %q, want %q", tt.sep, got, tt.want)
			}
		})
	}
}

func TestExtraValueJSONRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		v        extraValue
		wantJSON string
	}{
		{name: "should marshal a scalar as a JSON string", v: scalarExtra("eng"), wantJSON: `"eng"`},
		{name: "should marshal a list as a JSON array", v: listExtra([]string{"a", "b"}), wantJSON: `["a","b"]`},
		{name: "should marshal the zero value as an empty string", v: extraValue{}, wantJSON: `""`},
		{name: "should marshal an empty list as an empty array", v: listExtra([]string{}), wantJSON: `[]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatalf("unexpected marshal error: %v", err)
			}
			if string(data) != tt.wantJSON {
				t.Fatalf("Marshal = %s, want %s", data, tt.wantJSON)
			}

			var back extraValue
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if back.String() != tt.v.String() {
				t.Errorf("round-trip String() = %q, want %q", back.String(), tt.v.String())
			}
		})
	}
}

func TestExtraValueUnmarshalShouldErrorOnUnsupportedJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{name: "should error on a number", json: `3`},
		{name: "should error on an object", json: `{"a":1}`},
		{name: "should error on an array of numbers", json: `[1,2]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v extraValue
			if err := json.Unmarshal([]byte(tt.json), &v); err == nil {
				t.Fatalf("expected an error unmarshaling %s, got nil", tt.json)
			}
		})
	}
}
