package middleware

import "github.com/mnestor/ssoossh/server/service"

// AppSecurityHeaders reports the security header values this package's
// middleware sends, for the admin diagnostics to grade the public URL's
// headers against. It reads the same constants and CSP builder the
// middleware uses, so the diagnostics expect exactly what the app emits.
// HSTS is not included: its value is http.hsts, which the diagnostics read
// from config.
func AppSecurityHeaders() service.AppSecurityHeaders {
	return service.AppSecurityHeaders{
		FrameOptions:          xFrameOptions,
		ReferrerPolicy:        referrerPolicy,
		ContentTypeOptions:    xContentTypeOptions,
		ContentSecurityPolicy: contentSecurityPolicy,
	}
}
