package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewAdminController registers admin-scoped, SOC-scoped, and auditor-scoped
// routes on group. Admin routes require admin group membership. SOC routes
// take admin or SOC group membership. Auditor routes require auditor-level
// access, which admin and SOC membership also satisfy since auditor is a
// child role of both.
func NewAdminController(
	group *gin.RouterGroup,
	c *config.Config,
	db *gorm.DB,
	sessionAuthMiddleware gin.HandlerFunc,
	adminAuthMiddleware gin.HandlerFunc,
	socAuthMiddleware gin.HandlerFunc,
	auditorAuthMiddleware gin.HandlerFunc,
	csrfMiddleware gin.HandlerFunc,
	enrollmentService service.EnrollmentProvider,
	auditService *service.AuditService,
) {
	a := &adminController{config: c, db: db, enrollmentService: enrollmentService, audit: auditService}

	// Admin routes (restorative writes, require admin group)
	adminGroup := group.Group("/admin", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.PATCH("/users/:id/enable", a.enableUserHandler)

	// SOC routes (containment writes: admin or SOC group). SOC holds the
	// operations that revoke access — expiring an enrollment, disabling a
	// user — and never the ones that restore it, so a SOC analyst can
	// contain an incident but not quietly undo a containment.
	socGroup := group.Group("/admin", sessionAuthMiddleware, socAuthMiddleware, csrfMiddleware)
	socGroup.PATCH("/enrollments/:id/expire", a.expireEnrollmentHandler)
	socGroup.PATCH("/users/:id/disable", a.disableUserHandler)

	// Auditor routes (read-only operations)
	auditorGroup := group.Group("/admin", sessionAuthMiddleware, auditorAuthMiddleware)
	auditorGroup.GET("/config", a.effectiveConfigHandler)
	auditorGroup.GET("/users", a.listUsersHandler)
	auditorGroup.GET("/users/:id", a.getUserHandler)
	auditorGroup.GET("/certificates/history", a.certificateHistoryHandler)
	auditorGroup.GET("/enrollments", a.listEnrollmentsHandler)
	auditorGroup.GET("/enrollments/:id", a.getEnrollmentDetailHandler)
	auditorGroup.GET("/audit", a.auditFeedHandler)
	auditorGroup.GET("/users/:id/audit", a.userAuditHandler)
}

// adminController handles admin and auditor-scoped HTTP routes.
type adminController struct {
	config            *config.Config
	db                *gorm.DB
	enrollmentService service.EnrollmentProvider
	// audit records the privileged views and the containment actions this
	// controller performs. Nil in tests that do not exercise auditing.
	audit *service.AuditService
}

// auditRecord records one event when an auditor is wired. Privileged views
// audit through here, and a failed insert never fails the read it
// describes — see service.AuditService.Record.
func (a *adminController) auditRecord(g *gin.Context, event service.AuditEvent) {
	if a.audit == nil {
		return
	}
	a.audit.Record(g.Request.Context(), event)
}

// auditActor snapshots the calling identity as an event actor, resolving
// its users-row id for the timeline grouping key. A lookup miss yields a
// snapshot without the key rather than no event at all.
func (a *adminController) auditActor(g *gin.Context) *service.AuditSubject {
	identity, ok := middleware.Identity(g)
	if !ok {
		return nil
	}
	var user model.User
	if err := a.db.WithContext(g.Request.Context()).
		Select("id").Where("subject = ?", identity.Subject).
		First(&user).Error; err != nil {
		slog.Warn("failed to resolve the acting user for an audit event",
			"subject", identity.Subject, "error", err)
	}
	return service.AuditSubjectFromIdentity(identity, user.ID)
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
	a.auditRecord(g, service.AuditEvent{
		Action: service.AuditAdminConfigViewed,
		Actor:  a.auditActor(g),
	})

	resp := webtypes.EffectiveConfigResponse{
		PublicURL: a.config.HTTP.PublicURL,
		Port:      a.config.HTTP.Port,

		DBProvider:  string(a.config.DB.Provider),
		ProviderURL: a.config.AuthConfig.ProviderURL,

		AdminRequireGroup:    a.config.Admin.RequireGroup,
		AdminSOCGroup:        a.config.Admin.SOCGroup,
		AdminAuditorGroup:    a.config.Admin.AuditorGroup,
		AdminContactEmail:    a.config.Admin.ContactEmail,
		AdminDisabledMessage: a.config.Admin.DisabledMessage,

		LoggingLevel: a.config.Logging.Level,

		CertUserValidDuration:    a.config.CertOptions.User.ValidDuration.String(),
		CertUserRequire:          a.config.CertOptions.User.Require.String(),
		CertUserExtensions:       orEmpty(a.config.CertOptions.User.Extensions),
		CertServiceValidDuration: a.config.CertOptions.Service.ValidDuration.String(),
		CertServiceRequire:       a.config.CertOptions.Service.Require.String(),
		CertServiceExtensions:    orEmpty(a.config.CertOptions.Service.Extensions),
		CertPAMValidDuration:     a.config.CertOptions.PAM.ValidDuration.String(),
		CertPAMRequire:           a.config.CertOptions.PAM.Require.String(),
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
// @Summary     Expire an enrollment (admin or SOC)
// @Description Immediately marks an enrollment as expired, preventing future
// @Description service certificate retrievals. The operation is idempotent.
// @Tags        admin
// @Produce     json
// @Param       id path string true "Enrollment ID"
// @Param       request body webtypes.ExpireEnrollmentRequestBody true "Expiry reason (required)"
// @Success     200 {object} gin.H "Enrollment expired"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin or SOC"
// @Failure     404 {object} openapidoc.ErrorEnvelope "Enrollment not found"
// @Security    sessionCookie
// @Router      /api/admin/enrollments/{id}/expire [patch]
func (a *adminController) expireEnrollmentHandler(g *gin.Context) {
	id := g.Param("id")
	if id == "" {
		handleError(g, fmt.Errorf("enrollment ID is required"))
		return
	}

	var req webtypes.ExpireEnrollmentRequestBody
	if err := g.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "invalid request body"})
		return
	}
	reason, err := service.ValidateAuditReason(service.AuditEnrollmentExpired, req.Reason)
	if err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: err.Error()})
		return
	}

	// Read first so the event can name the enrollment's owner, which is
	// what puts the expiry on their timeline.
	var enrollment model.Enrollment
	if err := a.db.WithContext(g.Request.Context()).First(&enrollment, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", id)})
			return
		}
		handleError(g, fmt.Errorf("failed to look up the enrollment being expired: %w", err))
		return
	}

	now := time.Now()
	auditEvent := service.AuditEvent{
		Action:     service.AuditEnrollmentExpired,
		Actor:      a.auditActor(g),
		Target:     &service.AuditSubject{UserID: enrollment.UserID},
		Reason:     reason,
		OccurredAt: now,
		Detail: map[string]any{
			"enrollment_id": id,
			"key_id":        enrollment.KeyID,
		},
	}

	// Update the enrollment's ExpiresAt to now, which will prevent retrieval
	// in the enrollment service.
	err = a.db.WithContext(g.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// expiry_reminder_sent_at is cleared alongside the date, honoring the
		// rule that any path moving expires_at earlier releases the reminder
		// claim (see model.Enrollment.ExpiryReminderSentAt). It changes
		// nothing today — this path expires the code outright, and the sweep
		// only reminds about codes still in the future — but the invariant
		// belongs at every write of the column, not only at the ones where
		// omitting it would currently be visible.
		result := tx.Model(&adminEnrollmentModel{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"expires_at":              now,
				"expiry_reminder_sent_at": nil,
			})
		if result.Error != nil {
			return result.Error
		}
		// A valid UPDATE that matched no rows returns a nil error but
		// affects zero rows: without this the handler would answer
		// {"expired": true} for an enrollment ID that does not exist,
		// contradicting its own documented 404 and telling an admin an
		// expiry happened that did not.
		if result.RowsAffected == 0 {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", id)}
		}
		if a.audit == nil {
			return nil
		}
		return a.audit.RecordTx(tx, auditEvent)
	})
	if err != nil {
		handleError(g, err)
		return
	}

	if a.audit != nil {
		a.audit.LogOnly(auditEvent)
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

	// Resolve every "disabled by" name in one query rather than one per
	// disabled row: this page is up to 100 users, and a lookup inside the
	// loop below made the directory's cost scale with how much of the
	// organization had been disabled.
	disablerNames, err := a.lookupUsernames(g.Request.Context(), disablerIDs(users))
	if err != nil {
		handleError(g, fmt.Errorf("failed to resolve who disabled these users: %w", err))
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
			if u.DisabledByUserID != nil {
				// A missing name is left empty, as the per-row lookup did:
				// the admin's own row may have been pruned, and that is not
				// a reason to fail the directory.
				summary.DisabledByUsername = disablerNames[*u.DisabledByUserID]
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

	// Recorded after the lookup below rather than here, so a 404 does not
	// leave a view event for a user that does not exist.
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

	a.auditRecord(g, service.AuditEvent{
		Action: service.AuditAdminUserViewed,
		Actor:  a.auditActor(g),
		Target: service.AuditSubjectFromUser(&user),
	})

	otherAccounts := decodeStringList(user.OtherAccounts)
	serviceAccounts := decodeStringList(user.ServiceAccounts)
	extraFields := decodeStringMap(user.ExtraFields)

	certCount, enrollmentCount, err := a.userCounts(g.Request.Context(), id)
	if err != nil {
		handleError(g, err)
		return
	}

	detail := webtypes.AdminUserDetail{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
		Subject:  user.Subject,
		// orEmpty, because these are declared validate:"required" on the wire
		// type and typed string[] in the generated TypeScript. A user whose
		// row carries no accounts decodes to a nil slice, which marshals as
		// null -- and null where an array was promised is what stopped the
		// detail page rendering at all.
		OtherAccounts:          orEmpty(otherAccounts),
		ServiceAccounts:        orEmpty(serviceAccounts),
		ExtraFields:            extraFields,
		CreatedAt:              user.CreatedAt,
		UpdatedAt:              user.UpdatedAt,
		ServiceEnrollmentCount: enrollmentCount,
		CertificateCount:       certCount,
	}

	if user.DisabledAt != nil {
		detail.DisabledAt = user.DisabledAt
		detail.DisabledReason = user.DisabledReason
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
// user, preventing authentication.
//
// Service enrollments are deliberately untouched. They belong to their
// service accounts rather than to the person who approved them (see
// docs/proposals/enrollment-group-ownership.md), so the unattended jobs
// behind them keep running and the account's other holders keep control of
// them.
//
// @Summary     Disable a user (admin or SOC)
// @Description Marks a user as disabled, preventing authentication. Service
// @Description enrollments they approved are unaffected. The operation is
// @Description idempotent.
// @Tags        admin
// @Produce     json
// @Param       id path string true "User ID"
// @Param       request body webtypes.DisableUserRequestBody true "Disable reason (required)"
// @Success     200 {object} webtypes.DisableUserConsequences "Consequences of disabling"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin or SOC"
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

	// The body is optional, so an absent one is not an error.
	//
	// ShouldBindJSON, not BindJSON: the latter is MustBindWith, which writes
	// a 400 and aborts BEFORE returning the error. Ignoring the returned
	// error therefore does not undo anything -- the status is already set,
	// and the handler goes on to write its success payload underneath it,
	// producing a 400 whose body says {"error": null}. That is exactly what
	// the browser saw, and why the page never showed the user as disabled.
	var req webtypes.DisableUserRequestBody
	if err := g.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "invalid request body"})
		return
	}

	// The API already accepted a reason and silently discarded it. It is
	// now required and persisted: the next admin opening this account needs
	// to learn why it was disabled, and an optional field does not get
	// filled.
	reason, err := service.ValidateAuditReason(service.AuditUserDisabled, req.Reason)
	if err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: err.Error()})
		return
	}

	now := time.Now()

	// The target is snapshotted before the update so the audit event
	// records the account as it stood when the action was taken.
	var target model.User
	if err := a.db.WithContext(g.Request.Context()).First(&target, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)})
			return
		}
		handleError(g, fmt.Errorf("failed to look up the user being disabled: %w", err))
		return
	}

	actor := a.auditActor(g)
	auditEvent := service.AuditEvent{
		Action:     service.AuditUserDisabled,
		Actor:      actor,
		Target:     service.AuditSubjectFromUser(&target),
		Reason:     reason,
		OccurredAt: now,
	}

	// The disable and its audit row commit together: a containment action
	// without a record of who took it and why is the one outcome this
	// exists to prevent.
	err = a.db.WithContext(g.Request.Context()).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"disabled_at":         now,
				"disabled_by_user_id": currentUser.ID,
				"disabled_reason":     reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)}
		}
		if a.audit == nil {
			return nil
		}
		return a.audit.RecordTx(tx, auditEvent)
	})
	if err != nil {
		handleError(g, err)
		return
	}

	if a.audit != nil {
		a.audit.LogOnly(auditEvent)
	}

	// The disable is already committed, so a failure to count cannot fail
	// the request — that would report an error for a write that succeeded.
	// It is logged instead of silently reported as zero.
	enrollmentCount, err := countActiveEnrollments(a.db.WithContext(g.Request.Context()), id)
	if err != nil {
		slog.Error("failed to count the enrollments this user approved; reporting the disable without it",
			"user_id", id, "error", err)
	}

	respondData(g, webtypes.DisableUserConsequences{ServiceEnrollmentCount: enrollmentCount})
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
// @Param       request body webtypes.ReEnableUserRequestBody true "Re-enable reason (required)"
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

	var req webtypes.ReEnableUserRequestBody
	if err := g.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "invalid request body"})
		return
	}

	// Required for the same reason the disable reason is: "cleared with
	// security, SEC-1234" is as valuable to the person after this one.
	reason, err := service.ValidateAuditReason(service.AuditUserEnabled, req.Reason)
	if err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: err.Error()})
		return
	}

	// Snapshotted while still disabled, so the event records why it had
	// been disabled alongside why it is being restored.
	var target model.User
	if err := a.db.WithContext(g.Request.Context()).First(&target, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			handleError(g, &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)})
			return
		}
		handleError(g, fmt.Errorf("failed to look up the user being enabled: %w", err))
		return
	}

	auditEvent := service.AuditEvent{
		Action:     service.AuditUserEnabled,
		Actor:      a.auditActor(g),
		Target:     service.AuditSubjectFromUser(&target),
		Reason:     reason,
		OccurredAt: time.Now(),
		Detail:     map[string]any{"previous_disable_reason": target.DisabledReason},
	}

	err = a.db.WithContext(g.Request.Context()).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"disabled_at":         nil,
				"disabled_by_user_id": nil,
				"disabled_reason":     "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("user %q", id)}
		}
		if a.audit == nil {
			return nil
		}
		return a.audit.RecordTx(tx, auditEvent)
	})
	if err != nil {
		handleError(g, err)
		return
	}

	if a.audit != nil {
		a.audit.LogOnly(auditEvent)
	}

	respondData(g, gin.H{"enabled": true})
}

