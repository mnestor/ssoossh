package controller

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// NewCertRequestController registers the certificate-request routes on
// group: the client-facing create/wait endpoints (open to anyone — the
// approval step is where authorization happens) and the web-UI-facing
// list/approve/deny endpoints (behind sessionAuthMiddleware).
func NewCertRequestController(group *gin.RouterGroup, certRequestService service.CertRequestProvider, sessionAuthMiddleware gin.HandlerFunc) {
	cr := &certRequestController{certRequestService: certRequestService}

	// Client-facing: create a request and wait for its outcome.
	group.POST("/certs/user", cr.createUserRequestHandler)
	group.POST("/certs/host/sign", cr.createHostSignRequestHandler)
	group.POST("/certs/service/enroll", cr.createServiceEnrollRequestHandler)
	group.GET("/certs/requests/:id/stream", cr.streamHandler)

	// Web-UI-facing: list/approve/deny pending requests.
	approvalGroup := group.Group("/certs/requests", sessionAuthMiddleware)
	approvalGroup.GET("", cr.listPendingHandler)
	approvalGroup.POST("/:id/approve", cr.approveHandler)
	approvalGroup.POST("/:id/deny", cr.denyHandler)
}

// certRequestController handles the certificate-request HTTP routes.
type certRequestController struct {
	certRequestService service.CertRequestProvider
}

// createUserRequestBody is the POST /api/certs/user request body.
type createUserRequestBody struct {
	PublicKey string `json:"public_key" binding:"required"`
}

// createUserRequestHandler handles POST /api/certs/user (`ssh login`):
// creates a pending request for an interactive user certificate.
func (cr *certRequestController) createUserRequestHandler(g *gin.Context) {
	var body createUserRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), service.NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: body.PublicKey,
		SourceIP:  g.ClientIP(),
	})
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusAccepted, gin.H{"request_id": requestID})
}

// createHostSignRequestBody is the POST /api/certs/host/sign request body.
type createHostSignRequestBody struct {
	PublicKey string `json:"public_key" binding:"required"`
	Hostname  string `json:"hostname" binding:"required"`
}

// createHostSignRequestHandler handles POST /api/certs/host/sign: creates a
// pending request for first issuance of a host certificate, gated by the
// OIDC approval chain (a human vouching for the machine — the anti-MITM
// control, see docs/ssoossh-context.md). Subsequent renewals go through
// HostController instead, authenticated by the existing certificate.
func (cr *certRequestController) createHostSignRequestHandler(g *gin.Context) {
	var body createHostSignRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), service.NewCertRequestParams{
		Type:      model.CertificateTypeHost,
		PublicKey: body.PublicKey,
		Hostname:  body.Hostname,
		SourceIP:  g.ClientIP(),
	})
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusAccepted, gin.H{"request_id": requestID})
}

// createServiceEnrollRequestBody is the POST /api/certs/service/enroll
// request body.
type createServiceEnrollRequestBody struct {
	// PublicKey may be operator-supplied (BYO key, possibly HSM/PKCS#11/
	// encrypted file — the server never sees the private half) or
	// client-generated (see docs/ssoossh-context.md, "Service enrollment").
	PublicKey string `json:"public_key" binding:"required"`
}

// createServiceEnrollRequestHandler handles POST /api/certs/service/enroll:
// creates a pending request that, once approved, becomes a
// model.Enrollment (see service.EnrollmentService) rather than an
// immediately-issued certificate.
func (cr *certRequestController) createServiceEnrollRequestHandler(g *gin.Context) {
	var body createServiceEnrollRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), service.NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: body.PublicKey,
		SourceIP:  g.ClientIP(),
	})
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusAccepted, gin.H{"request_id": requestID})
}

// streamHandler handles GET /api/certs/requests/:id/stream: the SSE
// endpoint the client waits on for approval/denial (see
// docs/ssoossh-context.md — the client never opens a listening port, so it
// learns the outcome over this stream instead).
//
// TODO: this is a route/handler-shape stub only. The actual SSE loop (set
// text/event-stream headers, call certRequestService.Wait, write events,
// handle client disconnect via g.Request.Context().Done()) needs the
// pub/sub or channel-based broker design discussed but deferred — see
// service.CertRequestService.Wait.
func (cr *certRequestController) streamHandler(g *gin.Context) {
	g.Status(http.StatusNotImplemented)
}

// listPendingHandler handles GET /api/certs/requests (web UI, behind
// sessionAuthMiddleware): lists pending requests for approval.
func (cr *certRequestController) listPendingHandler(g *gin.Context) {
	requests, err := cr.certRequestService.ListPending(g.Request.Context())
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, gin.H{"requests": requests})
}

// approveHandler handles POST /api/certs/requests/:id/approve (web UI,
// behind sessionAuthMiddleware, which guarantees middleware.IdentityContextKey
// is set by the time this handler runs).
func (cr *certRequestController) approveHandler(g *gin.Context) {
	identity, ok := g.MustGet(middleware.IdentityContextKey).(*service.Identity)
	if !ok {
		_ = g.Error(errors.New("unexpected identity type in session context")) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	certificate, err := cr.certRequestService.Approve(g.Request.Context(), g.Param("id"), identity)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, gin.H{"certificate": certificate})
}

// denyHandler handles POST /api/certs/requests/:id/deny (web UI, behind
// sessionAuthMiddleware).
func (cr *certRequestController) denyHandler(g *gin.Context) {
	if err := cr.certRequestService.Deny(g.Request.Context(), g.Param("id")); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.Status(http.StatusOK)
}
