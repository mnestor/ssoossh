// Package secheaders holds the security response header values ssoosshd
// sets. The middleware emits them and the admin diagnostics grade the
// public URL's headers against them, so both read one definition and cannot
// drift apart. It is a leaf package: it imports nothing from this module.
package secheaders

import "strings"

// ReferrerPolicy is the Referrer-Policy value set on every response.
const ReferrerPolicy = "strict-origin-when-cross-origin"

// ContentTypeOptions is the X-Content-Type-Options value set on every response.
const ContentTypeOptions = "nosniff"

// FrameOptions is the X-Frame-Options value set on every response.
const FrameOptions = "DENY"

// ContentSecurityPolicy returns the Content-Security-Policy set on every
// response. The script-src allows only the given per-request nonce.
func ContentSecurityPolicy(nonce string) string {
	return "default-src 'self'; " +
		"base-uri 'self'; " +
		"object-src 'none'; " +
		"frame-ancestors 'none'; " +
		"form-action 'self'; " +
		"img-src 'self'; " +
		"font-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"script-src 'self' 'nonce-" + nonce + "'"
}

// IsNonceSource reports whether a CSP source expression is a nonce source
// such as 'nonce-abc'. The nonce differs per request, so callers match the
// prefix rather than the value.
func IsNonceSource(source string) bool {
	return strings.HasPrefix(source, "'nonce-")
}
