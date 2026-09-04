package service

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// The console user code: what a machine with no browser in front of it puts
// on screen for a human to read aloud to themselves and type into the web
// UI. See docs/proposals/console-login-pam.md.
//
// It is a lookup key for an already-authenticated approver, never a
// capability. Resolving one requires a session; an unauthenticated caller
// can never turn a code into a request ID, and the request ID is what
// yields the certificate.

const (
	// userCodeAlphabet is Crockford Base32: the digits and the upper-case
	// letters minus I, L, O and U. I/L/O are dropped because a human
	// reading a console font confuses them with 1 and 0 (and the decoder
	// below accepts them as such anyway); U is dropped so no generated
	// code spells anything unfortunate, which also leaves V unambiguous.
	userCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	// userCodeLength is 8 symbols, so 40 bits.
	//
	// RFC 8628 accepts around 20 bits for a device code because the
	// attacker there is guessing at their own pending authorization and
	// the rate limiter carries the rest. The guessing risk here is worse:
	// a hit resolves a stranger's console login and hands the guesser the
	// approval page for it. 40 bits keeps the margin regardless of how the
	// limiter is configured, and eight symbols is still comfortably
	// typeable.
	userCodeLength = 8

	// userCodeGroupSize is how the code is grouped for display —
	// "K7M4-QP2X" rather than "K7M4QP2X". Only ever a display concern:
	// what is stored, compared and indexed is the normalized form, which
	// carries no separator.
	userCodeGroupSize = 4
)

// newUserCode returns a fresh code in its normalized (separator-free) form.
//
// Rejection sampling is not needed: the alphabet is exactly 32 symbols, so
// five bits map onto it without bias, and 8 symbols consume 40 bits — five
// whole bytes with nothing left over.
func newUserCode() (string, error) {
	raw := make([]byte, userCodeLength*5/8)
	if _, err := rand.Read(raw); err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return "", fmt.Errorf("failed to generate a console user code: %w", err)
	}

	// Accumulate the bytes into a 40-bit integer and peel five bits off
	// the top at a time. Clearer than tracking a bit cursor across the
	// byte boundaries, and 40 bits fits a uint64 with room to spare.
	var acc uint64
	for _, b := range raw {
		acc = acc<<8 | uint64(b)
	}

	var out strings.Builder
	out.Grow(userCodeLength)
	for i := userCodeLength - 1; i >= 0; i-- {
		out.WriteByte(userCodeAlphabet[(acc>>(uint(i)*5))&0x1f])
	}
	return out.String(), nil
}

// FormatUserCode groups code for display: "K7M4QP2X" becomes "K7M4-QP2X".
// The grouping is what a human reads off a console screen; nothing ever
// stores or compares this form.
func FormatUserCode(code string) string {
	if len(code) <= userCodeGroupSize {
		return code
	}
	var out strings.Builder
	out.Grow(len(code) + len(code)/userCodeGroupSize)
	for i, r := range code {
		if i > 0 && i%userCodeGroupSize == 0 {
			out.WriteByte('-')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// NormalizeUserCode maps what a human typed onto the stored form, or
// reports why it cannot be one.
//
// Everything a person plausibly does to a code on the way from a screen to
// a text box is absorbed here: lower case, the separator typed or omitted,
// spaces, and Crockford's own decoding aliases (I, i, l and L all mean 1;
// O and o mean 0). What is left must be exactly userCodeLength symbols of
// the alphabet.
//
// The error says what is wrong with the input's shape and never whether a
// well-formed code exists — that distinction belongs to the resolver, which
// runs behind a session.
func NormalizeUserCode(input string) (string, error) {
	var out strings.Builder
	out.Grow(userCodeLength)

	for _, r := range strings.ToUpper(strings.TrimSpace(input)) {
		switch {
		// The separators a human introduces: the hyphen the display form
		// carries, plus whitespace from a copy/paste. Dropped rather than
		// rejected, so "K7M4 - QP2X" is the same code as "k7m4qp2x".
		case r == '-' || r == ' ' || r == '\t':
			continue
		// Crockford's decoding aliases. A console font that renders 1 and
		// I alike is exactly why the alphabet omits them, so accepting the
		// letter a reader saw is the point, not a leniency.
		case r == 'I' || r == 'L':
			out.WriteByte('1')
		case r == 'O':
			out.WriteByte('0')
		case strings.ContainsRune(userCodeAlphabet, r):
			out.WriteRune(r)
		default:
			return "", fmt.Errorf("%q is not a character a code can contain", string(r))
		}
	}

	if out.Len() != userCodeLength {
		return "", fmt.Errorf("a code is %d characters, this one is %d", userCodeLength, out.Len())
	}
	return out.String(), nil
}