// auditFeedHandler handles GET /api/admin/audit: the recent-activity feed.
//
// @Summary     View the recent audit event feed (auditor-only)
// @Description Returns the administrative audit stream newest first. The
// @Description database copy is a bounded cache of recent events; the
// @Description shipped audit log is the archive, and searching happens
// @Description there rather than here.
// @Tags        admin
// @Produce     json
// @Param       limit query int false "Page size (default 25, max 100)" example(25)
// @Param       offset query int false "Results to skip (default 0)" example(0)
// @Success     200 {object} webtypes.AuditEventsResponse "Audit events"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid paging parameters"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/audit [get]
func (a *adminController) auditFeedHandler(g *gin.Context) {
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}
	if a.audit == nil {
		respondData(g, webtypes.AuditEventsResponse{Events: []webtypes.AuditEventResponse{}})
		return
	}

	page, err := a.audit.ListRecent(g.Request.Context(), params.Limit, params.Offset)
	if err != nil {
		handleError(g, err)
		return
	}

	// One event per visit to the feed, not one per event displayed, which
	// settles the recursion question.
	a.auditRecord(g, service.AuditEvent{
		Action: service.AuditAdminAuditViewed,
		Actor:  a.auditActor(g),
		Detail: map[string]any{"scope": "feed", "limit": params.Limit, "offset": params.Offset},
	})

	respondData(g, auditPageResponse(page))
}

