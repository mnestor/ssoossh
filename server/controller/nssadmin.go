package controller

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewNSSAdminController registers the NSS directory's admin routes under
// /api/admin/nss: the pending uid and gid changes and their acceptance,
// the collisions report, the early purge of a tombstone, and the
// read-only directory viewer.
//
// Admin only, every route but the viewer's: accepting a changed number
// re-owns files on every host, and a purge hands a number and a name on.
// The viewer only reads, so auditors (and so SOC and admins) see it, as
// they see the user directory. nss and viewer are nil when nss.enabled is
// off, which every route answers with a 404 naming the setting.
func NewNSSAdminController(
	group *gin.RouterGroup,
	db *gorm.DB,
	nss service.NSSDirectoryAdmin,
	viewer service.NSSDirectoryViewer,
	sessionAuthMiddleware gin.HandlerFunc,
	adminAuthMiddleware gin.HandlerFunc,
	auditorAuthMiddleware gin.HandlerFunc,
	csrfMiddleware gin.HandlerFunc,
) {
	a := &nssAdminController{db: db, nss: nss, viewer: viewer}

	// One group: the CSRF middleware exempts safe methods, so the reads
	// share it with the mutations.
	adminGroup := group.Group("/admin/nss", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.GET("/pending-id-changes", a.pendingHandler)
	adminGroup.POST("/users/:uid/accept-id-change", a.acceptUserHandler)
	adminGroup.POST("/groups/:gid/accept-id-change", a.acceptGroupHandler)
	adminGroup.GET("/collisions", a.collisionsHandler)
	adminGroup.DELETE("/users/:uid/tombstone", a.purgeUserHandler)
	adminGroup.DELETE("/groups/:gid/tombstone", a.purgeGroupHandler)

	// The viewer: reads only, so no CSRF check, as the auditor routes in
	// admin.go.
	auditorGroup := group.Group("/admin/nss", sessionAuthMiddleware, auditorAuthMiddleware)
	auditorGroup.GET("/users", a.listAccountsHandler)
	auditorGroup.GET("/users/:uid", a.accountHandler)
	auditorGroup.GET("/groups", a.listGroupsHandler)
	auditorGroup.GET("/groups/:gid", a.groupHandler)
	auditorGroup.GET("/groups/:gid/members", a.groupMembersHandler)
	auditorGroup.GET("/users/:uid/audit", a.accountHistoryHandler)
	auditorGroup.GET("/groups/:gid/audit", a.groupHistoryHandler)
}

// nssAdminController handles the NSS directory's admin routes. The
// mutations are audited by the service, in their own transactions; the
// reads are not, as the user directory's are not, except the two history
// reads, which record admin.audit_viewed as the per-user timeline does.
type nssAdminController struct {
	db     *gorm.DB
	nss    service.NSSDirectoryAdmin
	viewer service.NSSDirectoryViewer
}

// enabled answers a 404 and reports false when NSS is disabled.
func (a *nssAdminController) enabled(g *gin.Context) bool {
	return nssEnabled(g, a.nss != nil)
}

// viewerEnabled is enabled for the viewer's routes.
func (a *nssAdminController) viewerEnabled(g *gin.Context) bool {
	return nssEnabled(g, a.viewer != nil)
}

// nssEnabled answers the disabled 404 unless on, and reports on.
func nssEnabled(g *gin.Context, on bool) bool {
	if on {
		return true
	}
	handleError(g, &errorresponses.NotFoundError{Resource: "the NSS directory (nss.enabled is false)"})
	return false
}

// number parses the :uid or :gid path parameter as a uint32, answering a
// 400 and reporting false when it is not one.
func nssNumberParam(g *gin.Context, param string) (uint32, bool) {
	n, err := strconv.ParseUint(g.Param(param), 10, 32)
	if err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("%s must be a number from 0 to 4294967295", param)})
		return 0, false
	}
	return uint32(n), true
}

