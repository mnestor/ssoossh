package controller

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/service"
)

// NewEnrollmentController registers the service-certificate retrieval
// route on group. When retrieveRateLimitMiddleware is provided, it is
// applied to the /certs/service/retrieve endpoint to protect against
// code brute-forcing.
func NewEnrollmentController(group *gin.RouterGroup, enrollmentService service.EnrollmentProvider, retrieveRateLimitMiddleware gin.HandlerFunc) {
	e := &enrollmentController{enrollmentService: enrollmentService}

	if retrieveRateLimitMiddleware != nil {
		group.POST("/certs/service/retrieve", retrieveRateLimitMiddleware, e.retrieveHandler)
	} else {
		group.POST("/certs/service/retrieve", e.retrieveHandler)
	}
}

// enrollmentController handles the service-enrollment HTTP routes.
type enrollmentController struct {
	enrollmentService service.EnrollmentProvider
}

// retrieveHandler handles POST /api/certs/service/retrieve (`service
// retrieve`): redeems an enrollment code for a signed service certificate.
//
// @Summary     Redeem an enrollment code for a certificate
// @Description Not implemented yet (delivery phase 8): the service behind this returns
// @Description an error, so the route exists and answers 500 rather than issuing
// @Description anything.
// @Tags        client
// @Accept      json
// @Produce     json
// @Param       request body apitypes.RetrieveRequestBody true "The enrollment code to redeem"
// @Success     200 {object} openapidoc.RetrieveEnvelope "The signed service certificate"
// @Failure     500 {object} openapidoc.ErrorEnvelope "Not implemented"
// @Router      /api/certs/service/retrieve [post]
func (e *enrollmentController) retrieveHandler(g *gin.Context) {
	var body apitypes.RetrieveRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	certificate, err := e.enrollmentService.Retrieve(g.Request.Context(), body.Code)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, apitypes.RetrieveResponse{Certificate: certificate})
}

// ExtractEnrollmentCodeForRateLimit reads the enrollment code from the request
// body without consuming it, for use in the CodeBucket rate limiter. Returns
// an empty string if the body is malformed or the field is missing. Safe to
// call multiple times on the same context; gin buffers the raw body so each
// call can re-read it.
func ExtractEnrollmentCodeForRateLimit(c *gin.Context) string {
	var body apitypes.RetrieveRequestBody
	rawData, err := c.GetRawData()
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(rawData, &body); err != nil {
		return ""
	}
	return body.Code
}
