package controller

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewEnrollmentController registers the service-certificate retrieval
// route and the web UI's retrieval-log route on group. When
// retrieveRateLimitMiddleware is provided, it is applied to the
// /certs/service/retrieve endpoint to protect against code brute-forcing.
func NewEnrollmentController(group *gin.RouterGroup, enrollmentService service.EnrollmentProvider, retrieveRateLimitMiddleware gin.HandlerFunc, sessionAuthMiddleware gin.HandlerFunc) {
	e := &enrollmentController{enrollmentService: enrollmentService}

	group.POST("/certs/service/retrieve", orPassThrough(retrieveRateLimitMiddleware), e.retrieveHandler)

	group.GET("/certs/requests/:id/retrievals", sessionAuthMiddleware, e.retrievalsHandler)
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
// @Description Every redemption of the enrollment created from this service request —
// @Description when, from where, and whether a certificate was issued. Codes are
// @Description reusable, so the log accumulates one row per redemption. Visible to the
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

	rows, err := e.enrollmentService.ListRetrievals(g.Request.Context(), g.Param("id"), identity)
	if err != nil {
		handleError(g, err)
		return
	}

	resp := webtypes.EnrollmentRetrievalsResponse{
		Retrievals: make([]webtypes.EnrollmentRetrievalResponse, 0, len(rows)),
	}
	for _, r := range rows {
		resp.Retrievals = append(resp.Retrievals, webtypes.EnrollmentRetrievalResponse{
			RetrievedAt:       r.RetrievedAt,
			SourceIP:          r.SourceIP,
			CertificateSerial: r.CertificateSerial,
			Succeeded:         r.Succeeded,
		})
	}
	respondData(g, resp)
}

// ExtractEnrollmentCodeForRateLimit reads the enrollment code from the
// request body for the CodeBucket rate limiter, and puts the body back so
// the handler behind it can still bind. Returns an empty string if the body
// is malformed or the field is missing — the handler is what reports that
// to the caller, not the limiter.
//
// The restore is the whole point. gin's GetRawData is io.ReadAll over
// c.Request.Body: it neither buffers nor rewinds, contrary to what this
// function's comment used to claim. Without putting the body back,
// retrieveHandler's ShouldBindJSON read an already-drained stream and every
// redemption failed with a 500 carrying io.EOF, so `service retrieve` could
// not redeem a code at all wherever the per-code rate limit was configured.
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
