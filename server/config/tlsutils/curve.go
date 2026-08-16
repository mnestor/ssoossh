package tlsutils

import (
	"crypto/tls"
	"fmt"
	"strings"
)

// curveIDs is the source of truth for supported curves, mapping each ID to
// its normalized name (what remains after curveNameTokenStripper): add a new
// tls.CurveID constant here and curveFromName picks it up automatically.
var curveIDs = map[tls.CurveID]string{
	tls.CurveP256:      "256",
	tls.CurveP384:      "384",
	tls.CurveP521:      "521",
	tls.X25519:         "x25519",
	tls.X25519MLKEM768: "x25519mlkem768",
}

// curveIDByDigits is the reverse lookup, built once from curveIDs.
var curveIDByDigits = func() map[string]tls.CurveID {
	m := make(map[string]tls.CurveID, len(curveIDs))
	for id, digits := range curveIDs {
		m[digits] = id
	}
	return m
}()

// curveNameTokenStripper removes the known prefix words and separators
// from a curve name, in any case, leaving only what should be a bare digit
// code (e.g. "256") if the input is well-formed. Anything left over after
// this — any unrecognized word or symbol — makes the lookup in
// curveFromName fail, so input that merely contains the right digits
// somewhere isn't silently accepted.
var curveNameTokenStripper = strings.NewReplacer(
	"curve", "",
	"p", "",
	"-", "",
	"_", "",
	" ", "",
)

// curveFromName resolves a curve name (e.g. "P256", "p256", "CurveP256",
// "P-256") into a tls.CurveID. It tolerates case and the "curve"/"p" prefix
// and "-"/"_"/" " separators, but requires everything else in n to reduce
// to exactly one of the known curve digit codes — unrecognized text is
// rejected rather than ignored.
func curveFromName(n string) (tls.CurveID, error) {
	normalized := curveNameTokenStripper.Replace(strings.ToLower(n))

	if id, ok := curveIDByDigits[normalized]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("unknown curve: %s should be one of (CurveP256, CurveP384, CurveP521, X25519, X25519MLKEM768)", n)
}

// Curve resolves names into the tls.CurveID values used by
// tls.Config.CurvePreferences, deduplicating while preserving the first
// occurrence's order. An empty or nil list resolves to nil, leaving
// tls.Config.CurvePreferences unset so Go's defaults apply.
func Curve(names []string) ([]tls.CurveID, error) {
	if len(names) == 0 {
		return nil, nil
	}

	curves := make([]tls.CurveID, 0, len(names))
	seen := make(map[tls.CurveID]bool, len(names))
	for _, name := range names {
		n, err := curveFromName(name)
		if err != nil {
			return nil, err
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		curves = append(curves, n)
	}

	return curves, nil
}
