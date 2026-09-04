package service

// Test methodology: the code alphabet and its normalization are pure
// functions over strings, so these are plain table-driven tests with no
// database or service involved. What they pin is the contract the console
// and the web UI both depend on — that what a human reads off a screen and
// what they type into a box land on the same eight characters.

import (
	"strings"
	"testing"
)

func TestNewUserCode_ShouldProduceEightAlphabetSymbols(t *testing.T) {
	t.Parallel()

	code, err := newUserCode()
	if err != nil {
		t.Fatalf("unexpected error minting a code: %v", err)
	}

	if len(code) != userCodeLength {
		t.Errorf("code %q is %d characters, want %d", code, len(code), userCodeLength)
	}
	for _, r := range code {
		if !strings.ContainsRune(userCodeAlphabet, r) {
			t.Errorf("code %q contains %q, which is outside the alphabet %q", code, string(r), userCodeAlphabet)
		}
	}
}

// A code that always came back the same would still pass the shape test
// above, so this checks that the generator actually varies. Not a
// randomness test: 40 bits means a repeat across a handful of draws would
// mean the entropy source is not being consulted at all.
func TestNewUserCode_ShouldNotRepeatAcrossDraws(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 16)
	for range 16 {
		code, err := newUserCode()
		if err != nil {
			t.Fatalf("unexpected error minting a code: %v", err)
		}
		if seen[code] {
			t.Fatalf("code %q was minted twice in 16 draws", code)
		}
		seen[code] = true
	}
}

// The alphabet is what makes a code readable off a serial console, so its
// exclusions are a contract rather than an implementation detail.
func TestUserCodeAlphabet_ShouldOmitTheAmbiguousLetters(t *testing.T) {
	t.Parallel()

	if got := len(userCodeAlphabet); got != 32 {
		t.Errorf("alphabet is %d symbols, want 32 (five bits each)", got)
	}
	for _, excluded := range []string{"I", "L", "O", "U"} {
		if strings.Contains(userCodeAlphabet, excluded) {
			t.Errorf("alphabet contains %q, which Crockford Base32 excludes", excluded)
		}
	}
}

func TestFormatUserCode_ShouldGroupForDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want string
	}{
		{name: "a full code splits into two groups", code: "K7M4QP2X", want: "K7M4-QP2X"},
		{name: "a short code is left alone", code: "K7M", want: "K7M"},
		{name: "exactly one group is left alone", code: "K7M4", want: "K7M4"},
		{name: "one symbol past a group starts the next", code: "K7M4Q", want: "K7M4-Q"},
		{name: "empty stays empty", code: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatUserCode(tt.code); got != tt.want {
				t.Errorf("FormatUserCode(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// Round trip: whatever the generator produces has to survive being
// displayed and typed back in.
func TestNormalizeUserCode_ShouldRoundTripAMintedCode(t *testing.T) {
	t.Parallel()

	code, err := newUserCode()
	if err != nil {
		t.Fatalf("unexpected error minting a code: %v", err)
	}

	got, err := NormalizeUserCode(FormatUserCode(code))
	if err != nil {
		t.Fatalf("unexpected error normalizing %q: %v", FormatUserCode(code), err)
	}
	if got != code {
		t.Errorf("round trip of %q gave %q", code, got)
	}
}

func TestNormalizeUserCode_ShouldAcceptWhatAHumanPlausiblyTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "the display form", input: "K7M4-QP2X", want: "K7M4QP2X"},
		{name: "no separator", input: "K7M4QP2X", want: "K7M4QP2X"},
		{name: "lower case", input: "k7m4-qp2x", want: "K7M4QP2X"},
		{name: "spaces instead of the hyphen", input: "K7M4 QP2X", want: "K7M4QP2X"},
		{name: "spaces around the hyphen", input: " K7M4 - QP2X ", want: "K7M4QP2X"},
		{name: "a tab from a paste", input: "K7M4\tQP2X", want: "K7M4QP2X"},
		{name: "upper I read as the digit one", input: "K7M4QP2I", want: "K7M4QP21"},
		{name: "lower l read as the digit one", input: "k7m4qp2l", want: "K7M4QP21"},
		{name: "upper O read as the digit zero", input: "K7M4QP2O", want: "K7M4QP20"},
		{name: "lower o read as the digit zero", input: "k7m4qp2o", want: "K7M4QP20"},
		{name: "every alias at once", input: "IlO4-qp2x", want: "1104QP2X"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeUserCode(tt.input)
			if err != nil {
				t.Fatalf("NormalizeUserCode(%q) returned an unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeUserCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeUserCode_ShouldRejectWhatCannotBeACode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "only separators", input: "----"},
		{name: "too short", input: "K7M4QP2"},
		{name: "too long", input: "K7M4QP2XA"},
		{name: "a letter the alphabet excludes and does not alias", input: "K7M4QP2U"},
		{name: "punctuation", input: "K7M4QP2!"},
		{name: "a non-ASCII lookalike", input: "K7M4QP2Х"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := NormalizeUserCode(tt.input); err == nil {
				t.Errorf("NormalizeUserCode(%q) = %q, want an error", tt.input, got)
			}
		})
	}
}
