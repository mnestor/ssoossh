package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/service"
)

// NewEnrollmentController registers the service-certificate retrieval
// route on group.
func NewEnrollmentController(group *gin.RouterGroup, enrollmentService service.EnrollmentProvider) {
	e := &enrollmentController{enrollmentService: enrollmentService}
	group.POST("/certs/service/retrieve", e.retrieveHandler)
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
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	certificate, err := e.enrollmentService.Retrieve(g.Request.Context(), body.Code)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	respondData(g, apitypes.RetrieveResponse{Certificate: certificate})
}
