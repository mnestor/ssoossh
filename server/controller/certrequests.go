package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// CertRequestRateLimitMiddleware holds optional per-endpoint rate limit
// middleware for certificate request creation endpoints. When the corresponding
// field is not nil, that middleware is applied to that endpoint's handler.
type CertRequestRateLimitMiddleware struct {
	User          gin.HandlerFunc
	ServiceEnroll gin.HandlerFunc
	PAM           gin.HandlerFunc
}

// orPassThrough returns h, or a handler that does nothing when h is nil, so
// a route registers once with a guaranteed middleware argument instead of
// once per branch of a nil check. A handler that returns without calling
// c.Next() is a pass-through in gin: the engine's own Next loop advances to
// the next handler either way.
func orPassThrough(h gin.HandlerFunc) gin.HandlerFunc {
	if h != nil {
		return h
	}
	return func(*gin.Context) {}
}

// rateLimits returns the per-endpoint middleware to apply, tolerating a nil
// receiver so callers that disabled rate limiting entirely need no branch.
func (m *CertRequestRateLimitMiddleware) rateLimits() (user, serviceEnroll, pam gin.HandlerFunc) {
	if m == nil {
		return orPassThrough(nil), orPassThrough(nil), orPassThrough(nil)
	}
	return orPassThrough(m.User), orPassThrough(m.ServiceEnroll), orPassThrough(m.PAM)
}

// NewCertRequestController registers the certificate-request routes on
// group: the client-facing create-and-wait endpoints (open to anyone — the
// approval step is where authorization happens) and the web-UI-facing
// approve/deny endpoints (behind sessionAuthMiddleware and csrfMiddleware).
// When rateLimitMiddleware is provided, any non-nil fields apply per-endpoint
// rate limits to the create-request routes.
func NewCertRequestController(group *gin.RouterGroup, certRequestService service.CertRequestProvider, sessionAuthMiddleware, csrfMiddleware gin.HandlerFunc, rateLimitMiddleware *CertRequestRateLimitMiddleware) {
	cr := &certRequestController{certRequestService: certRequestService}

	// Client-facing: each of these creates a request and returns two URLs —
	// events_url for the client's own SSE connection to wait on the
	// outcome, approval_url for the human to open in a browser. Both are
	// keyed by the request's own unbounded UUID (see
	// server/service/certrequest.go's CreateRequest — the ID is already the
	// unguessable capability token), which is also why GET .../events below
	// doesn't need its own auth: the ID is the credential.

	userLimit, serviceEnrollLimit, pamLimit := rateLimitMiddleware.rateLimits()
	group.POST("/certs/user", userLimit, cr.createUserRequestHandler)
	group.POST("/certs/service/enroll", serviceEnrollLimit, cr.createServiceEnrollRequestHandler)
	group.POST("/certs/pam", pamLimit, cr.createPAMRequestHandler)

	// GET .../events is the actual SSE connection: a real long-lived
	// text/event-stream response the client (or its HTTP client's SSE
	// support, e.g. resty's SSESource) connects to and waits on, separate
	// from the POST above per the SSE spec (an EventSource-style client is
	// GET-only). It's safe to reconnect any number of times — see
	// certRequestService.Wait's doc comment.
	group.GET("/certs/requests/:id/events", cr.eventsHandler)

	// Web-UI-facing: approve/deny pending requests. These are authorized by
	// the session cookie alone and carry no body, which is exactly the shape
	// a cross-site form post can forge — hence csrfMiddleware alongside the
	// session check. See middleware.CsrfMiddleware.
	approvalGroup := group.Group("/certs/requests", sessionAuthMiddleware, csrfMiddleware)
	approvalGroup.POST("/:id/approve", cr.approveHandler)
	approvalGroup.POST("/:id/deny", cr.denyHandler)

	// Web-UI-facing reads. Session-authed but not CSRF-guarded: they change
	// nothing, and CsrfMiddleware exempts safe methods anyway.
	//
	// There is deliberately no "list pending requests" endpoint. A request is
	// created by an unauthenticated call, so at that moment it belongs to
	// nobody and there is no owner to scope a list to. Listing them would
	// hand every signed-in user the IDs of everyone else's requests — and the
	// ID is the capability, so that is both an approval-hijacking primitive
	// and the raw material for a screen inviting people to approve requests
	// they did not start. Since a certificate takes the *approver's*
	// principals, being talked into approving a stranger's request is how
	// someone else's key ends up with your access. An approval reaches a
	// human exactly one way: they open the URL their own client printed.
	readGroup := group.Group("/certs/requests", sessionAuthMiddleware)
	readGroup.GET("/:id", cr.detailHandler)
}

