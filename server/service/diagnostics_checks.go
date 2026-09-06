package service

import (
	"fmt"
	"net"
	"strings"
)

// checkReachability reports whether the server could reach its own public
// URL from where it runs. It is the precondition for the other edge checks:
// if the probe never arrived, header hygiene and CORS could not be tested
// and say so rather than showing a false all-clear.
func (s *DiagnosticsService) checkReachability(origin string, p edgeProbe) DiagnosticCheck {
	c := DiagnosticCheck{ID: "reachability", Title: "Public URL reachability"}

	if origin == "" {
		c.Status = DiagnosticWarn
		c.Summary = "http.public_url is not set, so the edge checks cannot run."
		c.Remediation = "Set http.public_url to the origin browsers use to reach this deployment."
		return c
	}

	if !p.reached {
		c.Status = DiagnosticSkipped
		c.Summary = fmt.Sprintf("Could not reach %s from the server itself.", origin)
		if p.tlsError != "" {
			c.Findings = append(c.Findings, "The public URL's certificate did not validate: "+p.tlsError)
		} else if p.err != nil {
			c.Findings = append(c.Findings, p.err.Error())
		}
		c.Remediation = "This is often split-horizon DNS or an edge the server cannot dial internally, not a fault on its own. Re-run the header and CORS checks from where the edge terminates. If the certificate did not validate, fix the chain the public URL serves."
		return c
	}

	c.Status = DiagnosticOK
	c.Summary = fmt.Sprintf("Reached %s (HTTP %d).", origin, p.status)
	c.Findings = append(c.Findings, "The edge checks below tested this URL through whatever sits in front of the app.")
	return c
}

// checkProxyTrust reasons about how the client IP is resolved, which is what
// rate limiting and the audit log key on. It works from the admin's own
// request plus the configured trusted-proxy list; it needs no outbound probe.
//
// The dangerous configuration is an over-broad http.trusted_proxies: if it
// trusts every address, any caller can set X-Forwarded-For and be attributed
// to an IP of their choosing, which defeats per-IP rate limiting.
func (s *DiagnosticsService) checkProxyTrust(req DiagnosticsRequest) DiagnosticCheck {
	c := DiagnosticCheck{ID: "proxy_trust", Title: "Proxy trust and client IP"}

	trusted := s.cfg.HTTP.TrustedProxies
	peerHost := hostOnly(req.RemoteAddr)

	c.Findings = append(c.Findings,
		fmt.Sprintf("Your request's TCP peer is %s.", orUnknown(peerHost)),
		fmt.Sprintf("The server resolved your client IP to %s.", orUnknown(req.ClientIP)),
		fmt.Sprintf("X-Forwarded-For received: %s.", orNone(req.ForwardedFor)),
		fmt.Sprintf("http.trusted_proxies is set to: %s.", orNone(strings.Join(trusted, ", "))),
	)

	if broad := overBroadProxies(trusted); len(broad) > 0 {
		c.Status = DiagnosticCritical
		c.Summary = "http.trusted_proxies trusts every address, so client IPs are attacker-controlled."
		c.Findings = append(c.Findings, fmt.Sprintf("These entries trust the whole internet: %s. With them, any caller can set X-Forwarded-For and be counted as any IP, so per-IP rate limits can be bypassed and the audit log's source IPs cannot be trusted.", strings.Join(broad, ", ")))
		c.Remediation = "Set http.trusted_proxies to the specific address(es) of your reverse proxy only, never a default route (0.0.0.0/0 or ::/0)."
		return c
	}

	if len(trusted) == 0 {
		c.Status = DiagnosticWarn
		c.Summary = "No proxies are trusted, so client IPs are whoever connects to the process."
		c.Findings = append(c.Findings, "If this server sits behind a reverse proxy, every request's client IP is the proxy's, so per-IP rate limits are shared across all users and the audit log records the proxy rather than the caller. If nothing sits in front of it, this is correct.")
		c.Remediation = "If you run behind a proxy, set http.trusted_proxies to its address so the real client IP is recovered. If you do not, leave this empty."
		return c
	}

	c.Status = DiagnosticOK
	c.Summary = "Client IP resolution is bounded to specific trusted proxies."
	return c
}

