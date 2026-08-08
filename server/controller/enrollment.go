package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

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

// retrieveRequestBody is the POST /api/certs/service/retrieve request
// body. Only the code is posted — never a public key, so a stolen code
// can't be paired with an attacker's keypair (see
// docs/ssoossh-context.md, "Service enrollment").
type retrieveRequestBody struct {
	Code string `json:"code" binding:"required"`
}

// retrieveHandler handles POST /api/certs/service/retrieve (`service
// retrieve`): redeems an enrollment code for a signed service certificate.
func (e *enrollmentController) retrieveHandler(g *gin.Context) {
	var body retrieveRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	certificate, err := e.enrollmentService.Retrieve(g.Request.Context(), body.Code)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, gin.H{"certificate": certificate})
}
