package errs

import (
	"errors"
	"testing"
)

func TestNotImplementedErrorError(t *testing.T) {
	tests := []struct {
		name string
		what string
		want string
	}{
		{name: "should include What when set", what: "ssh login", want: "ssh login: not implemented"},
		{name: "should fall back to a bare message when What is empty", what: "", want: "not implemented"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &NotImplementedError{What: tt.what}
			if got := err.Error(); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNotImplementedErrorIs(t *testing.T) {
	tests := []struct {
		name   string
		target error
		want   bool
	}{
		{
			name:   "should match another NotImplementedError regardless of What",
			target: &NotImplementedError{What: "something else"},
			want:   true,
		},
		{
			name:   "should not match an unrelated error",
			target: errors.New("some other error"),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &NotImplementedError{What: "ca"}
			if got := errors.Is(err, tt.target); got != tt.want {
				t.Fatalf("expected errors.Is to return %v, got %v", tt.want, got)
			}
		})
	}
}