// approvalURL is the browser path a human opens to approve a request. One
// definition so the create response and the detail response cannot drift
// apart — the frontend routes on this exact shape.
func approvalURL(requestID string) string { return "/approve/" + requestID }

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
		ApprovalURL: approvalURL(requestID),
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

// decisionContext builds the connection context for an Approve/Deny call
// from g — the deliberate header allowlist described on
// service.DecisionContext. ForwardedFor captures the raw X-Forwarded-For
// header, distinct from g.ClientIP() (used for SourceIP), which already
// resolves that header down to one trusted address via SetTrustedProxies.
func decisionContext(g *gin.Context) service.DecisionContext {
	return service.DecisionContext{
		SourceIP:       g.ClientIP(),
		UserAgent:      g.Request.UserAgent(),
		AcceptLanguage: g.GetHeader("Accept-Language"),
		ForwardedFor:   g.GetHeader("X-Forwarded-For"),
	}
}

// createRequest is the part every create-request handler shares once it has
// bound its own wire-body type and built params: it fills in SourceIP,
// creates the request, and writes the response or registers the error. Each
// handler stays its own function (rather than folding into this one) so its
// own @Router/@Success/etc swag annotations keep generating make openapi's
// per-route spec — see .claude/rules/server-api.md.
func (cr *certRequestController) createRequest(g *gin.Context, params service.NewCertRequestParams) {
	params.SourceIP = g.ClientIP()
	requestID, err := cr.certRequestService.CreateRequest(g.Request.Context(), params)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newCreateRequestResponse(requestID))
}

// createUserRequestHandler handles POST /api/certs/user (`ssh login`):
// creates a pending request for an interactive user certificate and
// returns its events/approval URLs (see newCreateRequestResponse) — it
// does not itself wait for the outcome; the client does that separately
// against EventsURL.
//
// @Summary     Create a user certificate request
// @Description Unauthenticated. Returns two relative URLs: `events_url` to wait on, and
// @Description `approval_url` for a human to open. The request ID is the capability —
// @Description it is an unguessable UUID, and holding it is what authorizes waiting on
// @Description the outcome.
// @Tags        client
// @Accept      json
// @Produce     json
// @Param       request body apitypes.UserRequestBody true "The public key to sign, and the options being asked for"
// @Success     200 {object} openapidoc.CreateRequestEnvelope "Request created"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, or a public key that will not parse"
// @Router      /api/certs/user [post]
func (cr *certRequestController) createUserRequestHandler(g *gin.Context) {
	var body apitypes.UserRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	cr.createRequest(g, service.NewCertRequestParams{
		Type:             model.CertificateTypeUser,
		PublicKey:        body.PublicKey,
		LocalUsername:    body.LocalUsername,
		LocalHostname:    body.LocalHostname,
		RequestedOptions: toServiceOptions(body.RequestedOptions),
	})
}

// createServiceEnrollRequestHandler handles POST /api/certs/service/enroll:
// creates a pending request that, once approved, becomes a
// model.Enrollment (see service.EnrollmentService) rather than an
// immediately-issued certificate, and returns its events/approval URLs
// (see createUserRequestHandler).
//
// @Summary     Create a service enrollment request
// @Description Approving one yields an enrollment code rather than a certificate; the
// @Description certificate comes later from `/api/certs/service/retrieve`. This is the
// @Description one path where an approval does not queue a signing job.
// @Tags        client
// @Accept      json
// @Produce     json
// @Param       request body apitypes.ServiceEnrollRequestBody true "The service public key and the options being asked for"
// @Success     200 {object} openapidoc.CreateRequestEnvelope "Request created"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, or a public key that will not parse"
// @Router      /api/certs/service/enroll [post]
func (cr *certRequestController) createServiceEnrollRequestHandler(g *gin.Context) {
	var body apitypes.ServiceEnrollRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	cr.createRequest(g, service.NewCertRequestParams{
		Type:             model.CertificateTypeService,
		PublicKey:        body.PublicKey,
		RequestedOptions: toServiceOptions(body.RequestedOptions),
	})
}