// checkHeaderHygiene inspects the security headers actually returned by the
// public URL. Because the probe traverses the edge, this catches an edge that
// strips, weakens, or overrides the headers the app itself sets — including
// the legacy X-XSS-Protection header, which the app does not set at all, so
// any value seen there came from the edge.
func (s *DiagnosticsService) checkHeaderHygiene(p edgeProbe) DiagnosticCheck {
	c := DiagnosticCheck{ID: "header_hygiene", Title: "Security header hygiene"}
	if !p.reached {
		return skipped(c, "the public URL could not be reached")
	}

	worst := DiagnosticOK
	bump := func(to DiagnosticStatus) {
		if severity(to) > severity(worst) {
			worst = to
		}
	}

	h := p.headers
	if v := h.Get("Strict-Transport-Security"); v == "" {
		c.Findings = append(c.Findings, "Strict-Transport-Security (HSTS) is missing.")
		bump(DiagnosticWarn)
	}
	if v := h.Get("X-Content-Type-Options"); !strings.EqualFold(v, "nosniff") {
		c.Findings = append(c.Findings, "X-Content-Type-Options is not \"nosniff\".")
		bump(DiagnosticWarn)
	}
	if h.Get("X-Frame-Options") == "" && !strings.Contains(strings.ToLower(h.Get("Content-Security-Policy")), "frame-ancestors") {
		c.Findings = append(c.Findings, "Neither X-Frame-Options nor a CSP frame-ancestors directive is present.")
		bump(DiagnosticWarn)
	}
	if h.Get("Referrer-Policy") == "" {
		c.Findings = append(c.Findings, "Referrer-Policy is missing.")
		bump(DiagnosticWarn)
	}
	// The app never sets X-XSS-Protection. Any value here is the edge's, and
	// the legacy filter is best disabled (0) rather than enabled, per current
	// guidance, since the CSP is the real control.
	if v := h.Get("X-Xss-Protection"); v != "" && !strings.HasPrefix(strings.TrimSpace(v), "0") {
		c.Findings = append(c.Findings, fmt.Sprintf("X-XSS-Protection is %q. The app does not set this, so an edge added it; the legacy filter should be disabled (0) or removed.", v))
		bump(DiagnosticWarn)
	}

	c.Status = worst
	if worst == DiagnosticOK {
		c.Summary = "The public URL returns the expected security headers."
		return c
	}
	c.Summary = "The public URL is missing or weakening security headers the app sets."
	c.Remediation = "The app sets HSTS, X-Content-Type-Options, X-Frame-Options and Referrer-Policy itself. Where the edge shows something different it is overriding them; stop the edge rewriting these headers, and drop any X-XSS-Protection it adds."
	return c
}

// checkCORS attributes the CORS headers on the public URL against what the
// app itself emits. The app sets Access-Control-Allow-Origin: * (only on
// OIDC/well-known paths) and never sets Access-Control-Allow-Credentials, so
// any credentials header, and any reflected origin, came from the edge.
func (s *DiagnosticsService) checkCORS(p edgeProbe) DiagnosticCheck {
	c := DiagnosticCheck{ID: "cors", Title: "CORS / edge header attribution"}
	if !p.reached {
		return skipped(c, "the public URL could not be reached")
	}

	h := p.corsHeaders
	if h == nil {
		return skipped(c, "the CORS probe did not return")
	}

	acao := h.Get("Access-Control-Allow-Origin")
	acac := strings.EqualFold(h.Get("Access-Control-Allow-Credentials"), "true")
	reflected := acao != "" && strings.EqualFold(acao, canaryOrigin)

	switch {
	case reflected && acac:
		c.Status = DiagnosticCritical
		c.Summary = "The edge reflects an arbitrary Origin and allows credentials: a credentialed cross-origin read is possible."
		c.Findings = append(c.Findings,
			fmt.Sprintf("A probe with Origin %s came back with Access-Control-Allow-Origin echoing it and Access-Control-Allow-Credentials: true.", canaryOrigin),
			"The app never sets the credentials header, so this pairing is the edge's doing.",
		)
		c.Remediation = "At the reverse proxy, stop reflecting arbitrary origins into Access-Control-Allow-Origin, and remove Access-Control-Allow-Credentials unless a specific trusted origin genuinely needs it."
	case acac:
		c.Status = DiagnosticWarn
		c.Summary = "The edge adds Access-Control-Allow-Credentials, which the app never sets."
		c.Findings = append(c.Findings, "Access-Control-Allow-Credentials: true is present.")
		if acao == "*" {
			c.Findings = append(c.Findings, "It is paired with Access-Control-Allow-Origin: *, an invalid combination browsers reject today — but if the edge is ever changed to reflect the Origin instead of sending *, this becomes a live credentialed cross-origin read.")
		}
		c.Remediation = "Remove Access-Control-Allow-Credentials at the reverse proxy. The app's own CORS (Access-Control-Allow-Origin: * on OIDC paths only, no credentials) is already correct; the edge should not add to it."
	default:
		c.Status = DiagnosticOK
		c.Summary = "No unexpected CORS headers were added by the edge."
	}
	return c
}

// skipped marks a check that could not run.
func skipped(c DiagnosticCheck, why string) DiagnosticCheck {
	c.Status = DiagnosticSkipped
	c.Summary = "Skipped: " + why + "."
	return c
}

// severity orders the statuses so a check can keep the worst one it saw.
func severity(s DiagnosticStatus) int {
	switch s {
	case DiagnosticCritical:
		return 3
	case DiagnosticWarn:
		return 2
	case DiagnosticSkipped:
		return 1
	default:
		return 0
	}
}

// overBroadProxies returns the trusted-proxy entries that trust every
// address — a bare default route or an all-addresses CIDR — which is what
// makes X-Forwarded-For fully spoofable.
func overBroadProxies(entries []string) []string {
	var broad []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch e {
		case "0.0.0.0/0", "::/0", "0.0.0.0", "::":
			broad = append(broad, e)
			continue
		}
		if _, ipnet, err := net.ParseCIDR(e); err == nil {
			ones, bits := ipnet.Mask.Size()
			if ones == 0 && bits != 0 {
				broad = append(broad, e)
			}
		}
	}
	return broad
}

// hostOnly strips the port from a host:port pair, tolerating a bare host.
func hostOnly(addr string) string {
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