// userAuditHandler handles GET /api/admin/users/:id/audit: one user's
// timeline, as both actor and target.
//
// @Summary     View a user's audit timeline (auditor-only)
// @Description Returns the audit events where this user is the actor or the
// @Description target, newest first. One row serves both sides: a disable
// @Description shows on the analyst's history and on the disabled account's
// @Description page.
// @Tags        admin
// @Produce     json
// @Param       id path string true "User ID"
// @Param       limit query int false "Page size (default 25, max 100)" example(25)
// @Param       offset query int false "Results to skip (default 0)" example(0)
// @Success     200 {object} webtypes.AuditEventsResponse "Audit events"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid paging parameters"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/users/{id}/audit [get]
func (a *adminController) userAuditHandler(g *gin.Context) {
	id := g.Param("id")
	if id == "" {
		handleError(g, fmt.Errorf("user ID is required"))
		return
	}
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}
	if a.audit == nil {
		respondData(g, webtypes.AuditEventsResponse{Events: []webtypes.AuditEventResponse{}})
		return
	}

	page, err := a.audit.ListForUser(g.Request.Context(), id, params.Limit, params.Offset)
	if err != nil {
		handleError(g, err)
		return
	}

	a.auditRecord(g, service.AuditEvent{
		Action: service.AuditAdminAuditViewed,
		Actor:  a.auditActor(g),
		Target: &service.AuditSubject{UserID: id},
		Detail: map[string]any{"scope": "user_timeline"},
	})

	respondData(g, auditPageResponse(page))
}

