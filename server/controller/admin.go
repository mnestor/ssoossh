package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
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
	adminGroup.PATCH("/users/:id/enable", a.enableUserHandler)

	// Auditor routes (read-only operations)
	auditorGroup := group.Group("/admin", sessionAuthMiddleware, auditorAuthMiddleware)
	auditorGroup.GET("/config", a.effectiveConfigHandler)
	auditorGroup.GET("/users", a.listUsersHandler)
	auditorGroup.GET("/users/:id", a.getUserHandler)
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

		AdminRequireGroup:       a.config.Admin.RequireGroup,
		AdminAuditorGroup:       a.config.Admin.AuditorGroup,
		AdminDisableGracePeriod: a.config.Admin.DisableGracePeriod.String(),
		AdminContactEmail:       a.config.Admin.ContactEmail,
		AdminDisabledMessage:    a.config.Admin.DisabledMessage,

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

// listUsersHandler handles GET /api/admin/users: returns a paginated,
// searchable list of all users for auditor review.
//
// @Summary     List all users (auditor-only)
// @Description Returns a paginated list of users, searchable by username,
// @Description email, or subject. Useful for user directory and audit.
// @Tags        admin
// @Produce     json
// @Param       limit query int false "Page size (default 25, max 100)" example(25)
// @Param       offset query int false "Results to skip (default 0)" example(0)
// @Param       q query string false "Search term" example(alice)
// @Success     200 {object} webtypes.AdminUsersListResponse "User list"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid paging/search parameters"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/users [get]
func (a *adminController) listUsersHandler(g *gin.Context) {
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}

	// Search over username, email, and subject.
	//
	// Model() rather than relying on the destination type: Count runs before
	// any Find, so without a model named here gorm has no table to count and
	// fails with "Table not set". The Find below would have inferred it from
	// the slice, which is why this only broke on the count.
	whereSQL, args := paging.Filter(params.Query, "username", "email", "subject")
	q := a.db.WithContext(g.Request.Context()).Model(&model.User{})
	if whereSQL != "" {
		q = q.Where(whereSQL, args...)
	}

	total, err := paging.Count(q)
	if err != nil {
		handleError(g, fmt.Errorf("failed to count users: %w", err))
		return
	}

	// ORDER BY to ensure deterministic pagination
	q = q.Order("id ASC")
	q = paging.Apply(q, params)

	var users []model.User
	if err := q.Find(&users).Error; err != nil {
		handleError(g, fmt.Errorf("failed to list users: %w", err))
		return
	}

	summaries := make([]webtypes.AdminUserSummary, len(users))
	for i, u := range users {
		summary := webtypes.AdminUserSummary{
			ID:        u.ID,
			Username:  u.Username,
			Email:     u.Email,
			Subject:   u.Subject,
			CreatedAt: u.CreatedAt,
			UpdatedAt: u.UpdatedAt,
		}
		if u.DisabledAt != nil {
			summary.DisabledAt = u.DisabledAt
			// Look up the admin that disabled this user
			if u.DisabledByUserID != nil {
				var admin model.User
				if err := a.db.WithContext(g.Request.Context()).
					Select("username").
					Where("id = ?", *u.DisabledByUserID).
					First(&admin).Error; err == nil {
					summary.DisabledByUsername = admin.Username
				}
			}
		}
		summaries[i] = summary
	}

	meta := newPageMeta(params, total)
	respondData(g, webtypes.AdminUsersListResponse{
		Users: summaries,
		Meta:  meta,
	})
}

// getUserHandler handles GET /api/admin/users/:id: returns detailed identity
// and enrollment/certificate information for one user.
//
// @Summary     Get user details (auditor-only)
// @Description Returns full identity details, disable state, and counts of
// @Description certificates and service enrollments for one user.
// @Tags        admin
// @Produce     json
// @Param       id path string true "User ID"
// @Success     200 {object} webtypes.AdminUserDetail "User details"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "User not found"
// @Security    sessionCookie
// @Router      /api/admin/users/{id} [get]
func (a *adminController) getUserHandler(g *gin.Context) {
	id := g.Param("id")
	if id == "" {
		handleError(g, fmt.Errorf("user ID is required"))
		return
	}

	var user model.User
	if err := a.db.WithContext(g.Request.Context()).
		Where("id = ?", id).
		First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)})
			return
		}
		handleError(g, fmt.Errorf("failed to get user: %w", err))
		return
	}

	// Decode JSON fields
	var otherAccounts []string
	if user.OtherAccounts != "" {
		if err := json.Unmarshal([]byte(user.OtherAccounts), &otherAccounts); err != nil {
			otherAccounts = []string{}
		}
	}

	var serviceAccounts []string
	if user.ServiceAccounts != "" {
		if err := json.Unmarshal([]byte(user.ServiceAccounts), &serviceAccounts); err != nil {
			serviceAccounts = []string{}
		}
	}

	extraFields := make(map[string]any)
	if user.ExtraFields != "" && user.ExtraFields != "{}" {
		if err := json.Unmarshal([]byte(user.ExtraFields), &extraFields); err != nil {
			extraFields = make(map[string]any)
		}
	}

	// Count certificates for this user
	var certCount int64
	a.db.WithContext(g.Request.Context()).
		Model(&model.Certificate{}).
		Where("user_id = ?", id).
		Count(&certCount)

	// Count active enrollments for this user
	var enrollmentCount int64
	a.db.WithContext(g.Request.Context()).
		Model(&model.Enrollment{}).
		Where("user_id = ? AND expires_at > ?", id, time.Now()).
		Count(&enrollmentCount)

	detail := webtypes.AdminUserDetail{
		ID:                     user.ID,
		Username:               user.Username,
		Email:                  user.Email,
		Subject:                user.Subject,
		OtherAccounts:          otherAccounts,
		ServiceAccounts:        serviceAccounts,
		ExtraFields:            extraFields,
		CreatedAt:              user.CreatedAt,
		UpdatedAt:              user.UpdatedAt,
		ServiceEnrollmentCount: int(enrollmentCount),
		CertificateCount:       int(certCount),
	}

	if user.DisabledAt != nil {
		detail.DisabledAt = user.DisabledAt
		if user.DisabledByUserID != nil {
			detail.DisabledByUserID = user.DisabledByUserID
			// Look up the admin that disabled this user
			var admin model.User
			if err := a.db.WithContext(g.Request.Context()).
				Select("username").
				Where("id = ?", *user.DisabledByUserID).
				First(&admin).Error; err == nil {
				detail.DisabledByUsername = &admin.Username
			}
		}
	}

	respondData(g, detail)
}

