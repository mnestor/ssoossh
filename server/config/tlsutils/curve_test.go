package tlsutils

// Test methodology: Table-driven tests with t.Parallel() for parallelization.
// Tests verify TLS elliptic curve name resolution with tolerant parsing
// (e.g. "P256", "p256", "CurveP256" all work). Each test verifies one
// parsing behavior or edge case.

import (
	"crypto/tls"
	"testing"
)

func TestCurve_ShouldResolveKnownNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  tls.CurveID
	}{
		{"should resolve CurveP256 when given full name", "CurveP256", tls.CurveP256},
		{"should resolve CurveP384 when given full name", "CurveP384", tls.CurveP384},
		{"should resolve CurveP521 when given full name", "CurveP521", tls.CurveP521},
		{"should resolve P256 when given short name", "P256", tls.CurveP256},
		{"should resolve P384 when given short name", "P384", tls.CurveP384},
		{"should resolve P521 when given short name", "P521", tls.CurveP521},
		{"should resolve X25519 when given full name", "X25519", tls.X25519},
		{"should resolve X25519 when given lowercase name", "x25519", tls.X25519},
		{"should resolve X25519MLKEM768 when given full name", "X25519MLKEM768", tls.X25519MLKEM768},
		{"should resolve X25519MLKEM768 when given hyphenated name", "X25519-MLKEM768", tls.X25519MLKEM768},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			curves, err := Curve([]string{tt.input})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(curves) != 1 || curves[0] != tt.want {
				t.Errorf("got %v, want [%v]", curves, tt.want)
			}
		})
	}
}

func TestCurve_ShouldIgnoreCaseAndNonDigitCharacters(t *testing.T) {
	t.Parallel()

	curves, err := Curve([]string{"curvep256", "CURVEP384", "CuRvEp521"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := []tls.CurveID{tls.CurveP256, tls.CurveP384, tls.CurveP521}
	if len(curves) != len(want) {
		t.Fatalf("got %d curves, want %d", len(curves), len(want))
	}
	for i := range want {
		if curves[i] != want[i] {
			t.Errorf("index %d: got %v, want %v", i, curves[i], want[i])
		}
	}
}

func TestCurve_ShouldDedupeWhilePreservingFirstOccurrenceOrder(t *testing.T) {
	t.Parallel()

	curves, err := Curve([]string{"P384", "P256", "CurveP384", "521", "p256"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := []tls.CurveID{tls.CurveP384, tls.CurveP256, tls.CurveP521}
	if len(curves) != len(want) {
		t.Fatalf("got %d curves, want %d: %v", len(curves), len(want), curves)
	}
	for i := range want {
		if curves[i] != want[i] {
			t.Errorf("index %d: got %v, want %v", i, curves[i], want[i])
		}
	}
}

func TestCurve_ShouldErrorWhenNameUnknown(t *testing.T) {
	t.Parallel()

	_, err := Curve([]string{"CurveP999"})
	if err == nil {
		t.Fatal("expected an error for unknown curve name, got nil")
	}
}

func TestCurve_ShouldReturnNilWhenInputEmpty(t *testing.T) {
	t.Parallel()

	// Nil leaves tls.Config.CurvePreferences unset so Go's defaults apply.
	curves, err := Curve([]string{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if curves != nil {
		t.Errorf("expected nil, got %v", curves)
	}
}

func TestCurve_ShouldErrorWhenUnrecognizedTextSurroundsValidDigits(t *testing.T) {
	t.Parallel()

	// Only known tokens ("curve", "p", and separators) are stripped, so
	// unrelated words containing a valid curve's digits must not be
	// accepted as if they named that curve.
	tests := []string{
		"downgrade-p-256-insecure",
		"notacurve256",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := Curve([]string{input})
			if err == nil {
				t.Fatalf("expected an error for %q, got nil", input)
			}
		})
	}
}

func TestCurve_ShouldResolveNISTHyphenatedNames(t *testing.T) {
	t.Parallel()

	curves, err := Curve([]string{"P-256", "P-384", "P-521"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := []tls.CurveID{tls.CurveP256, tls.CurveP384, tls.CurveP521}
	if len(curves) != len(want) {
		t.Fatalf("got %d curves, want %d", len(curves), len(want))
	}
	for i := range want {
		if curves[i] != want[i] {
			t.Errorf("index %d: got %v, want %v", i, curves[i], want[i])
		}
	}
}

func TestCurve_ShouldReturnNilWhenInputNil(t *testing.T) {
	t.Parallel()

	curves, err := Curve(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if curves != nil {
		t.Errorf("expected nil, got %v", curves)
	}
}