// auditPageResponse converts a service page into the wire shape. The
// payload is passed through as already-decoded JSON rather than re-typed
// field by field, so a new action's detail reaches the UI without a wire
// type change.
func auditPageResponse(page *service.AuditPage) webtypes.AuditEventsResponse {
	out := webtypes.AuditEventsResponse{
		Events:     make([]webtypes.AuditEventResponse, 0, len(page.Events)),
		Total:      page.Total,
		NextOffset: page.NextOffset,
	}
	for _, e := range page.Events {
		out.Events = append(out.Events, webtypes.AuditEventResponse{
			ID:        e.ID,
			CreatedAt: e.CreatedAt,
			Action:    string(e.Event.Action),
			Actor:     auditSubjectResponse(e.Event.Actor),
			Target:    auditSubjectResponse(e.Event.Target),
			System:    e.Event.System,
			Reason:    e.Event.Reason,
			Detail:    e.Event.Detail,
		})
	}
	return out
}

// auditSubjectResponse converts an identity snapshot for the wire.
func auditSubjectResponse(s *service.AuditSubject) *webtypes.AuditSubjectResponse {
	if s == nil {
		return nil
	}
	return &webtypes.AuditSubjectResponse{
		UserID:   s.UserID,
		Subject:  s.Subject,
		Username: s.Username,
		Email:    s.Email,
		Groups:   s.Groups,
	}
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
// @Param       type query string false "Filter by certificate type (user/service/pam/console)" example(user)
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

// countActiveEnrollments counts the live (not yet expired) service
// enrollments a user *approved*. Deliberately keyed on user_id, which is
// provenance rather than ownership — the codes belong to their service
// accounts (see docs/proposals/enrollment-group-ownership.md), so this
// answers "what did this person create", not "what would disabling them
// take away".
//
// The error is returned rather than folded into a zero so each caller can
// decide what a missing count means to it.
func countActiveEnrollments(db *gorm.DB, userID string) (int, error) {
	var count int64
	if err := db.Model(&model.Enrollment{}).
		Where("user_id = ? AND expires_at > ?", userID, time.Now()).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// decodeStringList reads one of the JSON-in-TEXT account columns. A row that
// cannot be decoded yields an empty list rather than an error: these columns
// are populated from OIDC claims at login, and one malformed value is not a
// reason to refuse to show the account at all.
func decodeStringList(encoded string) []string {
	var out []string
	if encoded == "" {
		return out
	}
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return []string{}
	}
	return out
}

// decodeStringMap is decodeStringList for the extra-claims column, which is a
// JSON object rather than an array.
func decodeStringMap(encoded string) map[string]any {
	out := make(map[string]any)
	if encoded == "" || encoded == "{}" {
		return out
	}
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return make(map[string]any)
	}
	return out
}

