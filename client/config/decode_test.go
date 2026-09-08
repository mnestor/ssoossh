package config

import (
	"reflect"
	"testing"
)

// capubkey was a single string before a CA rotation needed two, so every
// shape that used to work has to keep working, and the sequence has to work
// as well.
func TestSplitKeyList(t *testing.T) {
	t.Parallel()

	const keyOne = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca-one@example"
	const keyTwo = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINlbdxKlyGGaGRLcaOWWJcRJUdcVJEIvA0SBCVSbdcxJ ca-two@example"

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "should return nothing for an empty string", in: "", want: []string{}},
		{name: "should return nothing for whitespace alone", in: "  \n\t\n", want: []string{}},
		{name: "should return one key unchanged", in: keyOne, want: []string{keyOne}},
		{
			name: "should split a multi-line block into one key per line",
			in:   keyOne + "\n" + keyTwo,
			want: []string{keyOne, keyTwo},
		},
		{
			name: "should drop blank lines between keys",
			in:   keyOne + "\n\n" + keyTwo + "\n",
			want: []string{keyOne, keyTwo},
		},
		{
			name: "should trim the carriage returns a Windows-edited file leaves behind",
			in:   keyOne + "\r\n" + keyTwo,
			want: []string{keyOne, keyTwo},
		},
		{
			// The reason this splits on newlines and not commas: a comma is
			// ordinary inside an SSH key's comment, and viper's default hook
			// would cut this key in half.
			name: "should keep a key whose comment contains a comma intact",
			in:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca@example, prod",
			want: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca@example, prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SplitKeyList(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

// The hook must leave everything that is not a string-to-[]string
// conversion alone, or it would quietly rewrite unrelated settings.
func TestStringToKeyListHook_ShouldOnlyConvertStringsToStringSlices(t *testing.T) {
	t.Parallel()

	hook := stringToKeyListHook()

	tests := []struct {
		name string
		from any
		to   any
		want any
	}{
		{name: "should convert a string bound for a string slice", from: "a\nb", to: []string{}, want: []string{"a", "b"}},
		{name: "should leave a string bound for a string alone", from: "a\nb", to: "", want: "a\nb"},
		{name: "should leave a string bound for an int alone", from: "7", to: 0, want: "7"},
		{name: "should leave a non-string alone", from: 7, to: []string{}, want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := hook(reflect.TypeOf(tt.from), reflect.TypeOf(tt.to), tt.from)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
