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
