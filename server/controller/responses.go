package controller

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// Converters from the server's internal types to the web UI's wire shapes.
// The shapes themselves live in server/webtypes, which is what tygo reads to
// generate the frontend's TypeScript — keeping them in a package of their
// own means the generator sees wire types and nothing else. See docs/api.md
// for the wire contract.
//
// New endpoints use the {data, error} envelope from
// .claude/rules/server-api.md. The older client-facing endpoints
// (create/approve/events) return bare payloads instead; they are left alone
// because internal/api already depends on their shape.

// respondData writes payload in the success half of the {data, error}
// envelope. The error half is written by middleware.ErrorHandlerMiddleware,
// which is why handlers here return errors via c.Error rather than
// responding directly.
func respondData(c *gin.Context, payload any) {
	c.JSON(http.StatusOK, gin.H{"data": payload, "error": nil})
}

// handleError registers err on g for middleware.ErrorHandlerMiddleware to
// write into the error half of the {data, error} envelope. Callers still
// need their own `return` immediately after — this only registers the
// error, it doesn't stop the handler.
func handleError(g *gin.Context, err error) {
	_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
}

// orEmpty returns s, or a non-nil empty slice if s is nil — for wire-shape
// fields that must serialize as [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// newPageMeta converts a page request and its total row count to the wire
// shape every paged admin/auditor list carries. Page and PageCount are
// derived here rather than in each list UI so the boundaries — an empty list,
// a total that is an exact multiple of the page size — read the same way
// everywhere.
func newPageMeta(p paging.Params, total int64) webtypes.PageMeta {
	return webtypes.PageMeta{
		Total:     total,
		Limit:     p.Limit,
		Offset:    p.Offset,
		Page:      p.PageNumber(),
		PageCount: p.PageCount(total),
	}
}

// newCurrentUserResponse converts the session identity to its wire shape.
// IsAuditor is display-only: the server re-checks GrantsAuditor on every
// auditor-scoped read, so the UI hiding or showing an affordance changes
// nothing about what this session can actually fetch. Extra is hydrated from
// the users table by subject, since it is a stored attribute independent of
// the session. Malformed JSON degrades to empty rather than erroring.
func newCurrentUserResponse(identity *service.Identity, c *config.Config, db any, subject string) webtypes.CurrentUserResponse {
	extra := hydrateExtraFields(db, subject)
	return webtypes.CurrentUserResponse{
		Subject:         identity.Subject,
		Username:        identity.Username,
		Email:           identity.Email,
		Groups:          orEmpty(identity.Groups),
		OtherAccounts:   orEmpty(identity.OtherAccounts),
		ServiceAccounts: orEmpty(identity.ServiceAccounts),
		Extra:           extra,
		IsAuditor:       c.Admin.GrantsAuditor(identity.Groups),
	}
}

// hydrateExtraFields queries the users table for the given subject and decodes
// its ExtraFields JSON. Returns an empty map if db is nil, no row exists for
// the subject, or the JSON is malformed (warning logged). Always returns a
// non-nil map.
func hydrateExtraFields(db any, subject string) map[string]any {
	if db == nil {
		return make(map[string]any)
	}

	var user model.User

	// Try to call a method on db to load the user. The real *gorm.DB
	// implementation chains Where().First(); test mocks can do the same.
	if userGetter, ok := db.(interface {
		GetUser(string, *model.User) error
	}); ok {
		// Test mock interface.
		if err := userGetter.GetUser(subject, &user); err != nil {
			return make(map[string]any)
		}
	} else if gormDB, ok := db.(*gorm.DB); ok {
		// Real *gorm.DB.
		gormDB.Where("subject = ?", subject).First(&user)
	} else {
		// Unknown type, return empty.
		return make(map[string]any)
	}

	if user.Subject == "" {
		return make(map[string]any)
	}

	// Decode the ExtraFields JSON.
	if user.ExtraFields == "" {
		return make(map[string]any)
	}

	var extra map[string]any
	if err := json.Unmarshal([]byte(user.ExtraFields), &extra); err != nil {
		slog.Warn("failed to decode user's stored extra fields", slog.String("error", err.Error()))
		return make(map[string]any)
	}

	if extra == nil {
		return make(map[string]any)
	}
	return extra
}