// pendingSince is a pending row's pending_since, which the sync always
// sets with the pending value; zero only for a row written some other way.
func pendingSince(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// actor snapshots the calling admin for the audit event, resolving their
// users-row id.
func (a *nssAdminController) actor(g *gin.Context) *service.AuditSubject {
	identity, ok := middleware.Identity(g)
	if !ok || identity == nil {
		// not covered: every route here sits behind SessionAuthMiddleware,
		// which aborts before the handler when there is no identity.
		return nil
	}
	var user model.User
	if a.db != nil {
		// A missing row only leaves the actor without its id; the event is
		// still written.
		_ = a.db.WithContext(g.Request.Context()).
			Select("id").Where("subject = ?", identity.Subject).
			First(&user).Error
	}
	return service.AuditSubjectFromIdentity(identity, user.ID)
}

// pendingHandler handles GET /api/admin/nss/pending-id-changes.
//
// @Summary     List pending uid and gid changes (admin only)
// @Description Every NSS account and group whose directory entry now gives
// @Description a stored uid or gid a different value. Hosts keep being
// @Description served the stored value until an admin accepts the change
// @Description (after re-owning files on the hosts) or the directory
// @Description reverts; the server raises nss.id_change_pending daily
// @Description meanwhile. Oldest first.
// @Tags        admin
// @Produce     json
// @Success     200 {object} openapidoc.NSSPendingIDChangesEnvelope "The pending changes"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/pending-id-changes [get]
func (a *nssAdminController) pendingHandler(g *gin.Context) {
	if !a.enabled(g) {
		return
	}
	accounts, groups, err := a.nss.PendingIDChanges(g.Request.Context())
	if err != nil {
		handleError(g, err)
		return
	}
	resp := webtypes.NSSPendingIDChangesResponse{
		Users:  make([]webtypes.NSSPendingUserIDChange, 0, len(accounts)),
		Groups: make([]webtypes.NSSPendingGroupIDChange, 0, len(groups)),
	}
	for _, row := range accounts {
		resp.Users = append(resp.Users, webtypes.NSSPendingUserIDChange{
			Name: row.Name, DN: row.DN, UID: row.UID, GID: row.GID,
			PendingUID: row.PendingUID, PendingGID: row.PendingGID, PendingSince: pendingSince(row.PendingSince),
		})
	}
	for _, row := range groups {
		pending := row.GID
		if row.PendingGID != nil {
			pending = *row.PendingGID
		}
		resp.Groups = append(resp.Groups, webtypes.NSSPendingGroupIDChange{
			Name: row.Name, DN: row.DN, GID: row.GID, PendingGID: pending, PendingSince: pendingSince(row.PendingSince),
		})
	}
	respondData(g, resp)
}

// acceptUserHandler handles POST /api/admin/nss/users/:uid/accept-id-change.
//
// @Summary     Accept an account's pending uid or gid (admin only)
// @Description Applies the pending uid and gid of the NSS account stored
// @Description with this uid, in one transaction that re-keys its group
// @Description memberships to the new uid, and audits it as
// @Description nss.id_change_accepted. Hosts see the new number from then
// @Description on, so re-own the account's files on them first. Refused
// @Description with 409 when another account, a tombstone included, holds
// @Description the new uid.
// @Tags        admin
// @Produce     json
// @Param       uid path integer true "The account's stored uid"
// @Success     200 {object} openapidoc.NSSAcceptedUserIDChangeEnvelope "The account after the change"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The uid is not a number"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No account with that uid has a pending change, or NSS is disabled"
// @Failure     409 {object} openapidoc.ErrorEnvelope "Another account holds the new uid"
// @Security    sessionCookie
// @Router      /api/admin/nss/users/{uid}/accept-id-change [post]
func (a *nssAdminController) acceptUserHandler(g *gin.Context) {
	if !a.enabled(g) {
		return
	}
	uid, ok := nssNumberParam(g, "uid")
	if !ok {
		return
	}
	row, err := a.nss.AcceptUserIDChange(g.Request.Context(), uid, a.actor(g))
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, webtypes.NSSAcceptedUserIDChangeResponse{Name: row.Name, DN: row.DN, UID: row.UID, GID: row.GID})
}

// acceptGroupHandler handles POST /api/admin/nss/groups/:gid/accept-id-change.
//
// @Summary     Accept a group's pending gid (admin only)
// @Description Applies the pending gid of the NSS group stored with this
// @Description gid, in one transaction that re-keys its membership rows and
// @Description moves every account whose primary gid it was, and audits it
// @Description as nss.id_change_accepted. Refused with 409 when another
// @Description group, a tombstone included, holds the new gid.
// @Tags        admin
// @Produce     json
// @Param       gid path integer true "The group's stored gid"
// @Success     200 {object} openapidoc.NSSAcceptedGroupIDChangeEnvelope "The group after the change"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The gid is not a number"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No group with that gid has a pending change, or NSS is disabled"
// @Failure     409 {object} openapidoc.ErrorEnvelope "Another group holds the new gid"
// @Security    sessionCookie
// @Router      /api/admin/nss/groups/{gid}/accept-id-change [post]
func (a *nssAdminController) acceptGroupHandler(g *gin.Context) {
	if !a.enabled(g) {
		return
	}
	gid, ok := nssNumberParam(g, "gid")
	if !ok {
		return
	}
	row, err := a.nss.AcceptGroupIDChange(g.Request.Context(), gid, a.actor(g))
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, webtypes.NSSAcceptedGroupIDChangeResponse{Name: row.Name, DN: row.DN, GID: row.GID})
}

