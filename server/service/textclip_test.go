package service

import (
	"testing"
	"unicode/utf8"
)

func TestClipUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{name: "should keep a string when it fits", in: "short", n: 10, want: "short"},
		{name: "should keep an empty string when it is empty", in: "", n: 5, want: ""},
		{name: "should cut ASCII at n when it is too long", in: "abcdef", n: 3, want: "abc"},
		{name: "should keep a string when it is exactly n bytes", in: "abc", n: 3, want: "abc"},
		{name: "should back off when the cut splits a two-byte character", in: "abécd", n: 3, want: "ab"},
		{name: "should back off when the cut splits a four-byte character", in: "a\U0001F600b", n: 3, want: "a"},
		{name: "should keep a multibyte character when the cut falls after it", in: "éé", n: 2, want: "é"},
		{name: "should drop invalid bytes when the input carries them", in: "a\xffb", n: 10, want: "ab"},
		{name: "should return empty when n is zero", in: "abc", n: 0, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clipUTF8(tt.in, tt.n)
			if got != tt.want || !utf8.ValidString(got) || len(got) > max(tt.n, 0) {
				t.Errorf("clipUTF8(%q, %d) = %q, want %q (valid UTF-8, at most n bytes)", tt.in, tt.n, got, tt.want)
			}
		})
	}
}