// createPAMRequestHandler handles POST /api/certs/pam: creates a pending
// request for a short-lived PAM certificate — one that authenticates a
// single local operation (e.g. `sudo`) to pam_ssoossh rather than an
// interactive SSH session — and returns its events/approval URLs (see
// createUserRequestHandler). Username is the local account being
// authenticated; it becomes the issued certificate's principal (see
// service.resolvePrincipals), not whatever the approving browser identity
// is called.
//
// @Summary     Create a PAM certificate request
// @Description Unauthenticated. Username is the local account pam_ssoossh is authenticating —
// @Description that, not the approver's identity, becomes the certificate's principal. Set
// @Description `cert_options.pam.require_group` to restrict who may approve one.
// @Tags        client
// @Accept      json
// @Produce     json
// @Param       request body apitypes.PAMRequestBody true "The public key to sign, the local username being authenticated, and the options being asked for"
// @Success     200 {object} openapidoc.CreateRequestEnvelope "Request created"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, or a public key that will not parse"
// @Router      /api/certs/pam [post]
func (cr *certRequestController) createPAMRequestHandler(g *gin.Context) {
	var body apitypes.PAMRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, err)
		return
	}

	cr.createRequest(g, service.NewCertRequestParams{
		Type:             model.CertificateTypePAM,
		PublicKey:        body.PublicKey,
		Username:         body.Username,
		RequestedOptions: toServiceOptions(body.RequestedOptions),
	})
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
//
// @Summary     Wait for a request's outcome (SSE)
// @Description A real `text/event-stream`, separate from the creating POST because an
// @Description EventSource-style client is GET-only. Safe to reconnect any number of
// @Description times.
// @Description
// @Description The SSE event *name* carries the terminal status (`approved`, `denied`,
// @Description `expired`, `enrolled`, `failed`); each event's data is an envelope whose
// @Description `data` half carries the certificate, or the enrollment code together with
// @Description the service account it was approved for and when the code expires. That
// @Description shape is not in this document: the response body is a stream rather than
// @Description JSON, so there is nowhere to declare a schema for what individual events
// @Description carry.
// @Description
// @Description Unauthenticated: the request ID is the credential.
// @Tags        client
// @Produce     text/event-stream
// @Param       id path string true "The certificate request's UUID"
// @Success     200 {string} string "The stream, closed after one terminal event"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such request"
// @Failure     410 {object} openapidoc.ErrorEnvelope "The certificate was issued but is no longer available. They are never persisted, so a client that missed delivery must re-request."
// @Router      /api/certs/requests/{id}/events [get]
func (cr *certRequestController) eventsHandler(g *gin.Context) {
	outcome, err := cr.certRequestService.Wait(g.Request.Context(), g.Param("id"))
	if err != nil {
		handleError(g, err)
		return
	}

	result := apitypes.CertificateResult{
		Certificate:    outcome.Certificate,
		Code:           outcome.Code,
		ServiceAccount: outcome.ServiceAccount,
	}
	// Only sent when there is one to send: an approved or denied outcome
	// has no enrollment behind it, and the zero time on the wire would read
	// as an expiry in the year 1.
	if !outcome.ExpiresAt.IsZero() {
		expiresAt := outcome.ExpiresAt
		result.ExpiresAt = &expiresAt
	}

	// Enveloped like every other JSON body this API emits, so a consumer
	// has one decode path rather than a special case for the stream. It
	// also leaves somewhere for a per-event error to go: the "failed"
	// status currently carries no detail.
	g.SSEvent(string(outcome.Status), apitypes.Envelope[apitypes.CertificateResult]{Data: result})
}

