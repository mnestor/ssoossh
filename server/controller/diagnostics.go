package controller

import (
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// diagnosticsController serves the admin deployment self-checks.
type diagnosticsController struct {
	diag  service.DiagnosticsRunner
	audit *service.AuditService
	db    *gorm.DB
}

// NewDiagnosticsController registers the admin diagnostics route.
//
// Admin-only, not auditor: unlike the read-only LDAP status, running this
// makes the server open an outbound connection (to its own public URL), so
// it sits with the actions rather than the reads. CSRF-guarded and
// rate-limited for the same reason the directory probe is — it is a
// state-free but side-effecting POST a loop should not be able to drive.
func NewDiagnosticsController(
	group *gin.RouterGroup,
	diag service.DiagnosticsRunner,
	db *gorm.DB,
	auditService *service.AuditService,
	sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware gin.HandlerFunc,
	rateLimit gin.HandlerFunc,
) {
	d := &diagnosticsController{diag: diag, audit: auditService, db: db}

	adminGroup := group.Group("/admin/diagnostics", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	if rateLimit != nil {
		adminGroup.POST("/run", rateLimit, d.runHandler)
		return
	}
	adminGroup.POST("/run", d.runHandler)
}

// runHandler handles POST /api/admin/diagnostics/run.
//
// @Summary     Run deployment self-checks (admin-only)
// @Description Runs the deployment self-checks and returns what they found:
// @Description whether the server can reach its own public URL, how the
// @Description client IP is resolved through http.trusted_proxies, whether
// @Description the edge weakens the security headers the app sets, and
// @Description whether the edge adds dangerous CORS headers. The edge checks
// @Description call the server's own public_url back through whatever sits in
// @Description front of it; the target is always public_url and cannot be
// @Description supplied by the caller. Nothing is written.
// @Tags        admin
// @Produce     json
// @Success     200 {object} webtypes.DiagnosticsResponse "What the checks found"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     429 {object} openapidoc.ErrorEnvelope "Too many runs"
// @Security    sessionCookie
// @Router      /api/admin/diagnostics/run [post]
func (d *diagnosticsController) runHandler(g *gin.Context) {
	report := d.diag.Run(g.Request.Context(), service.DiagnosticsRequest{
		RemoteAddr:   g.Request.RemoteAddr,
		ClientIP:     g.ClientIP(),
		ForwardedFor: g.GetHeader("X-Forwarded-For"),
	})

	d.recordAudit(g, report)
	respondData(g, toDiagnosticsResponse(report))
}

// recordAudit records the run with the worst status each check reached, so
// the log shows an admin ran the checks and what they concluded without
// storing the full report.
func (d *diagnosticsController) recordAudit(g *gin.Context, report service.DiagnosticsReport) {
	if d.audit == nil {
		return
	}
	statuses := make(map[string]any, len(report.Checks))
	for _, c := range report.Checks {
		statuses[c.ID] = string(c.Status)
	}
	d.audit.Record(g.Request.Context(), service.AuditEvent{
		Action:     service.AuditAdminDiagnosticsRun,
		Actor:      d.auditActor(g),
		OccurredAt: time.Now(),
		Detail:     statuses,
	})
}

// auditActor snapshots the calling identity as an event actor, resolving its
// users-row id. Mirrors the LDAP admin controller's.
func (d *diagnosticsController) auditActor(g *gin.Context) *service.AuditSubject {
	identity, ok := middleware.Identity(g)
	if !ok || identity == nil {
		// not covered: this route sits behind SessionAuthMiddleware, which
		// aborts before the handler when there is no identity.
		return nil
	}
	var user model.User
	if d.db != nil {
		_ = d.db.WithContext(g.Request.Context()).
			Select("id").Where("subject = ?", identity.Subject).
			First(&user).Error
	}
	return service.AuditSubjectFromIdentity(identity, user.ID)
}

// toDiagnosticsResponse maps the service report to its wire shape.
func toDiagnosticsResponse(report service.DiagnosticsReport) webtypes.DiagnosticsResponse {
	checks := make([]webtypes.DiagnosticCheckResult, 0, len(report.Checks))
	for _, c := range report.Checks {
		checks = append(checks, webtypes.DiagnosticCheckResult{
			ID:          c.ID,
			Title:       c.Title,
			Status:      string(c.Status),
			Summary:     c.Summary,
			Findings:    orEmpty(c.Findings),
			Remediation: c.Remediation,
		})
	}
	return webtypes.DiagnosticsResponse{
		PublicOrigin: report.PublicOrigin,
		Checks:       checks,
	}
}