// newRequestDetailResponse converts a service.RequestDetail to its wire
// shape.
func newRequestDetailResponse(d *service.RequestDetail) webtypes.RequestDetailResponse {
	resp := webtypes.RequestDetailResponse{
		ID:            d.Request.ID,
		Type:          d.Request.Type,
		Status:        d.Request.Status,
		SourceIP:      d.Request.SourceIP,
		LocalUsername: d.Request.LocalUsername,
		LocalHostname: d.Request.LocalHostname,
		PublicKey:     d.Request.PublicKey,
		Principals:    orEmpty(d.Principals),
		ValidSeconds:  int(d.ValidDuration.Seconds()),
		Requested:     newCertificateOptionsResponse(d.Requested),
		Granted:       newCertificateOptionsResponse(d.Narrowed),
		CreatedAt:     d.Request.CreatedAt,
		ApprovalURL:   approvalURL(d.Request.ID),
		// Detail binds the request to the caller, so reaching this point at
		// all means they own it. Present as a field anyway so the UI does
		// not have to infer ownership from the absence of an error.
		IsOwnedByYou:  true,
		AlreadyClosed: d.Request.Status != model.CertificateRequestStatusPending,
	}

	if d.Decision != nil {
		setDecisionFields(&resp, d.Decision)
	}

	return resp
}

// setDecisionFields maps decision's fields onto resp's Decided* fields. Who
// ever sees a populated response is documented on
// webtypes.RequestDetailResponse's doc comment — the request-detail
// endpoint only ever shows this to the single identity bound to the
// request.
func setDecisionFields(resp *webtypes.RequestDetailResponse, decision *model.CertificateRequestDecision) {
	resp.DecidedByOutcome = string(decision.Outcome)
	resp.DecidedBySubject = decision.Subject
	resp.DecidedByUsername = decision.Username
	resp.DecidedByEmail = decision.Email
	resp.DecidedSourceIP = decision.SourceIP
	resp.DecidedUserAgent = decision.UserAgent
	resp.DecidedAcceptLanguage = decision.AcceptLanguage
	resp.DecidedForwardedFor = decision.ForwardedFor
	decidedAt := decision.DecidedAt
	resp.DecidedAt = &decidedAt

	resp.DecidedByGroups = decodeDecisionStringList("groups", decision.Groups)
	resp.DecidedByOtherAccounts = decodeDecisionStringList("other_accounts", decision.OtherAccounts)
	resp.DecidedByServiceAccounts = decodeDecisionStringList("service_accounts", decision.ServiceAccounts)
}

// setDecisionFieldsOnCertificate maps decision's fields onto a certificate
// response's Decided* fields.
func setDecisionFieldsOnCertificate(resp *webtypes.CertificateResponse, decision *model.CertificateRequestDecision) {
	resp.DecidedByOutcome = string(decision.Outcome)
	resp.DecidedBySubject = decision.Subject
	resp.DecidedByUsername = decision.Username
	resp.DecidedByEmail = decision.Email
	resp.DecidedSourceIP = decision.SourceIP
	resp.DecidedUserAgent = decision.UserAgent
	resp.DecidedAcceptLanguage = decision.AcceptLanguage
	resp.DecidedForwardedFor = decision.ForwardedFor
	decidedAt := decision.DecidedAt
	resp.DecidedAt = &decidedAt

	resp.DecidedByGroups = decodeDecisionStringList("groups", decision.Groups)
	resp.DecidedByOtherAccounts = decodeDecisionStringList("other_accounts", decision.OtherAccounts)
	resp.DecidedByServiceAccounts = decodeDecisionStringList("service_accounts", decision.ServiceAccounts)
}

// decodeDecisionStringList decodes a JSON-encoded []string column from
// model.CertificateRequestDecision. A parse failure logs and returns nil
// rather than failing the whole response — this is audit data, not a
// security decision on the read path.
func decodeDecisionStringList(field, raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		slog.Error("failed to decode certificate request decision field", "field", field, "error", err)
		return nil
	}
	return out
}

// newServiceEnrollmentsResponse converts the caller's enrollments to their
// wire shape.
//
// Enrollment.Code is present on every row this walks and is deliberately
// not read: see webtypes.ServiceEnrollmentResponse for why the page that
// consumes this must never be able to show it.
func newServiceEnrollmentsResponse(enrollments []service.ServiceEnrollment) webtypes.ServiceEnrollmentsResponse {
	out := webtypes.ServiceEnrollmentsResponse{
		Enrollments: make([]webtypes.ServiceEnrollmentResponse, 0, len(enrollments)),
	}
	for _, e := range enrollments {
		out.Enrollments = append(out.Enrollments, newServiceEnrollmentResponse(e))
	}
	return out
}

