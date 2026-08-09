package controller

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// NewCertRequestController registers the certificate-request routes on
// group: the client-facing create-and-wait endpoints (open to anyone — the
// approval step is where authorization happens) and the web-UI-facing
// approve/deny endpoints (behind sessionAuthMiddleware).
func NewCertRequestController(group *gin.RouterGroup, certRequestService service.CertRequestProvider, sessionAuthMiddleware gin.HandlerFunc) {
	cr := &certRequestController{certRequestService: certRequestService}

	// Client-facing: each of these creates a request and returns two URLs —
	// events_url for the client's own SSE connection to wait on the
	// outcome, approval_url for the human to open in a browser. Both are
	// keyed by the request's own unbounded UUID (see
	// server/service/certrequest.go's CreateRequest — the ID is already the
	// unguessable capability token), which is also why GET .../events below
	// doesn't need its own auth: the ID is the credential.
	group.POST("/certs/user", cr.createUserRequestHandler)
	group.POST("/certs/host/sign", cr.createHostSignRequestHandler)
	group.POST("/certs/service/enroll", cr.createServiceEnrollRequestHandler)

	// GET .../events is the actual SSE connection: a real long-lived
	// text/event-stream response the client (or its HTTP client's SSE
	// support, e.g. resty's SSESource) connects to and waits on, separate
	// from the POST above per the SSE spec (an EventSource-style client is
	// GET-only). It's safe to reconnect any number of times — see
	// certRequestService.Wait's doc comment.
	group.GET("/certs/requests/:id/events", cr.eventsHandler)

	// Web-UI-facing: approve/deny pending requests.
	approvalGroup := group.Group("/certs/requests", sessionAuthMiddleware)
	approvalGroup.POST("/:id/approve", cr.approveHandler)
	approvalGroup.POST("/:id/deny", cr.denyHandler)
}

// certRequestController handles the certificate-request HTTP routes.
type certRequestController struct {
	certRequestService service.CertRequestProvider
}

// newCreateRequestResponse builds the response for a newly created
// requestID. See docs/README.md for the /approve/<id> URL convention, and
// internal/apitypes.CreateRequestResponse's doc comment for why both URLs
// are relative.
func newCreateRequestResponse(requestID string) apitypes.CreateRequestResponse {
	return apitypes.CreateRequestResponse{
		RequestID:   requestID,
		EventsURL:   "/api/certs/requests/" + requestID + "/events",
		ApprovalURL: "/approve/" + requestID,
	}
}

// toServiceOptions converts the wire-contract RequestedOptions
// (internal/apitypes, shared with the client) into the server's internal
// service.RequestedOptions. Kept as an explicit conversion rather than a
// shared type so the two can evolve independently — see
// service.RequestedOptions's doc comment.
func toServiceOptions(o apitypes.RequestedOptions) service.RequestedOptions {
	return service.RequestedOptions{
		Extensions:      o.Extensions,
		ForceCommand:    o.ForceCommand,
		SourceAddresses: o.SourceAddresses,
		NoTouchRequired: o.NoTouchRequired,
	}
}

// createUserRequestHandler handles POST /api/certs/user (`ssh login`):
// creates a pending request for an interactive user certificate and
// returns its events/approval URLs (see newCreateRequestResponse) — it
// does not itself wait for the outcome; the client does that separately
// against EventsURL.
func (cr *certRequestController) createUserRequestHandler(g *gin.Context) {
	var body apitypes.UserRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), service.NewCertRequestParams{
		Type:             model.CertificateTypeUser,
		PublicKey:        body.PublicKey,
		SourceIP:         g.ClientIP(),
		RequestedOptions: toServiceOptions(body.RequestedOptions),
	})
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, newCreateRequestResponse(requestID))
}

// createHostSignRequestHandler handles POST /api/certs/host/sign: creates a
// pending request for first issuance of a host certificate, gated by the
// OIDC approval chain (a human vouching for the machine — the anti-MITM
// control, see docs/ssoossh-context.md), and returns its events/approval
// URLs (see createUserRequestHandler). Subsequent renewals go through
// HostController instead, authenticated by the existing certificate.
func (cr *certRequestController) createHostSignRequestHandler(g *gin.Context) {
	var body apitypes.HostSignRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), service.NewCertRequestParams{
		Type:             model.CertificateTypeHost,
		PublicKey:        body.PublicKey,
		Hostname:         body.Hostname,
		SourceIP:         g.ClientIP(),
		RequestedOptions: toServiceOptions(body.RequestedOptions),
	})
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, newCreateRequestResponse(requestID))
}

// createServiceEnrollRequestHandler handles POST /api/certs/service/enroll:
// creates a pending request that, once approved, becomes a
// model.Enrollment (see service.EnrollmentService) rather than an
// immediately-issued certificate, and returns its events/approval URLs
// (see createUserRequestHandler).
func (cr *certRequestController) createServiceEnrollRequestHandler(g *gin.Context) {
	var body apitypes.ServiceEnrollRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), service.NewCertRequestParams{
		Type:             model.CertificateTypeService,
		PublicKey:        body.PublicKey,
		SourceIP:         g.ClientIP(),
		RequestedOptions: toServiceOptions(body.RequestedOptions),
	})
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, newCreateRequestResponse(requestID))
}

// eventsHandler handles GET /api/certs/requests/:id/events: the client's
// (or resty SSESource's) actual SSE connection, separate from the create
// calls above. Blocks on certRequestService.Wait for :id, then writes a
// single terminal SSE event (approved/denied/expired) and returns, closing
// the connection. Blocking on Wait before writing anything means a Wait
// error (unknown ID, or the client disconnecting — see Wait's doc comment)
// still gets a normal JSON error response via ErrorHandlerMiddleware
// instead of needing to be encoded as an SSE event after the fact. Safe to
// hit repeatedly for the same :id — e.g. resty's SSESource reconnecting
// after a dropped connection — since Wait itself handles that.
func (cr *certRequestController) eventsHandler(g *gin.Context) {
	status, certificate, err := cr.certRequestService.Wait(g.Request.Context(), g.Param("id"))
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.SSEvent(string(status), apitypes.CertificateResult{Certificate: certificate})
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

	g.JSON(http.StatusOK, apitypes.CertificateResult{Certificate: certificate})
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
