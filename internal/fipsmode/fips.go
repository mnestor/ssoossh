package fipsmode

import "crypto/fips140"

// Enabled reports whether FIPS steering is in effect.
//
// An explicit setting always wins. When it's unset, this follows the Go
// runtime: a binary built and running in FIPS 140-3 mode shouldn't
// generate or accept a key it can't itself use, and inferring that saves
// operators from having to say so twice.
func Enabled(explicit *bool) bool {
	if explicit != nil {
		return *explicit
	}
	return fips140.Enabled()
}
