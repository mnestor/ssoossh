//go:build e2e || resilience || load

package harness

import "testing"

// TestTailLines pins how much of the browser console a failure quotes in the
// test log: the last n lines, in order, with no trailing newline, and a
// placeholder rather than a blank line when the console was empty.
func TestTailLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{name: "should say (empty) when the console is empty", in: "", n: 3, want: "(empty)"},
		{name: "should say (empty) when the console holds only newlines", in: "\n\n", n: 3, want: "(empty)"},
		{name: "should keep every line when there are fewer than n", in: "a\nb\n", n: 3, want: "a\nb"},
		{name: "should keep every line when there are exactly n", in: "a\nb\nc", n: 3, want: "a\nb\nc"},
		{name: "should keep the last n lines when there are more", in: "a\nb\nc\nd\n", n: 2, want: "c\nd"},
		{name: "should keep a blank line inside the tail", in: "a\n\nb\n", n: 2, want: "\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tailLines(tt.in, tt.n); got != tt.want {
				t.Errorf("tailLines(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}
