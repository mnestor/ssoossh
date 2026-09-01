package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewEnrollmentController registers the service-certificate retrieval
// route and the web UI's enrollment-list and retrieval-log routes on group. When
// retrieveRateLimitMiddleware is provided, it is applied to the
// /certs/service/retrieve endpoint to protect against code brute-forcing.
func NewEnrollmentController(group *gin.RouterGroup, enrollmentService service.EnrollmentProvider, retrieveRateLimitMiddleware gin.HandlerFunc, sessionAuthMiddleware gin.HandlerFunc) {
	e := &enrollmentController{enrollmentService: enrollmentService}

	group.POST("/certs/service/retrieve", orPassThrough(retrieveRateLimitMiddleware), e.retrieveHandler)

	group.GET("/certs/requests/:id/retrievals", sessionAuthMiddleware, e.retrievalsHandler)

	group.GET("/certs/service/enrollments", sessionAuthMiddleware, e.listHandler)

	group.PATCH("/certs/service/enrollments/:id/notification-email", sessionAuthMiddleware, e.setNotificationEmailHandler)
}

// enrollmentController handles the service-enrollment HTTP routes.
type enrollmentController struct {
	enrollmentService service.EnrollmentProvider
}

// retrieveHandler handles POST /api/certs/service/retrieve (`service
// retrieve`): redeems an enrollment code for a signed service certificate.
//
// @Summary     Redeem an enrollment code for a certificate
// @Description Signs and returns a service certificate for the enrollment the code
// @Description identifies, using the public key and options fixed at approval time.
// @Description Codes are reusable until the enrollment expires; every redemption is
// @Description logged for the approving user and auditors. The certificate is valid
// @Description from now until the enrollment's expiry.
// @Tags        client
// @Accept      json
// @Produce     json
// @Param       request body apitypes.RetrieveRequestBody true "The enrollment code to redeem"
// @Success     200 {object} openapidoc.RetrieveEnvelope "The signed service certificate"
// @Failure     404 {object} openapidoc.ErrorEnvelope "Unknown or expired enrollment code"
// @Failure     500 {object} openapidoc.ErrorEnvelope "Signing failed or timed out"
// @Router      /api/certs/service/retrieve [post]
func (e *enrollmentController) retrieveHandler(g *gin.Context) {
	var body apitypes.RetrieveRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	certificate, err := e.enrollmentService.Retrieve(g.Request.Context(), body.Code, g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, apitypes.RetrieveResponse{Certificate: certificate})
}

// retrievalsHandler handles GET /api/certs/requests/:id/retrievals (web
// UI, behind sessionAuthMiddleware): the retrieval log for a service
// request's enrollment, newest first.
//
// @Summary     A service enrollment's retrieval log
// @Description Redemptions of the enrollment created from this service request —
// @Description when, from where, and whether a certificate was issued. Codes are
// @Description reusable, so the log accumulates one row per redemption; the newest 100
// @Description are returned and "total" reports how many exist. Visible to the
// @Description enrollment's approving user and to auditors.
// @Tags        web
// @Produce     json
// @Param       id path string true "The certificate request's UUID"
// @Success     200 {object} openapidoc.EnrollmentRetrievalsEnvelope "The retrieval log"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Neither the approving user nor an auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No enrollment for this request"
// @Security    sessionCookie
// @Router      /api/certs/requests/{id}/retrievals [get]
func (e *enrollmentController) retrievalsHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	log, err := e.enrollmentService.ListRetrievals(g.Request.Context(), g.Param("id"), identity)
	if err != nil {
		handleError(g, err)
		return
	}

	resp := webtypes.EnrollmentRetrievalsResponse{
		Retrievals: make([]webtypes.EnrollmentRetrievalResponse, 0, len(log.Retrievals)),
		Total:      log.Total,
	}
	for _, r := range log.Retrievals {
		resp.Retrievals = append(resp.Retrievals, webtypes.EnrollmentRetrievalResponse{
			RetrievedAt:       r.RetrievedAt,
			SourceIP:          r.SourceIP,
			CertificateSerial: r.CertificateSerial,
			Succeeded:         r.Succeeded,
		})
	}
	respondData(g, resp)
}

