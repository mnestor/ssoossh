package controller

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
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
) {
	a := &adminController{config: c, db: db}

	// Admin routes (write operations)
	adminGroup := group.Group("/admin", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.PATCH("/enrollments/:id/expire", a.expireEnrollmentHandler)
	adminGroup.PATCH("/users/:id/disable", a.disableUserHandler)

	// Auditor routes (read-only operations)
	auditorGroup := group.Group("/admin", sessionAuthMiddleware, auditorAuthMiddleware)
	auditorGroup.GET("/config", a.effectiveConfigHandler)
	auditorGroup.GET("/certificates/history", a.certificateHistoryHandler)
}

// adminController handles admin and auditor-scoped HTTP routes.
type adminController struct {
	config *config.Config
	db     *gorm.DB
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
// @Description searching over key ID, principals, serial number, fingerprint, and owner
// @Description username/email, filtering by type and expiration status, and offset pagination.
// @Tags        admin
// @Produce     json
// @Param       limit query int false "Number of results (default 25, max 100)" example(25)
// @Param       offset query int false "Number of results to skip (default 0)" example(0)
// @Param       q query string false "Search term (max 200 chars)" example(alice)
// @Param       type query string false "Filter by certificate type (user/service/pam)" example(user)
// @Param       status query string false "Filter by expiration (live/expired)" example(live)
// @Success     200 {object} openapidoc.CertificateListAdminEnvelope "Certificates and page metadata"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid parameters"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/certificates/history [get]
func (a *adminController) certificateHistoryHandler(g *gin.Context) {
	// Parse pagination and search parameters.
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}

	// Parse certificate type filter (optional).
	certTypeFilter := g.Query("type")
	statusFilter := g.Query("status")

	// Build the query: select certificates with their owner's username/email.
	query := a.db.WithContext(g.Request.Context()).
		Model(&model.Certificate{}).
		Select(`certificates.id, certificates.type, certificates.serial_number,
			certificates.key_id, certificates.principals, certificates.public_key_fingerprint,
			certificates.issued_at, certificates.expires_at, certificates.user_id,
			users.username, users.email`).
		Joins("LEFT JOIN users ON certificates.user_id = users.id")

	// Apply search filter across key ID, principals, serial, fingerprint, username, email.
	if params.Query != "" {
		whereClause, args := paging.Filter(params.Query,
			"certificates.key_id",
			"certificates.principals",
			"certificates.public_key_fingerprint",
			"users.username",
			"users.email",
			"CAST(certificates.serial_number AS TEXT)",
		)
		if whereClause != "" {
			query = query.Where(whereClause, args...)
		}
	}

	// Apply type filter (optional).
	if certTypeFilter != "" {
		query = query.Where("certificates.type = ?", certTypeFilter)
	}

	// Apply status filter (optional): live=not expired, expired=expired.
	now := time.Now()
	switch statusFilter {
	case "live":
		query = query.Where("certificates.expires_at > ?", now)
	case "expired":
		query = query.Where("certificates.expires_at <= ?", now)
	}

	// Apply deterministic ordering to prevent pagination issues.
	query = query.Order("certificates.issued_at DESC, certificates.id DESC")

	// Count total matching rows before paging.
	total, err := paging.Count(query)
	if err != nil {
		handleError(g, err)
		return
	}

	// Apply pagination window.
	query = paging.Apply(query, params)

	// Fetch the results.
	type certRow struct {
		ID                   string
		Type                 model.CertificateType
		SerialNumber         uint64
		KeyID                string
		Principals           string
		PublicKeyFingerprint string
		IssuedAt             time.Time
		ExpiresAt            time.Time
		UserID               *string
		Username             *string
		Email                *string
	}

	var rows []certRow
	if err := query.Scan(&rows).Error; err != nil {
		handleError(g, err)
		return
	}

	// Convert to response format.
	certs := make([]webtypes.CertificateResponse, 0, len(rows))
	for _, r := range rows {
		cert := webtypes.CertificateResponse{
			ID:           r.ID,
			Type:         r.Type,
			SerialNumber: r.SerialNumber,
			KeyID:        r.KeyID,
			Principals:   r.Principals,
			Fingerprint:  r.PublicKeyFingerprint,
			IssuedAt:     r.IssuedAt,
			ExpiresAt:    r.ExpiresAt,
		}
		certs = append(certs, cert)
	}

	// Build response with page metadata.
	resp := webtypes.CertificateListAdminResponse{
		Certificates: certs,
		PageMeta:     newPageMeta(params, total),
	}

	respondData(g, resp)
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
