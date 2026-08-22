// Package openapidoc carries the OpenAPI scaffolding: the general API info,
// and one envelope type per response body.
//
// docs/openapi.yaml is generated from here plus the annotations on the
// handlers in server/controller — run `make openapi`. The file is no longer
// hand-edited, so the prose that used to live in it now lives in
// @Description blocks beside the handler each one describes.
//
// # Why the envelopes are spelled out
//
// Every response is the {data, error} envelope, so each one is that shape
// with a different payload. The obvious spelling is a generic —
// apitypes.Envelope[T], which the Go client already decodes into — but
// swag v2.0.0-rc5 cannot resolve a type parameter at all. Its other
// mechanism, the composition syntax Envelope{data=T}, is worse than
// unsupported: it emits every composed body as a schema named "data" and
// they overwrite each other, so two endpoints silently end up documented
// with whichever payload was generated last. Verified against rc5 before
// writing these out.
//
// So each response gets a named struct. They are never constructed —
// handlers write the success half through respondData and the error half
// through middleware.ErrorHandlerMiddleware. What they buy is that the
// payload field references the real type, so a field added to
// webtypes.RequestDetailResponse reaches the spec without anyone editing
// YAML. Only the wrapper itself is manual, and there is nothing in a
// wrapper to get wrong.
//
// Revisit when swag ships generics support: every type below collapses into
// apitypes.Envelope[T] at the annotation site.
package openapidoc

import (
	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// @title                      ssoosshd API
// @version                    0.1
// @description                SSO for SSH. Two audiences share this surface:
// @description
// @description                - **The client** (`ssoossh`, and `pam_ssoossh`) creates certificate requests and waits on the result over SSE. These endpoints are unauthenticated: a request's ID is an unguessable UUID and is itself the capability, and authorization happens at the approval step.
// @description                - **The web UI** reads and approves. Everything under a session cookie.
// @description
// @description                **Every** JSON body this API emits is the `{data, error}` envelope from `.claude/rules/server-api.md` — success and error, client-facing and web-UI-facing, including the data of each server-sent event. One decode path, no exceptions to remember.
// @description
// @description                This file is generated from Go annotations by `make openapi`. Edit the handlers, not the YAML.
// @contact.name               ssoossh
// @contact.url                https://github.com/mnestor/ssoossh
// @license.name               MIT
//
// @tag.name                   client
// @tag.description            Called by the ssoossh client, unauthenticated
// @tag.name                   web
// @tag.description            Called by the web UI, session-authenticated
// @tag.name                   auth
// @tag.description            Browser-facing OIDC redirects
//
// @securityDefinitions.apikey sessionCookie
// @in                         cookie
// @name                       ssoossh_session
// @description                The session established by /auth/callback. Sent by the browser automatically; the ssoossh client never has one, and the endpoints it calls do not require one.

// ErrorEnvelope is the body of any failed request: no payload, and the error
// half populated. Referenced by every @Failure annotation so an error
// response documents the body it actually returns rather than being
// described by its status code alone.
type ErrorEnvelope struct {
	// Data is always null on an error response.
	Data any `json:"data"`

	// Error is a human-readable message. Branch on the HTTP status rather
	// than this string; it is for humans and logs.
	Error string `json:"error" validate:"required" example:"certificate request \"9f1c…\" not found"`

	// ErrorCode is a machine-readable error classifier. One of the error_code
	// constants from internal/apitypes; use this to decide whether to retry
	// or branch on the failure mode.
	ErrorCode string `json:"error_code" validate:"required" example:"not_found"`
}

// HealthPayload is GET /healthz's body. Note it is not wrapped in an
// envelope: that endpoint predates the convention and is consumed by
// orchestrators rather than by this project's own clients.
type HealthPayload struct {
	Status string `json:"status" validate:"required" example:"ok"`
}

// LogoutEnvelope is POST /auth/logout's body.
type LogoutEnvelope struct {
	Data  LogoutPayload `json:"data" validate:"required"`
	Error *string       `json:"error"`
}

// LogoutPayload confirms the session was cleared.
type LogoutPayload struct {
	LoggedOut bool `json:"logged_out" validate:"required"`
}

// CAEnvelope is GET /api/ca's body.
type CAEnvelope struct {
	Data  apitypes.CAResponse `json:"data" validate:"required"`
	Error *string             `json:"error"`
}

// CreateRequestEnvelope is the body of all three create-a-request endpoints.
type CreateRequestEnvelope struct {
	Data  apitypes.CreateRequestResponse `json:"data" validate:"required"`
	Error *string                        `json:"error"`
}

// RetrieveEnvelope is POST /api/certs/service/retrieve's body.
type RetrieveEnvelope struct {
	Data  apitypes.RetrieveResponse `json:"data" validate:"required"`
	Error *string                   `json:"error"`
}

// ApproveEnvelope is POST /api/certs/requests/{id}/approve's body. The
// status is the request's new state, not a certificate — approval only
// queues a signing job.
type ApproveEnvelope struct {
	Data  apitypes.ApproveResponse `json:"data" validate:"required"`
	Error *string                  `json:"error"`
}

// DenyEnvelope is POST /api/certs/requests/{id}/deny's body.
type DenyEnvelope struct {
	Data  apitypes.DenyResponse `json:"data" validate:"required"`
	Error *string               `json:"error"`
}

// CurrentUserEnvelope is GET /api/users/me's body.
type CurrentUserEnvelope struct {
	Data  webtypes.CurrentUserResponse `json:"data" validate:"required"`
	Error *string                      `json:"error"`
}

// RequestDetailEnvelope is GET /api/certs/requests/{id}'s body.
type RequestDetailEnvelope struct {
	Data  webtypes.RequestDetailResponse `json:"data" validate:"required"`
	Error *string                        `json:"error"`
}

// CertificateListEnvelope is GET /api/certs's body. Uses cursor-based
// pagination with the optional "after" and "limit" query parameters.
type CertificateListEnvelope struct {
	Data  webtypes.CertificateListResponse `json:"data" validate:"required"`
	Error *string                          `json:"error"`
}
