package controller

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// NewNSSLookupController registers the four NSS directory lookups under
// /nss on group, each behind gate, which authenticates the fleet key and
// limits the request before the handler runs. lookups is nil when NSS is
// disabled; gate then answers nss_disabled before a handler is reached.
//
// The name routes are catch-alls so a name holding a "/" reaches the
// validator and is answered 400 nss_bad_request rather than falling
// through to a 404 the daemon cannot tell from a miss.
//
// The handlers' "@Security nssKeyID || nssKeySecret" reads as a choice but
// is not one: swag v2.0.0-rc5's OpenAPI 3 parser puts every name on one
// @Security line into a single requirement object, which OpenAPI defines
// as all of them together. It is how this swag spells "both headers";
// "&&" comes out as one scheme literally named "nssKeyID && nssKeySecret".
func NewNSSLookupController(group *gin.RouterGroup, lookups *service.NSSLookupService, gate *middleware.NSSFleetKeyMiddleware) {
	n := &nssLookupController{lookups: lookups}

	nss := group.Group("/nss")
	nss.GET("/user/name/*name", gate.Lookup(service.NSSKindUser, service.NSSByName), n.userByNameHandler)
	nss.GET("/user/uid/*uid", gate.Lookup(service.NSSKindUser, service.NSSByID), n.userByUIDHandler)
	nss.GET("/group/name/*name", gate.Lookup(service.NSSKindGroup, service.NSSByName), n.groupByNameHandler)
	nss.GET("/group/gid/*gid", gate.Lookup(service.NSSKindGroup, service.NSSByID), n.groupByGIDHandler)
}

// nssLookupController answers the lookups from the NSS lookup service.
type nssLookupController struct {
	lookups *service.NSSLookupService
}

// pathValue reads a catch-all parameter without its leading slash.
func pathValue(g *gin.Context, name string) string {
	return strings.TrimPrefix(g.Param(name), "/")
}

// userByNameHandler handles GET /api/nss/user/name/{name}.
//
// @Summary     Look a POSIX user up by name
// @Description Resolves a login name, matched byte for byte, to the account a host serves: uid, primary gid
// @Description (after nss.auto_private_groups), every group with its name, home, shell, gecos and a status
// @Description derived on read. A disabled or deleted account is served with nss.disabled_shell. Called by
// @Description ssoosshd-nss with a fleet key. 404 nss_not_found is the one answer a host may cache as a miss.
// @Tags        nss
// @Produce     json
// @Param       name path string true "The login name, by the relaxed POSIX rules nss-systemd applies"
// @Success     200 {object} openapidoc.NSSUserEnvelope "The account"
// @Failure     400 {object} openapidoc.ErrorEnvelope "nss_bad_request: the name fails validation"
// @Failure     401 {object} openapidoc.ErrorEnvelope "nss_unauthorized: the fleet key is missing, unknown, revoked, expired or wrong"
// @Failure     403 {object} openapidoc.ErrorEnvelope "nss_forbidden: the source address is outside nss.source_allow_list"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_not_found, or nss_disabled when nss.enabled is false"
// @Failure     429 {object} openapidoc.ErrorEnvelope "nss_rate_limited, with Retry-After"
// @Failure     503 {object} openapidoc.ErrorEnvelope "unavailable: the name is not in the database and the directory could not be asked"
// @Security    nssKeyID || nssKeySecret
// @Router      /api/nss/user/name/{name} [get]
func (n *nssLookupController) userByNameHandler(g *gin.Context) {
	name := pathValue(g, "name")
	n.answerUser(g, name, func(ctx context.Context) (*apitypes.NSSUserResponse, error) {
		return n.lookups.UserByName(ctx, name)
	})
}

// userByUIDHandler handles GET /api/nss/user/uid/{uid}.
//
// @Summary     Look a POSIX user up by uid
// @Description The same answer as the lookup by name, for a uid. Called by ssoosshd-nss with a fleet key.
// @Tags        nss
// @Produce     json
// @Param       uid path int true "The uid, a decimal uint32"
// @Success     200 {object} openapidoc.NSSUserEnvelope "The account"
// @Failure     400 {object} openapidoc.ErrorEnvelope "nss_bad_request: the uid is not a decimal uint32"
// @Failure     401 {object} openapidoc.ErrorEnvelope "nss_unauthorized: the fleet key is missing, unknown, revoked, expired or wrong"
// @Failure     403 {object} openapidoc.ErrorEnvelope "nss_forbidden: the source address is outside nss.source_allow_list"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_not_found, or nss_disabled when nss.enabled is false"
// @Failure     429 {object} openapidoc.ErrorEnvelope "nss_rate_limited, with Retry-After"
// @Failure     503 {object} openapidoc.ErrorEnvelope "unavailable: the uid is not in the database and the directory could not be asked"
// @Security    nssKeyID || nssKeySecret
// @Router      /api/nss/user/uid/{uid} [get]
func (n *nssLookupController) userByUIDHandler(g *gin.Context) {
	uid, err := service.ParseNSSID(pathValue(g, "uid"))
	n.answerUser(g, "", func(ctx context.Context) (*apitypes.NSSUserResponse, error) {
		if err != nil {
			return nil, err
		}
		return n.lookups.UserByUID(ctx, uid)
	})
}