// newServiceEnrollmentResponse converts one enrollment to its wire shape.
func newServiceEnrollmentResponse(e service.ServiceEnrollment) webtypes.ServiceEnrollmentResponse {
	resp := webtypes.ServiceEnrollmentResponse{
		ID:                   e.Enrollment.ID,
		ServiceAccount:       e.Enrollment.ServiceAccount,
		ApprovedByUsername:   e.ApproverUsername,
		Principals:           orEmpty(e.Principals),
		KeyID:                e.Enrollment.KeyID,
		PublicKeyFingerprint: e.Fingerprint,
		Options:              newCertificateOptionsResponse(e.Options),
		CreatedAt:            e.Enrollment.CreatedAt,
		ExpiresAt:            e.Enrollment.ExpiresAt,
		FirstRedeemedAt:      e.Enrollment.RedeemedAt,
		LastRetrievedAt:      e.LastRetrievedAt,
		RetrievalCount:       e.RetrievalCount,
	}

	if e.Enrollment.CertificateRequestID != nil {
		resp.CertificateRequestID = *e.Enrollment.CertificateRequestID
	}

	// Left absent for a row predating the split between the code's lifetime
	// and the certificate's, where ExpiresAt bounded both — the same
	// distinction EnrollmentService.Retrieve honors. Reporting the code's
	// window as the certificate's would be a lie about what a redemption
	// hands out.
	if e.Enrollment.CertificateDurationSeconds != nil {
		seconds := int(*e.Enrollment.CertificateDurationSeconds)
		resp.CertificateValidSeconds = &seconds
	}

	return resp
}

// newCertificateOptionsResponse converts resolved options to their wire
// shape, normalizing nil slices to empty ones.
func newCertificateOptionsResponse(o service.RequestedOptions) webtypes.CertificateOptionsResponse {
	return webtypes.CertificateOptionsResponse{
		Extensions:      orEmpty(o.Extensions),
		ForceCommand:    o.ForceCommand,
		SourceAddresses: o.SourceAddresses,
		NoTouchRequired: o.NoTouchRequired,
	}
}

// newCertificateResponsesWithDecisions converts certificate+decision pairs to
// their wire shape, including decision-audit data when available.
func newCertificateResponsesWithDecisions(certsWithDecisions []service.CertificateWithDecision) []webtypes.CertificateResponse {
	out := make([]webtypes.CertificateResponse, 0, len(certsWithDecisions))
	for _, cd := range certsWithDecisions {
		resp := webtypes.CertificateResponse{
			ID:           cd.Certificate.ID,
			Type:         cd.Certificate.Type,
			SerialNumber: cd.Certificate.SerialNumber,
			KeyID:        cd.Certificate.KeyID,
			Principals:   cd.Certificate.Principals,
			Fingerprint:  cd.Certificate.PublicKeyFingerprint,
			IssuedAt:     cd.Certificate.IssuedAt,
			ExpiresAt:    cd.Certificate.ExpiresAt,
		}

		// Populate decision fields if a decision record exists.
		if cd.Decision != nil {
			setDecisionFieldsOnCertificate(&resp, cd.Decision)
		}

		// Only a service certificate has one. It is what says where this
		// particular certificate was fetched from, as opposed to where the
		// code it came from was approved.
		if cd.Retrieval != nil {
			retrievedAt := cd.Retrieval.RetrievedAt
			resp.RetrievedSourceIP = cd.Retrieval.SourceIP
			resp.RetrievedAt = &retrievedAt
			resp.EnrollmentID = cd.Retrieval.EnrollmentID
		}

		out = append(out, resp)
	}
	return out
}

// newCertificateListResponse converts paginated certificate+decision pairs to
// their wire shape, wrapped in a CertificateListResponse with cursor.
func newCertificateListResponse(certsWithDecisions []service.CertificateWithDecision, nextCursor *string) webtypes.CertificateListResponse {
	return webtypes.CertificateListResponse{
		Certificates: newCertificateResponsesWithDecisions(certsWithDecisions),
		NextCursor:   nextCursor,
	}
}

// newCertificateResponseFromWithDecision converts a single certificate+decision pair
// to its wire shape for the detail endpoint.
func newCertificateResponseFromWithDecision(cd service.CertificateWithDecision) webtypes.CertificateResponse {
	resp := webtypes.CertificateResponse{
		ID:           cd.Certificate.ID,
		Type:         cd.Certificate.Type,
		SerialNumber: cd.Certificate.SerialNumber,
		KeyID:        cd.Certificate.KeyID,
		Principals:   cd.Certificate.Principals,
		Fingerprint:  cd.Certificate.PublicKeyFingerprint,
		IssuedAt:     cd.Certificate.IssuedAt,
		ExpiresAt:    cd.Certificate.ExpiresAt,
	}

	if cd.Decision != nil {
		setDecisionFieldsOnCertificate(&resp, cd.Decision)
	}

	if cd.Retrieval != nil {
		retrievedAt := cd.Retrieval.RetrievedAt
		resp.RetrievedSourceIP = cd.Retrieval.SourceIP
		resp.RetrievedAt = &retrievedAt
		resp.EnrollmentID = cd.Retrieval.EnrollmentID
	}

	return resp
}
