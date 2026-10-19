package openapidoc

import "github.com/mnestor/ssoossh/server/webtypes"

// NSSPendingIDChangesEnvelope is GET /api/admin/nss/pending-id-changes's
// body.
type NSSPendingIDChangesEnvelope struct {
	Data  webtypes.NSSPendingIDChangesResponse `json:"data" validate:"required"`
	Error *string                              `json:"error"`
}

// NSSAcceptedUserIDChangeEnvelope is
// POST /api/admin/nss/users/{uid}/accept-id-change's body.
type NSSAcceptedUserIDChangeEnvelope struct {
	Data  webtypes.NSSAcceptedUserIDChangeResponse `json:"data" validate:"required"`
	Error *string                                  `json:"error"`
}

// NSSAcceptedGroupIDChangeEnvelope is
// POST /api/admin/nss/groups/{gid}/accept-id-change's body.
type NSSAcceptedGroupIDChangeEnvelope struct {
	Data  webtypes.NSSAcceptedGroupIDChangeResponse `json:"data" validate:"required"`
	Error *string                                   `json:"error"`
}

// NSSCollisionsEnvelope is GET /api/admin/nss/collisions's body.
type NSSCollisionsEnvelope struct {
	Data  webtypes.NSSCollisionsResponse `json:"data" validate:"required"`
	Error *string                        `json:"error"`
}

// NSSPurgedTombstoneEnvelope is the body of both tombstone purges,
// DELETE /api/admin/nss/users/{uid}/tombstone and
// /api/admin/nss/groups/{gid}/tombstone.
type NSSPurgedTombstoneEnvelope struct {
	Data  webtypes.NSSPurgedTombstoneResponse `json:"data" validate:"required"`
	Error *string                             `json:"error"`
}

// NSSAccountsEnvelope is GET /api/admin/nss/users's body.
type NSSAccountsEnvelope struct {
	Data  webtypes.NSSAccountsResponse `json:"data" validate:"required"`
	Error *string                      `json:"error"`
}

// NSSAccountDetailEnvelope is GET /api/admin/nss/users/{uid}'s body.
type NSSAccountDetailEnvelope struct {
	Data  webtypes.NSSAccountDetailResponse `json:"data" validate:"required"`
	Error *string                           `json:"error"`
}

// NSSGroupsEnvelope is GET /api/admin/nss/groups's body.
type NSSGroupsEnvelope struct {
	Data  webtypes.NSSGroupsResponse `json:"data" validate:"required"`
	Error *string                    `json:"error"`
}

// NSSGroupDetailEnvelope is GET /api/admin/nss/groups/{gid}'s body.
type NSSGroupDetailEnvelope struct {
	Data  webtypes.NSSGroupDetailResponse `json:"data" validate:"required"`
	Error *string                         `json:"error"`
}

// NSSGroupMembersEnvelope is GET /api/admin/nss/groups/{gid}/members's
// body.
type NSSGroupMembersEnvelope struct {
	Data  webtypes.NSSGroupMembersResponse `json:"data" validate:"required"`
	Error *string                          `json:"error"`
}

// NSSHistoryEnvelope is the body of both history reads,
// GET /api/admin/nss/users/{uid}/audit and /api/admin/nss/groups/{gid}/audit:
// the per-user timeline's payload.
type NSSHistoryEnvelope struct {
	Data  webtypes.AuditEventsResponse `json:"data" validate:"required"`
	Error *string                      `json:"error"`
}

// NSSAdminKeysEnvelope is GET /api/admin/nss/keys's body.
type NSSAdminKeysEnvelope struct {
	Data  webtypes.NSSAdminKeysResponse `json:"data" validate:"required"`
	Error *string                       `json:"error"`
}