// disableUserHandler handles PATCH /api/admin/users/:id/disable: disables a
// user, preventing authentication and expiring their enrollments after a
// configured grace period.
//
// @Summary     Disable a user (admin-only)
// @Description Marks a user as disabled, preventing authentication and
// @Description eventually expiring their enrollments after the configured
// @Description grace period. The operation is idempotent.
// @Tags        admin
// @Produce     json
// @Param       id path string true "User ID"
// @Param       request body webtypes.DisableUserRequestBody false "Disable reason"
// @Success     200 {object} webtypes.DisableUserConsequences "Consequences of disabling"
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

	// Get current user to record who disabled this one
	currentIdentity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	// Look up current user's ID from the subject
	var currentUser model.User
	if err := a.db.WithContext(g.Request.Context()).
		Select("id").
		Where("subject = ?", currentIdentity.Subject).
		First(&currentUser).Error; err != nil {
		handleError(g, fmt.Errorf("failed to look up current user: %w", err))
		return
	}

	// Parse optional body for reason
	var req webtypes.DisableUserRequestBody
	if err := g.BindJSON(&req); err != nil && g.ContentType() != "" {
		// Only treat as error if body was actually provided
		handleError(g, &errorresponses.InvalidRequestError{Reason: "invalid request body"})
		return
	}

	now := time.Now()
	gracePeriod := a.config.Admin.DisableGracePeriod

	result := a.db.WithContext(g.Request.Context()).
		Model(&model.User{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"disabled_at":         now,
			"disabled_by_user_id": currentUser.ID,
		})

	if result.Error != nil {
		handleError(g, result.Error)
		return
	}

	if result.RowsAffected == 0 {
		handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)})
		return
	}

	expireAt := now.Add(gracePeriod)

	respondData(g, webtypes.DisableUserConsequences{
		GracePeriodSeconds:     int64(gracePeriod.Seconds()),
		ExpireAtTimestamp:      expireAt,
		ServiceEnrollmentCount: countActiveEnrollments(a.db.WithContext(g.Request.Context()), id),
	})
}

// enableUserHandler handles PATCH /api/admin/users/:id/enable: re-enables a
// user after disabling. Already-expired enrollments are not un-expired.
//
// @Summary     Re-enable a user (admin-only)
// @Description Re-enables a previously disabled user. The operation is idempotent.
// @Description Note: service enrollments that have already expired are not
// @Description un-expired; the user must request new enrollments.
// @Tags        admin
// @Produce     json
// @Param       id path string true "User ID"
// @Param       request body webtypes.ReEnableUserRequestBody false "Re-enable reason"
// @Success     200 {object} gin.H "User re-enabled"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "User not found"
// @Security    sessionCookie
// @Router      /api/admin/users/{id}/enable [patch]
func (a *adminController) enableUserHandler(g *gin.Context) {
	id := g.Param("id")
	if id == "" {
		handleError(g, fmt.Errorf("user ID is required"))
		return
	}

	result := a.db.WithContext(g.Request.Context()).
		Model(&model.User{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"disabled_at":         nil,
			"disabled_by_user_id": nil,
		})

	if result.Error != nil {
		handleError(g, result.Error)
		return
	}

	if result.RowsAffected == 0 {
		handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)})
		return
	}

	respondData(g, gin.H{"enabled": true})
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

// countActiveEnrollments counts how many active (not yet expired) service
// enrollments a user has.
func countActiveEnrollments(db *gorm.DB, userID string) int {
	var count int64
	db.Model(&model.Enrollment{}).
		Where("user_id = ? AND expires_at > ?", userID, time.Now()).
		Count(&count)
	return int(count)
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