// collisionsHandler handles GET /api/admin/nss/collisions.
//
// @Summary     List uid, gid and name collisions (admin only)
// @Description Every pair of directory entries contending for one uid, gid
// @Description or name, oldest first: the stored row holding the value,
// @Description the other entry (stored, or refused and known only by what
// @Description it carried), whether the holder is a tombstone, and since
// @Description when. Neither side is served while a pair lasts. A pair
// @Description goes when either entry is read again without the clash, at
// @Description the latest on the next full sync.
// @Tags        admin
// @Produce     json
// @Success     200 {object} openapidoc.NSSCollisionsEnvelope "The current collisions"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/collisions [get]
func (a *nssAdminController) collisionsHandler(g *gin.Context) {
	if !a.enabled(g) {
		return
	}
	reports, err := a.nss.Collisions(g.Request.Context())
	if err != nil {
		handleError(g, err)
		return
	}
	resp := webtypes.NSSCollisionsResponse{Collisions: make([]webtypes.NSSCollisionResponse, 0, len(reports))}
	for _, r := range reports {
		resp.Collisions = append(resp.Collisions, webtypes.NSSCollisionResponse{
			Kind:        r.Kind,
			Value:       r.Value,
			Holder:      nssCollisionEntryResponse(r.Holder),
			Other:       nssCollisionEntryResponse(r.Other),
			FirstSeenAt: r.FirstSeenAt,
			LastSeenAt:  r.LastSeenAt,
		})
	}
	respondData(g, resp)
}

// nssCollisionEntryResponse maps one side of a collision to the wire.
func nssCollisionEntryResponse(e service.NSSCollisionEntry) webtypes.NSSCollisionEntryResponse {
	return webtypes.NSSCollisionEntryResponse{
		Name: e.Name, DN: e.DN, Number: e.Number, Stored: e.Stored, Tombstone: e.DeletedAt != nil,
		MissingSince: e.MissingSince, DeletedAt: e.DeletedAt,
	}
}

// purgeUserHandler handles DELETE /api/admin/nss/users/:uid/tombstone.
//
// @Summary     Purge a tombstoned account early (admin only)
// @Description Deletes the tombstoned NSS account with this uid before
// @Description nss.tombstone_retention, with its memberships and
// @Description collisions, freeing its uid and name for a new entry at the
// @Description next sync, and audits it as nss.tombstone_purged. Only a
// @Description tombstone: a live account is 404.
// @Tags        admin
// @Produce     json
// @Param       uid path integer true "The tombstone's uid"
// @Success     200 {object} openapidoc.NSSPurgedTombstoneEnvelope "The purged account"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The uid is not a number"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No tombstoned account has that uid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/users/{uid}/tombstone [delete]
func (a *nssAdminController) purgeUserHandler(g *gin.Context) {
	if !a.enabled(g) {
		return
	}
	uid, ok := nssNumberParam(g, "uid")
	if !ok {
		return
	}
	row, err := a.nss.PurgeUserTombstone(g.Request.Context(), uid, a.actor(g))
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, webtypes.NSSPurgedTombstoneResponse{Name: row.Name, DN: row.DN, Number: row.UID})
}

// purgeGroupHandler handles DELETE /api/admin/nss/groups/:gid/tombstone.
//
// @Summary     Purge a tombstoned group early (admin only)
// @Description Deletes the tombstoned NSS group with this gid before
// @Description nss.tombstone_retention, freeing its gid and name, and
// @Description audits it as nss.tombstone_purged. Only a tombstone: a live
// @Description group is 404.
// @Tags        admin
// @Produce     json
// @Param       gid path integer true "The tombstone's gid"
// @Success     200 {object} openapidoc.NSSPurgedTombstoneEnvelope "The purged group"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The gid is not a number"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No tombstoned group has that gid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/groups/{gid}/tombstone [delete]
func (a *nssAdminController) purgeGroupHandler(g *gin.Context) {
	if !a.enabled(g) {
		return
	}
	gid, ok := nssNumberParam(g, "gid")
	if !ok {
		return
	}
	row, err := a.nss.PurgeGroupTombstone(g.Request.Context(), gid, a.actor(g))
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, webtypes.NSSPurgedTombstoneResponse{Name: row.Name, DN: row.DN, Number: row.GID})
}