// userCounts returns how many certificates a user holds and how many active
// service enrollments they have.
//
// Both are part of the detail response, so a failure to run either is
// returned rather than rendered: zero certificates and "the database did not
// say" look identical on the page but are different facts about an account,
// and only one of them is safe to act on.
func (a *adminController) userCounts(ctx context.Context, userID string) (certificates int, enrollments int, err error) {
	var certCount int64
	if err := a.db.WithContext(ctx).
		Model(&model.Certificate{}).
		Where("user_id = ?", userID).
		Count(&certCount).Error; err != nil {
		return 0, 0, fmt.Errorf("failed to count the user's certificates: %w", err)
	}

	enrollmentCount, err := countActiveEnrollments(a.db.WithContext(ctx), userID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to count the user's active enrollments: %w", err)
	}

	return int(certCount), enrollmentCount, nil
}

// disablerIDs collects the distinct users named as having disabled someone
// on this page.
func disablerIDs(users []model.User) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, u := range users {
		if u.DisabledAt == nil || u.DisabledByUserID == nil {
			continue
		}
		if id := *u.DisabledByUserID; !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// lookupUsernames resolves ids to usernames in a single query. An id with no
// surviving row is simply absent from the result.
func (a *adminController) lookupUsernames(ctx context.Context, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}

	var rows []model.User
	if err := a.db.WithContext(ctx).
		Select("id", "username").
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		names[r.ID] = r.Username
	}
	return names, nil
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
// @Success     200 {object} webtypes.AdminEnrollmentsResponse "Paged enrollment list"
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
			ServiceAccount:       row.Enrollment.ServiceAccount,
			ApprovedByUsername:   row.Approver.Username,
			ApprovedByEmail:      row.Approver.Email,
			Principals:           row.Principals,
			KeyID:                row.Enrollment.KeyID,
			PublicKeyFingerprint: row.Fingerprint,
			Options:              newCertificateOptionsResponse(row.Options),
			CreatedAt:            row.Enrollment.CreatedAt,
			ExpiresAt:            row.Enrollment.ExpiresAt,
			RetrievalCount:       row.RetrievalCount,
			LastRetrievedAt:      row.LastRetrievedAt,
			NotificationEmail:    row.Enrollment.NotificationEmail,
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
// @Description and any historical reassignments. Visible to auditors and to
// @Description holders of the enrollment's service account.
// @Tags        admin
// @Produce     json
// @Param       id path string true "Enrollment ID"
// @Success     200 {object} gin.H "Full enrollment details"
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

	// Detail views are audited; list views are not — a row per page of a
	// directory listing says nothing.
	a.auditRecord(g, service.AuditEvent{
		Action: service.AuditAdminEnrollmentViewed,
		Actor:  a.auditActor(g),
		Detail: map[string]any{"enrollment_id": g.Param("id")},
	})

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
		ServiceAccount:       detail.Enrollment.ServiceAccount,
		ApprovedByUsername:   detail.Approver.Username,
		ApprovedByEmail:      detail.Approver.Email,
		Principals:           detail.Principals,
		KeyID:                detail.Enrollment.KeyID,
		PublicKeyFingerprint: detail.Fingerprint,
		Options:              newCertificateOptionsResponse(detail.Options),
		CreatedAt:            detail.Enrollment.CreatedAt,
		ExpiresAt:            detail.Enrollment.ExpiresAt,
		RetrievalCount:       detail.Retrievals.Total,
		NotificationEmail:    detail.Enrollment.NotificationEmail,
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

// convertOptions converts a service.RequestedOptions to a webtypes.CertificateOptionsResponse.