// approveHandler handles POST /api/certs/requests/:id/approve (web UI,
// behind sessionAuthMiddleware, which guarantees middleware.IdentityContextKey
// is set by the time this handler runs).
//
// @Summary     Approve a request
// @Description Session-authed and CSRF-guarded. Publishes a signing job; the certificate
// @Description reaches the client over its own SSE stream, not in this response.
// @Description
// @Description Approving a service-type request requires a body naming which of the
// @Description approver's own service accounts the certificate is for; that account
// @Description becomes the certificate principal. Other types take no body.
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       id path string true "The certificate request's UUID"
// @Param       request body webtypes.ApproveRequestBody false "Service-account selection (service-type requests only)"
// @Success     200 {object} openapidoc.ApproveEnvelope "Queued for signing"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Bound to another user, or a cross-origin call"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such request"
// @Security    sessionCookie
// @Router      /api/certs/requests/{id}/approve [post]
func (cr *certRequestController) approveHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	// The body is optional: only service-type and user-type approvals carry
	// one (the chosen service account and/or principals). An absent or empty
	// body binds to the zero value rather than erroring, so PAM approvals
	// stay body-less.
	var body webtypes.ApproveRequestBody
	if g.Request.ContentLength > 0 {
		if err := g.ShouldBindJSON(&body); err != nil {
			handleError(g, err)
			return
		}
	}

	selection := service.ApprovalSelection{
		ServiceAccount:    body.ServiceAccount,
		Principals:        body.Principals,
		NotificationEmail: body.NotificationEmail,
	}
	if err := cr.certRequestService.Approve(g.Request.Context(), g.Param("id"), identity, decisionContext(g), selection); err != nil {
		handleError(g, err)
		return
	}

	respondData(g, apitypes.ApproveResponse{Status: "signing"})
}

// denyHandler handles POST /api/certs/requests/:id/deny (web UI, behind
// sessionAuthMiddleware).
//
// @Summary     Deny a request
// @Description Session-authed and CSRF-guarded. The waiting client is told over its own
// @Description SSE stream.
// @Tags        web
// @Produce     json
// @Param       id path string true "The certificate request's UUID"
// @Success     200 {object} openapidoc.DenyEnvelope "Denied"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Bound to another user, or a cross-origin call"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such request"
// @Security    sessionCookie
// @Router      /api/certs/requests/{id}/deny [post]
func (cr *certRequestController) denyHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	if err := cr.certRequestService.Deny(g.Request.Context(), g.Param("id"), identity, decisionContext(g)); err != nil {
		handleError(g, err)
		return
	}

	// A body rather than a bare 200, so this response is shaped like every
	// other one — a caller that always decodes an envelope should not have
	// to special-case deny.
	respondData(g, apitypes.DenyResponse{Status: string(model.CertificateRequestStatusDenied)})
}

// detailHandler returns what the caller would be approving, and binds the
// request to them — this is the approval page's data source, and the first
// authenticated touch a request gets. See service.CertRequestService.Detail.
//
// @Summary     What the caller would be approving
// @Description The approval page's data source.
// @Description
// @Description Returns `requested` and `granted` separately on purpose: server config is
// @Description the outer bound on every option and trims rather than rejects, so a human
// @Description has to be able to see what is being dropped before they approve.
// @Description
// @Description **This binds the request to the caller.** It is the first authenticated
// @Description touch a request gets, so opening the page claims it; a different user gets
// @Description 403 here rather than after clicking approve.
// @Tags        web
// @Produce     json
// @Param       id path string true "The certificate request's UUID"
// @Success     200 {object} openapidoc.RequestDetailEnvelope "Request detail"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Already bound to another user"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such request"
// @Security    sessionCookie
// @Router      /api/certs/requests/{id} [get]
func (cr *certRequestController) detailHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	detail, err := cr.certRequestService.Detail(g.Request.Context(), g.Param("id"), identity)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newRequestDetailResponse(detail))
}
