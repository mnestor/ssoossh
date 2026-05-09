package middleware

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/gin-gonic/gin"
)

// GetCSPNonce returns the CSP nonce generated for this request, if any.
func GetCSPNonce(c *gin.Context) string {
	if v, ok := c.Get("csp_nonce"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func CspMiddleware(c *gin.Context) {
	// Generate a random base64 nonce for this request
	nonce := generateNonce()
	c.Set("csp_nonce", nonce)

	csp := "default-src 'self'; " +
		"base-uri 'self'; " +
		"object-src 'none'; " +
		"frame-ancestors 'none'; " +
		"form-action 'self'; " +
		"img-src * blob:;" +
		"font-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"script-src 'self' 'nonce-" + nonce + "'"

	c.Writer.Header().Set("Content-Security-Policy", csp)
	c.Next()
}

func generateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "" // if generation fails, return empty; policy will omit nonce
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
