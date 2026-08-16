package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CsrfMiddleware rejects state-changing requests that a browser initiated
// from another origin.
//
// The routes it guards authorize certificate issuance and are authenticated
// by the session cookie alone — they take no request body and no token — so
// without this a cross-site form post would be a complete, preflight-free
// CSRF: the attacker creates a certificate request with their own public
// key, lures an authenticated user into submitting the approval, and
// collects a certificate carrying that user's principals.
//
// SameSite=Strict on the session cookie is related but not sufficient. It
// stops cross-*site* requests, and says nothing about a different origin of
// the same site — a second hostname under one registrable domain, which is
// exactly how the homelab deployments this targets are usually laid out.
type CsrfMiddleware struct {
	// allowedOrigin is the origin browsers are expected to send, derived
	// from server config. Empty disables origin matching and falls back to
	// Sec-Fetch-Site alone.
	allowedOrigin string
}

// NewCsrfMiddleware creates a CsrfMiddleware that accepts requests whose
// Origin matches allowedOrigin. Pass "" when the deployment's public origin
// isn't known from config; Sec-Fetch-Site still carries the check on every
// browser that sends it.
func NewCsrfMiddleware(allowedOrigin string) *CsrfMiddleware {
	return &CsrfMiddleware{allowedOrigin: strings.TrimSuffix(allowedOrigin, "/")}
}

// safeMethods never change state, so they are exempt. Note this middleware
// is only registered on state-changing route groups anyway; the check is
// here so that adding a GET to such a group doesn't start failing.
var safeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// Add returns a gin.HandlerFunc that aborts with 403 when a request looks
// cross-origin.
//
// Two signals, in order of trust:
//
//  1. Sec-Fetch-Site, set by the browser itself and not settable by script.
//     "same-origin" and "none" (a user typing the URL, or a bookmark) pass;
//     "cross-site" and "same-site" are rejected — "same-site" deliberately,
//     since a sibling origin is precisely the gap SameSite leaves open.
//  2. Origin, compared against the configured public origin. Only consulted
//     when Sec-Fetch-Site is absent, since it is the weaker signal.
//
// A request with neither header is allowed: that is a non-browser client
// (the ssoossh CLI, curl, a health probe), which cannot be induced to send
// a user's cookies by a hostile web page. CSRF is a browser-ambient-
// authority problem, so a request no browser sent is not in scope.
func (m *CsrfMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		if safeMethods[c.Request.Method] {
			c.Next()
			return
		}

		if site := c.GetHeader("Sec-Fetch-Site"); site != "" {
			if site != "same-origin" && site != "none" {
				m.reject(c, "request originated from another site")
				return
			}
			c.Next()
			return
		}

		if origin := c.GetHeader("Origin"); origin != "" {
			if !m.originAllowed(origin) {
				m.reject(c, "request origin is not allowed")
				return
			}
		}

		c.Next()
	}
}

// originAllowed reports whether origin matches the configured public origin.
// With no configured origin there is nothing to compare against, so this
// cannot judge and defers (Sec-Fetch-Site already had its say).
func (m *CsrfMiddleware) originAllowed(origin string) bool {
	if m.allowedOrigin == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSuffix(origin, "/"), m.allowedOrigin)
}

// reject aborts the request. The response is deliberately terse: the caller
// that triggers this is a hostile page, and telling it which check failed
// only helps it iterate.
func (m *CsrfMiddleware) reject(c *gin.Context, reason string) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": reason})
}