// groupByNameHandler handles GET /api/nss/group/name/{name}.
//
// @Summary     Look a POSIX group up by name
// @Description Resolves a group name, matched byte for byte, to its gid and member names, capped at
// @Description nss.max_group_members with members_truncated set when the cap applies. An account's private
// @Description group under nss.auto_private_groups resolves too, with no members. A tombstoned group resolves
// @Description with no members. Called by ssoosshd-nss with a fleet key.
// @Tags        nss
// @Produce     json
// @Param       name path string true "The group name, by the relaxed POSIX rules nss-systemd applies"
// @Success     200 {object} openapidoc.NSSGroupEnvelope "The group"
// @Failure     400 {object} openapidoc.ErrorEnvelope "nss_bad_request: the name fails validation"
// @Failure     401 {object} openapidoc.ErrorEnvelope "nss_unauthorized: the fleet key is missing, unknown, revoked, expired or wrong"
// @Failure     403 {object} openapidoc.ErrorEnvelope "nss_forbidden: the source address is outside nss.source_allow_list"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_not_found, or nss_disabled when nss.enabled is false"
// @Failure     429 {object} openapidoc.ErrorEnvelope "nss_rate_limited, with Retry-After"
// @Failure     503 {object} openapidoc.ErrorEnvelope "unavailable: the name is not in the database and the directory could not be asked"
// @Security    nssKeyID || nssKeySecret
// @Router      /api/nss/group/name/{name} [get]
func (n *nssLookupController) groupByNameHandler(g *gin.Context) {
	name := pathValue(g, "name")
	n.answerGroup(g, name, func(ctx context.Context) (*apitypes.NSSGroupResponse, error) {
		return n.lookups.GroupByName(ctx, name)
	})
}

// groupByGIDHandler handles GET /api/nss/group/gid/{gid}.
//
// @Summary     Look a POSIX group up by gid
// @Description The same answer as the lookup by name, for a gid. Called by ssoosshd-nss with a fleet key.
// @Tags        nss
// @Produce     json
// @Param       gid path int true "The gid, a decimal uint32"
// @Success     200 {object} openapidoc.NSSGroupEnvelope "The group"
// @Failure     400 {object} openapidoc.ErrorEnvelope "nss_bad_request: the gid is not a decimal uint32"
// @Failure     401 {object} openapidoc.ErrorEnvelope "nss_unauthorized: the fleet key is missing, unknown, revoked, expired or wrong"
// @Failure     403 {object} openapidoc.ErrorEnvelope "nss_forbidden: the source address is outside nss.source_allow_list"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_not_found, or nss_disabled when nss.enabled is false"
// @Failure     429 {object} openapidoc.ErrorEnvelope "nss_rate_limited, with Retry-After"
// @Failure     503 {object} openapidoc.ErrorEnvelope "unavailable: the gid is not in the database and the directory could not be asked"
// @Security    nssKeyID || nssKeySecret
// @Router      /api/nss/group/gid/{gid} [get]
func (n *nssLookupController) groupByGIDHandler(g *gin.Context) {
	gid, err := service.ParseNSSID(pathValue(g, "gid"))
	n.answerGroup(g, "", func(ctx context.Context) (*apitypes.NSSGroupResponse, error) {
		if err != nil {
			return nil, err
		}
		return n.lookups.GroupByGID(ctx, gid)
	})
}

// answerUser runs a user lookup, reports how it ended to the gate, and
// writes the account with the key that asked.
func (n *nssLookupController) answerUser(g *gin.Context, name string, lookup func(context.Context) (*apitypes.NSSUserResponse, error)) {
	if n.lookups == nil {
		// not covered: the gate answers nss_disabled before this handler
		// whenever the lookups are not built.
		handleError(g, errorresponses.NSSDisabled())
		return
	}
	user, err := lookup(g.Request.Context())
	middleware.SetNSSLookupResult(g, nssLookupResult(err), name)
	if err != nil {
		handleError(g, err)
		return
	}
	user.KeyID = middleware.NSSVerifiedKeyID(g)
	respondData(g, user)
}

// answerGroup runs a group lookup the way answerUser runs a user lookup.
func (n *nssLookupController) answerGroup(g *gin.Context, name string, lookup func(context.Context) (*apitypes.NSSGroupResponse, error)) {
	if n.lookups == nil {
		// not covered: the gate answers nss_disabled before this handler
		// whenever the lookups are not built.
		handleError(g, errorresponses.NSSDisabled())
		return
	}
	group, err := lookup(g.Request.Context())
	middleware.SetNSSLookupResult(g, nssLookupResult(err), name)
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, group)
}

// nssLookupResult names how a lookup ended, for the metrics.
func nssLookupResult(err error) string {
	if err == nil {
		return service.NSSResultHit
	}
	var nssErr *errorresponses.NSSError
	if errors.As(err, &nssErr) {
		switch nssErr.Code {
		case apitypes.ErrorCodeNSSNotFound:
			return service.NSSResultMiss
		case apitypes.ErrorCodeNSSBadRequest:
			return service.NSSResultBadRequest
		}
	}
	return service.NSSResultError
}