// listHandler handles GET /api/certs/service/enrollments (web UI, behind
// sessionAuthMiddleware): the caller's own approved service enrollments,
// newest first.
//
// @Summary     The caller's approved service enrollments
// @Description What each enrollment code will hand out and how long it stays
// @Description redeemable: the principals and options fixed at approval, the keypair it
// @Description is bound to, when it was approved, when it stops working, and how often
// @Description it has been redeemed.
// @Description
// @Description The code itself is never returned. `service enroll` prints it once, and
// @Description a browser session is not a way to mint service certificates.
// @Description
// @Description Scoped by the caller's users row, with no parameter to widen it.
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.ServiceEnrollmentsEnvelope "Approved enrollments, newest first"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Security    sessionCookie
// @Router      /api/certs/service/enrollments [get]
func (e *enrollmentController) listHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	enrollments, err := e.enrollmentService.ListForIdentity(g.Request.Context(), identity)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newServiceEnrollmentsResponse(enrollments))
}

// setNotificationEmailHandler handles PATCH
// /api/certs/service/enrollments/:id/notification-email (web UI, behind
// sessionAuthMiddleware).
//
// @Summary     Set an enrollment's notification address
// @Description Points every notification about this enrollment at one address instead
// @Description of fanning out to every holder of its service account. An empty value
// @Description clears it and restores fan-out.
// @Description
// @Description This exists for the cases fan-out cannot serve: a service account whose
// @Description holders have never logged in reaches nobody, an identity provider that
// @Description releases no email claim silences every holder, and a large holder set
// @Description turns every redemption into a mailshot where a team alias would do.
// @Description
// @Description Allowed to any holder of the enrollment's service account, and to SOC
// @Description operators. Auditor is not enough: it is a read role, and this write
// @Description redirects every future message about a credential.
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       id path string true "The enrollment's UUID"
// @Param       request body webtypes.SetNotificationEmailRequestBody true "The address, or empty to clear it"
// @Success     200 {object} openapidoc.SetNotificationEmailEnvelope "The stored address"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Not a valid email address"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not a holder of the service account"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such enrollment"
// @Security    sessionCookie
// @Router      /api/certs/service/enrollments/{id}/notification-email [patch]
func (e *enrollmentController) setNotificationEmailHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	var body webtypes.SetNotificationEmailRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	if err := e.enrollmentService.SetNotificationEmail(g.Request.Context(), g.Param("id"), identity, body.NotificationEmail); err != nil {
		handleError(g, err)
		return
	}

	// The stored value is echoed back rather than a bare acknowledgement:
	// it is trimmed server-side, so the page needs to be told what was
	// actually saved instead of assuming its own input.
	respondData(g, webtypes.SetNotificationEmailRequestBody{
		NotificationEmail: strings.TrimSpace(body.NotificationEmail),
	})
}

// ExtractEnrollmentCodeForRateLimit reads the enrollment code from the
// request body for the CodeBucket rate limiter, and puts the body back so
// the handler behind it can still bind. Returns an empty string if the body
// is malformed or the field is missing — the handler is what reports that
// to the caller, not the limiter.
//
// The restore is the whole point. gin's GetRawData is io.ReadAll over
// c.Request.Body: it neither buffers nor rewinds. Without putting the body
// back, retrieveHandler's ShouldBindJSON reads an already-drained stream and
// every redemption fails with a 500 carrying io.EOF, so `service retrieve`
// cannot redeem a code at all wherever the per-code rate limit is
// configured.
func ExtractEnrollmentCodeForRateLimit(c *gin.Context) string {
	rawData, err := c.GetRawData()
	if err != nil {
		// Partially consumed at best; hand the handler an empty body rather
		// than a half-read one, and let it produce the error.
		c.Request.Body = io.NopCloser(bytes.NewReader(nil))
		return ""
	}
	// Restored before the parse below can return early, so a malformed body
	// still reaches the handler and still gets the handler's 400.
	c.Request.Body = io.NopCloser(bytes.NewReader(rawData))

	var body apitypes.RetrieveRequestBody
	if err := json.Unmarshal(rawData, &body); err != nil {
		return ""
	}
	return body.Code
}
