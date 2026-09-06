package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/server/config"
)

// DiagnosticStatus is the outcome of a single diagnostic check, worst-case
// across everything the check looked at.
type DiagnosticStatus string

const (
	// DiagnosticOK means the check found nothing wrong.
	DiagnosticOK DiagnosticStatus = "ok"
	// DiagnosticWarn means the check found a fragile or non-ideal
	// configuration that is not currently exploitable.
	DiagnosticWarn DiagnosticStatus = "warn"
	// DiagnosticCritical means the check found a live weakness.
	DiagnosticCritical DiagnosticStatus = "critical"
	// DiagnosticSkipped means the check could not run, most often because
	// the server could not reach its own public URL from where it runs.
	DiagnosticSkipped DiagnosticStatus = "skipped"
)

// DiagnosticCheck is the result of one deployment self-check.
type DiagnosticCheck struct {
	// ID is a stable machine key for the check, e.g. "reachability".
	ID string
	// Title is the human name shown in the admin UI.
	Title string
	// Status is the worst finding's severity.
	Status DiagnosticStatus
	// Summary is a one-line headline for the check.
	Summary string
	// Findings are the individual observations, already phrased for an
	// operator. Facts the app is sure of, not raw header dumps.
	Findings []string
	// Remediation is the concrete fix, empty when Status is OK.
	Remediation string
}

// DiagnosticsReport is the full set of checks from one run.
type DiagnosticsReport struct {
	// PublicOrigin is the URL the edge checks probed, echoed so the operator
	// can confirm the run tested what they expected.
	PublicOrigin string
	// Checks are the individual results, in display order.
	Checks []DiagnosticCheck
}

// DiagnosticsRequest carries the facts about the admin's own request that the
// proxy-trust check reasons about. Passed in rather than read from a
// *gin.Context so the service stays free of the web framework.
type DiagnosticsRequest struct {
	// RemoteAddr is the raw TCP peer (host:port), i.e. whoever actually
	// opened the connection to this process — the reverse proxy, normally.
	RemoteAddr string
	// ClientIP is what the server resolved the client to after applying
	// http.trusted_proxies, i.e. what rate limiting and the audit log key on.
	ClientIP string
	// ForwardedFor is the raw X-Forwarded-For header the request carried.
	ForwardedFor string
}

// httpDoer is the subset of *http.Client the self-probe needs, so tests can
// substitute a transport that never touches the network.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// canaryOrigin is the Origin the CORS check sends. It is a syntactically
// valid but unroutable origin: if it comes back reflected in
// Access-Control-Allow-Origin, the edge reflects arbitrary origins.
const canaryOrigin = "https://ssoossh-cors-probe.invalid"

// DiagnosticsService runs the deployment self-checks behind the admin
// diagnostics endpoint. It reads configuration and, for the edge checks,
// calls the server's own public URL back through whatever sits in front of
// it (see the check comments).
type DiagnosticsService struct {
	cfg    *config.Config
	client httpDoer
}

// NewDiagnosticsService builds a DiagnosticsService. A nil client installs a
// default with a short timeout and no redirect-following, so the checks see
// the edge's first response rather than wherever it forwards them.
func NewDiagnosticsService(cfg *config.Config, client httpDoer) *DiagnosticsService {
	if client == nil {
		client = &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &DiagnosticsService{cfg: cfg, client: client}
}

// Run executes every check and returns the report. It never returns an
// error: a check that cannot run reports itself as skipped, because a
// diagnostics screen that 500s is useless precisely when something is wrong.
func (s *DiagnosticsService) Run(ctx context.Context, req DiagnosticsRequest) DiagnosticsReport {
	origin := s.cfg.HTTP.PublicOrigin()

	// The edge checks share one probe of the public URL, so a single
	// unreachable target degrades all three the same way rather than three
	// separate round-trips and three separate failure messages.
	probe := s.probeEdge(ctx, origin)

	return DiagnosticsReport{
		PublicOrigin: origin,
		Checks: []DiagnosticCheck{
			s.checkReachability(origin, probe),
			s.checkProxyTrust(req),
			s.checkHeaderHygiene(probe),
			s.checkCORS(probe),
		},
	}
}

// edgeProbe is the result of fetching the public URL back through the edge.
type edgeProbe struct {
	// reached is true when a response came back at all.
	reached bool
	// err explains why reached is false.
	err error
	// status is the HTTP status of the plain (no-Origin) probe.
	status int
	// headers are the plain probe's response headers.
	headers http.Header
	// corsHeaders are the response headers when a canary Origin is sent to
	// an OIDC/well-known path (the app's own CORS surface).
	corsHeaders http.Header
	// tlsError is set when the public URL's certificate did not validate.
	tlsError string
}

// probeEdge fetches the public URL twice: once plainly (for reachability and
// header hygiene) and once with a canary Origin against a CORS path (for the
// CORS check). Both go out to PublicOrigin and therefore traverse whatever
// terminates TLS and rewrites headers in front of this process, which is the
// only vantage point from which the edge's additions are visible at all.
func (s *DiagnosticsService) probeEdge(ctx context.Context, origin string) edgeProbe {
	if origin == "" {
		return edgeProbe{err: fmt.Errorf("http.public_url is not set")}
	}

	plainStatus, plainHeaders, err := s.fetch(ctx, origin+"/healthz", "")
	if err != nil {
		p := edgeProbe{err: err}
		if strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate") {
			p.tlsError = err.Error()
		}
		return p
	}

	// The CORS surface is the OIDC discovery path, which the app answers
	// with Access-Control-Allow-Origin. A failure here is not fatal to the
	// whole probe: reachability and headers already succeeded, so a nil
	// header set just makes the CORS check report that it could not run.
	_, corsHeaders, corsErr := s.fetch(ctx, origin+"/.well-known/openid-configuration", canaryOrigin)
	if corsErr != nil {
		corsHeaders = nil
	}

	return edgeProbe{
		reached:     true,
		status:      plainStatus,
		headers:     plainHeaders,
		corsHeaders: corsHeaders,
	}
}

// fetch performs one GET, optionally with an Origin header, and returns the
// status and response headers.
func (s *DiagnosticsService) fetch(ctx context.Context, url, origin string) (int, http.Header, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	if origin != "" {
		httpReq.Header.Set("Origin", origin)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header, nil
}

// DiagnosticsRunner is the diagnostics behaviour the controller depends on,
// so its tests can substitute a canned report. Implemented by
// *DiagnosticsService.
type DiagnosticsRunner interface {
	Run(ctx context.Context, req DiagnosticsRequest) DiagnosticsReport
}
