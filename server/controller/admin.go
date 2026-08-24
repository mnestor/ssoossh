package controller

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewAdminController registers admin-scoped authorization and auditor-scoped
// read-only routes on group. Admin routes require admin group membership.
// Auditor routes require auditor-level access, which admin group membership
// also satisfies since auditor is a child role of admin.
func NewAdminController(
	group *gin.RouterGroup,
	c *config.Config,
	db *gorm.DB,
	sessionAuthMiddleware gin.HandlerFunc,
	adminAuthMiddleware gin.HandlerFunc,
	auditorAuthMiddleware gin.HandlerFunc,
	csrfMiddleware gin.HandlerFunc,
	enrollmentService service.EnrollmentProvider,
) {
	a := &adminController{config: c, db: db, enrollmentService: enrollmentService}

	// Admin routes (write operations)
	adminGroup := group.Group("/admin", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.PATCH("/enrollments/:id/expire", a.expireEnrollmentHandler)
	adminGroup.PATCH("/users/:id/disable", a.disableUserHandler)
	adminGroup.PATCH("/enrollments/:id/reassign", a.reassignEnrollmentHandler)

	// Auditor routes (read-only operations)
	auditorGroup := group.Group("/admin", sessionAuthMiddleware, auditorAuthMiddleware)
	auditorGroup.GET("/config", a.effectiveConfigHandler)
	auditorGroup.GET("/certificates/history", a.certificateHistoryHandler)
	auditorGroup.GET("/enrollments", a.listEnrollmentsHandler)
	auditorGroup.GET("/enrollments/:id", a.getEnrollmentDetailHandler)
}

// adminController handles admin and auditor-scoped HTTP routes.
type adminController struct {
	config            *config.Config
	db                *gorm.DB
	enrollmentService service.EnrollmentProvider
}

// effectiveConfigHandler handles GET /api/admin/config: returns the server's
// effective configuration with sensitive fields redacted.
//
// @Summary     View effective server configuration (auditor-only)
// @Description Returns the server's effective configuration with sensitive
// @Description fields redacted (CA key, client secret, cookie signing key,
// @Description database password). Useful for debugging and audit trails.
// @Tags        admin
// @Produce     json
// @Success     200 {object} webtypes.EffectiveConfigResponse "Current effective configuration"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/config [get]
func (a *adminController) effectiveConfigHandler(g *gin.Context) {
	resp := webtypes.EffectiveConfigResponse{
		ServerName: a.config.HTTP.ServerName,
		Port:       a.config.HTTP.Port,
		IsHTTPS:    a.config.HTTP.IsHTTPS,

		DBProvider:  string(a.config.DB.Provider),
		ProviderURL: a.config.AuthConfig.ProviderURL,

		AdminRequireGroup: a.config.Admin.RequireGroup,
		AdminAuditorGroup: a.config.Admin.AuditorGroup,

		LoggingLevel: a.config.Logging.Level,

		CertUserValidDuration:    a.config.CertOptions.User.ValidDuration.String(),
		CertUserRequireGroup:     a.config.CertOptions.User.RequireGroup,
		CertUserExtensions:       orEmpty(a.config.CertOptions.User.Extensions),
		CertServiceValidDuration: a.config.CertOptions.Service.ValidDuration.String(),
		CertServiceRequireGroup:  a.config.CertOptions.Service.RequireGroup,
		CertServiceExtensions:    orEmpty(a.config.CertOptions.Service.Extensions),
		CertPAMValidDuration:     a.config.CertOptions.PAM.ValidDuration.String(),
		CertPAMRequireGroup:      a.config.CertOptions.PAM.RequireGroup,
		CertClientTimeout:        a.config.CertOptions.ClientTimeout.String(),
		CertApprovalTTL:          a.config.CertOptions.ApprovalTTL().String(),
		CertSigningGrace:         a.config.CertOptions.SigningGrace().String(),
	}

	respondData(g, resp)
}

// expireEnrollmentHandler handles PATCH /api/admin/enrollments/:id/expire:
// immediately expires an enrollment, preventing future service certificate
// retrievals.
//
// @Summary     Expire an enrollment (admin-only)
// @Description Immediately marks an enrollment as expired, preventing future
// @Description service certificate retrievals. The operation is idempotent.
// @Tags        admin
// @Produce     json
// @Param       id path string true "Enrollment ID"
// @Success     200 {object} gin.H "Enrollment expired"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "Enrollment not found"
// @Security    sessionCookie
// @Router      /api/admin/enrollments/{id}/expire [patch]
func (a *adminController) expireEnrollmentHandler(g *gin.Context) {
	id := g.Param("id")
	if id == "" {
		handleError(g, fmt.Errorf("enrollment ID is required"))
		return
	}

	// Update the enrollment's ExpiresAt to now, which will prevent retrieval
	// in the enrollment service.
	result := a.db.WithContext(g.Request.Context()).
		Model(&adminEnrollmentModel{}).
		Where("id = ?", id).
		Update("expires_at", time.Now())
	if result.Error != nil {
		handleError(g, result.Error)
		return
	}
	// A valid UPDATE that matched no rows returns a nil error but affects
	// zero rows: without this the handler would answer {"expired": true} for
	// an enrollment ID that does not exist, contradicting its own documented
	// 404 and telling an admin an expiry happened that did not.
	if result.RowsAffected == 0 {
		handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", id)})
		return
	}

	respondData(g, gin.H{"expired": true})
}

// disableUserHandler handles PATCH /api/admin/users/:id/disable: disables a
// user, preventing future authentication and expiring their enrollments after
// a configured grace period.
//
// @Summary     Disable a user (admin-only)
// @Description Marks a user as disabled, preventing authentication and
// @Description eventually expiring their enrollments after the configured
// @Description grace period. The operation is idempotent.
// @Tags        admin
// @Produce     json
// @Param       id path string true "User ID"
// @Success     200 {object} gin.H "User disabled"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "User not found"
// @Security    sessionCookie
// @Router      /api/admin/users/{id}/disable [patch]
func (a *adminController) disableUserHandler(g *gin.Context) {
	id := g.Param("id")
	if id == "" {
		handleError(g, fmt.Errorf("user ID is required"))
		return
	}

	// TODO: implement user disable logic with grace period for enrollment expiry.
	// This is a placeholder that documents the interface.
	handleError(g, fmt.Errorf("user disable is not yet implemented"))
}

// certificateHistoryHandler handles GET /api/admin/certificates/history: returns
// cross-user certificate history for auditor review.
//
// @Summary     View certificate history across all users (auditor-only)
// @Description Returns issued certificates across all users, useful for audits,
// @Description incident review, and tracking "who issued this?". Supports
// @Description filtering and pagination.
// @Tags        admin
// @Produce     json
// @Param       limit query int false "Number of results (default 50)" example(50)
// @Param       offset query int false "Number of results to skip (default 0)" example(0)
// @Success     200 {object} gin.H "Certificate history"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/certificates/history [get]
func (a *adminController) certificateHistoryHandler(g *gin.Context) {
	// TODO: implement certificate history query with filtering and pagination.
	// This is a placeholder that documents the interface.
	respondData(g, gin.H{"certificates": []any{}})
}

// adminEnrollmentModel is a minimal GORM model used for admin operations on enrollments.
type adminEnrollmentModel struct {
	ID        string    `gorm:"column:id"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
}

// TableName specifies the database table name.
func (adminEnrollmentModel) TableName() string {
	return "enrollments"
}

// listEnrollmentsHandler handles GET /api/admin/enrollments: paged, searchable
// list of all enrollments across users, auditor-scoped.
//
// @Summary     List all service enrollments (auditor-only)
// @Description Returns a paged list of all service enrollments across users, searchable
// @Description by approver name, principals, key ID, or request ID. Visible to auditors.
// @Tags        admin
// @Produce     json
// @Param       limit query int false "Page size (default 25, max 100)" example(25)
// @Param       offset query int false "Rows to skip (default 0)" example(0)
// @Param       q query string false "Search query (max 200 chars)" example("alice")
// @Success     200 {object} openapidoc.AdminEnrollmentsEnvelope "Paged enrollment list"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/enrollments [get]
func (a *adminController) listEnrollmentsHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}

	list, err := a.enrollmentService.ListForAdmin(g.Request.Context(), identity,
		service.AdminListParams{Limit: params.Limit, Offset: params.Offset, Query: params.Query})
	if err != nil {
		handleError(g, err)
		return
	}

	// Convert to web types
	enrollments := make([]webtypes.AdminEnrollmentResponse, 0, len(list.Enrollments))
	for _, row := range list.Enrollments {
		resp := webtypes.AdminEnrollmentResponse{
			ID:                   row.Enrollment.ID,
			ApprovedByUsername:   row.Approver.Username,
			ApprovedByEmail:      row.Approver.Email,
			Principals:           row.Principals,
			KeyID:                row.Enrollment.KeyID,
			PublicKeyFingerprint: row.Fingerprint,
			Options:              convertOptions(row.Options),
			CreatedAt:            row.Enrollment.CreatedAt,
			ExpiresAt:            row.Enrollment.ExpiresAt,
			RetrievalCount:       row.RetrievalCount,
			LastRetrievedAt:      row.LastRetrievedAt,
		}
		if row.Enrollment.RedeemedAt != nil {
			resp.FirstRedeemedAt = row.Enrollment.RedeemedAt
		}
		if row.Enrollment.CertificateDurationSeconds != nil {
			certSecs := int(*row.Enrollment.CertificateDurationSeconds)
			resp.CertificateValidSeconds = &certSecs
		}
		enrollments = append(enrollments, resp)
	}

	meta := webtypes.PageMeta{
		Total:     list.Total,
		Limit:     params.Limit,
		Offset:    params.Offset,
		Page:      params.PageNumber(),
		PageCount: params.PageCount(list.Total),
	}

	respondData(g, webtypes.AdminEnrollmentsResponse{
		Enrollments: enrollments,
		Meta:        meta,
	})
}

// getEnrollmentDetailHandler handles GET /api/admin/enrollments/:id: full
// enrollment details including retrieval log, auditor-scoped.
//
// @Summary     Get enrollment details (auditor-only)
// @Description Returns full details of one enrollment including its retrieval log
// @Description and reassignment history. Visible to the approver and auditors.
// @Tags        admin
// @Produce     json
// @Param       id path string true "Enrollment ID"
// @Success     200 {object} openapidoc.EnrollmentDetailEnvelope "Full enrollment details"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized"
// @Failure     404 {object} openapidoc.ErrorEnvelope "Enrollment not found"
// @Security    sessionCookie
// @Router      /api/admin/enrollments/{id} [get]
func (a *adminController) getEnrollmentDetailHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	detail, err := a.enrollmentService.GetEnrollmentDetail(g.Request.Context(),
		g.Param("id"), identity)
	if err != nil {
		handleError(g, err)
		return
	}

	// Build retrievals response
	retrievals := make([]webtypes.EnrollmentRetrievalResponse, 0, len(detail.Retrievals.Retrievals))
	for _, r := range detail.Retrievals.Retrievals {
		retrievals = append(retrievals, webtypes.EnrollmentRetrievalResponse{
			RetrievedAt:       r.RetrievedAt,
			SourceIP:          r.SourceIP,
			CertificateSerial: r.CertificateSerial,
			Succeeded:         r.Succeeded,
		})
	}

	resp := webtypes.AdminEnrollmentResponse{
		ID:                   detail.Enrollment.ID,
		ApprovedByUsername:   detail.Approver.Username,
		ApprovedByEmail:      detail.Approver.Email,
		Principals:           detail.Principals,
		KeyID:                detail.Enrollment.KeyID,
		PublicKeyFingerprint: detail.Fingerprint,
		Options:              convertOptions(detail.Options),
		CreatedAt:            detail.Enrollment.CreatedAt,
		ExpiresAt:            detail.Enrollment.ExpiresAt,
		RetrievalCount:       detail.Retrievals.Total,
	}
	if detail.Enrollment.RedeemedAt != nil {
		resp.FirstRedeemedAt = detail.Enrollment.RedeemedAt
	}
	if detail.Enrollment.CertificateDurationSeconds != nil {
		certSecs := int(*detail.Enrollment.CertificateDurationSeconds)
		resp.CertificateValidSeconds = &certSecs
	}
	if len(detail.Retrievals.Retrievals) > 0 {
		resp.LastRetrievedAt = &detail.Retrievals.Retrievals[0].RetrievedAt
	}

	respondData(g, gin.H{
		"enrollment":      resp,
		"retrievals":      retrievals,
		"retrieval_total": detail.Retrievals.Total,
	})
}

// reassignEnrollmentHandler handles PATCH /api/admin/enrollments/:id/reassign:
// transfer ownership of an enrollment to another user (admin-scoped).
//
// @Summary     Reassign an enrollment (admin/owner-only)
// @Description Transfers ownership of an enrollment to another user. The new owner
// @Description must have the required service account. Idempotent.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Param       id path string true "Enrollment ID"
// @Param       request body openapidoc.ReassignEnrollmentRequest true "New owner user ID"
// @Success     200 {object} gin.H "Enrollment reassigned"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid request (ineligible target)"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized (must be owner or admin)"
// @Failure     404 {object} openapidoc.ErrorEnvelope "Enrollment or target user not found"
// @Security    sessionCookie
// @Router      /api/admin/enrollments/{id}/reassign [patch]
func (a *adminController) reassignEnrollmentHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	var body struct {
		ToUserID string `json:"to_user_id" binding:"required"`
	}
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	err := a.enrollmentService.Reassign(g.Request.Context(),
		g.Param("id"), body.ToUserID, identity)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, gin.H{"reassigned": true})
}

// convertOptions converts a service.RequestedOptions to a webtypes.CertificateOptionsResponse.
func convertOptions(opts service.RequestedOptions) webtypes.CertificateOptionsResponse {
	return webtypes.CertificateOptionsResponse{
		Extensions:      opts.Extensions,
		ForceCommand:    opts.ForceCommand,
		SourceAddresses: opts.SourceAddresses,
		NoTouchRequired: opts.NoTouchRequired,
	}
}
