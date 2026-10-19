package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// The read-only directory viewer's handlers, registered by
// NewNSSAdminController behind the auditor middleware.

// nssAccountStates and nssGroupStates are the state filter's values for
// each list, in precedence order. A group has no disable, so "disabled" is
// not a group state.
func nssAccountStates() []model.NSSEntryState {
	return []model.NSSEntryState{
		model.NSSEntryStateWithheld, model.NSSEntryStateTombstone, model.NSSEntryStateDisabled,
		model.NSSEntryStateMissing, model.NSSEntryStatePending, model.NSSEntryStateActive,
	}
}

// nssGroupStates is nssAccountStates without "disabled".
func nssGroupStates() []model.NSSEntryState {
	return []model.NSSEntryState{
		model.NSSEntryStateWithheld, model.NSSEntryStateTombstone,
		model.NSSEntryStateMissing, model.NSSEntryStatePending, model.NSSEntryStateActive,
	}
}

// parseNSSStateFilter reads the `state` query parameter against allowed.
// Empty is every state; anything else outside allowed is an error rather
// than a silent fall back to every state, for the reason
// parseUserStatusFilter gives.
func parseNSSStateFilter(raw string, allowed []model.NSSEntryState) (model.NSSEntryState, error) {
	state := model.NSSEntryState(strings.TrimSpace(raw))
	if state == "" {
		return "", nil
	}
	names := make([]string, 0, len(allowed))
	for _, a := range allowed {
		if state == a {
			return state, nil
		}
		names = append(names, string(a))
	}
	return "", &errorresponses.InvalidRequestError{
		Reason: fmt.Sprintf("state must be one of %s, got %q", strings.Join(names, ", "), raw),
	}
}

// nssViewerFilter reads a list's paging, search and state parameters,
// answering a 400 and reporting false when one is malformed.
func nssViewerFilter(g *gin.Context, allowed []model.NSSEntryState) (service.NSSViewerFilter, bool) {
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return service.NSSViewerFilter{}, false
	}
	state, err := parseNSSStateFilter(g.Query("state"), allowed)
	if err != nil {
		handleError(g, err)
		return service.NSSViewerFilter{}, false
	}
	return service.NSSViewerFilter{Page: params, State: state}, true
}

// listAccountsHandler handles GET /api/admin/nss/users.
//
// @Summary     List the stored NSS accounts (auditor and above)
// @Description One page of the posix_users rows, ordered by name then id, each with its derived state.
// @Description q matches a case-insensitive substring of the name or DN, and also the exact uid when it is a number.
// @Description Uses cursor-based pagination (not offset): pass the previous page's next_cursor as after.
// @Description Reads only the database: never the directory, and never writes.
// @Tags        admin
// @Produce     json
// @Param       after query string false "Row id to start after (cursor): the previous page's next_cursor"
// @Param       limit query int    false "Page size (default 25, max 100)" example(25)
// @Param       q     query string false "Search term matched against name and DN, or an exact uid" example(ada)
// @Param       state query string false "State filter; omitted returns every state" Enums(withheld, tombstone, disabled, missing, pending, active)
// @Success     200 {object} openapidoc.NSSAccountsEnvelope "One page of accounts"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid paging/search parameters, an unknown state, an after that names no account, or offset"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/users [get]
func (a *nssAdminController) listAccountsHandler(g *gin.Context) {
	if !a.viewerEnabled(g) {
		return
	}
	filter, ok := nssViewerFilter(g, nssAccountStates())
	if !ok {
		return
	}
	page, err := a.viewer.Accounts(g.Request.Context(), filter)
	if err != nil {
		handleError(g, err)
		return
	}
	resp := webtypes.NSSAccountsResponse{
		Accounts: make([]webtypes.NSSAccountSummaryResponse, 0, len(page.Accounts)),
		Total:    page.Total, NextCursor: page.NextCursor,
	}
	for _, e := range page.Accounts {
		resp.Accounts = append(resp.Accounts, webtypes.NSSAccountSummaryResponse{
			ID: e.Account.ID, Name: e.Account.Name, UID: e.Account.UID, GID: e.Account.GID,
			Gecos: e.Account.Gecos, DN: e.Account.DN, State: e.State, LastSyncedAt: e.Account.LastSyncedAt,
		})
	}
	respondData(g, resp)
}

// accountHandler handles GET /api/admin/nss/users/:uid.
//
// @Summary     Read one stored NSS account (auditor and above)
// @Description The posix_users row with this uid as stored, the ssoossh user linked to it, every stored
// @Description membership row for the uid, and what a host's lookup by the uid is served now, built by
// @Description the lookup code itself (absent with not_served saying why when the lookup finds nothing).
// @Description Reads only the database: where a host's miss would ask the directory, this does not.
// @Tags        admin
// @Produce     json
// @Param       uid path integer true "The account's stored uid"
// @Success     200 {object} openapidoc.NSSAccountDetailEnvelope "The account"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The uid is not a number"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No stored account has that uid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/users/{uid} [get]
func (a *nssAdminController) accountHandler(g *gin.Context) {
	if !a.viewerEnabled(g) {
		return
	}
	uid, ok := nssNumberParam(g, "uid")
	if !ok {
		return
	}
	view, err := a.viewer.Account(g.Request.Context(), uid)
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, nssAccountDetailResponse(view))
}

// nssAccountDetailResponse maps one account view to the wire.
func nssAccountDetailResponse(view *service.NSSAccountView) webtypes.NSSAccountDetailResponse {
	row := view.Account
	resp := webtypes.NSSAccountDetailResponse{
		ID: row.ID, Name: row.Name, UID: row.UID, GID: row.GID, DirectoryID: row.DirectoryID, DN: row.DN,
		Home: row.Home, Shell: row.Shell, Gecos: row.Gecos, DirDisabled: row.DirDisabled,
		PendingUID: row.PendingUID, PendingGID: row.PendingGID, PendingSince: row.PendingSince,
		MissingSince: row.MissingSince, DeletedAt: row.DeletedAt, WithheldAt: row.WithheldAt,
		WithheldReason: row.WithheldReason, State: view.State,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastSyncedAt: row.LastSyncedAt,
		Groups:    make([]webtypes.NSSStoredMembershipResponse, 0, len(view.Groups)),
		Served:    nssServedAccountResponse(view.Served),
		NotServed: view.NotServed,
	}
	if p := view.Person; p != nil {
		resp.Person = &webtypes.NSSLinkedPersonResponse{
			UserID: p.ID, Username: p.Username, DisplayName: p.DisplayName, DisabledAt: p.DisabledAt,
		}
	}
	for _, m := range view.Groups {
		resp.Groups = append(resp.Groups, webtypes.NSSStoredMembershipResponse{GID: m.GID, Name: m.Name, State: m.State, AddedAt: m.AddedAt})
	}
	return resp
}

// nssServedAccountResponse maps the lookup's record to the wire, nil for
// nil.
func nssServedAccountResponse(served *apitypes.NSSUserResponse) *webtypes.NSSServedAccountResponse {
	if served == nil {
		return nil
	}
	resp := &webtypes.NSSServedAccountResponse{
		Name: served.Name, UID: served.UID, GID: served.GID,
		Groups: make([]webtypes.NSSServedMembershipResponse, 0, len(served.Groups)),
		Home:   served.Home, Shell: served.Shell, Gecos: served.Gecos,
		Status: model.PosixStatus(served.Status),
	}
	for _, m := range served.Groups {
		resp.Groups = append(resp.Groups, webtypes.NSSServedMembershipResponse{GID: m.GID, Name: m.Name})
	}
	return resp
}

// listGroupsHandler handles GET /api/admin/nss/groups.
//
// @Summary     List the stored NSS groups (auditor and above)
// @Description One page of the posix_groups rows, ordered by name then id, each with its derived state and
// @Description its count of stored membership rows. Private groups are never stored, so are not listed.
// @Description q matches a case-insensitive substring of the name or DN, and also the exact gid when it is a number.
// @Description Uses cursor-based pagination (not offset): pass the previous page's next_cursor as after.
// @Description Reads only the database: never the directory, and never writes.
// @Tags        admin
// @Produce     json
// @Param       after query string false "Row id to start after (cursor): the previous page's next_cursor"
// @Param       limit query int    false "Page size (default 25, max 100)" example(25)
// @Param       q     query string false "Search term matched against name and DN, or an exact gid" example(engineering)
// @Param       state query string false "State filter; omitted returns every state" Enums(withheld, tombstone, missing, pending, active)
// @Success     200 {object} openapidoc.NSSGroupsEnvelope "One page of groups"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid paging/search parameters, an unknown state, an after that names no group, or offset"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/groups [get]
func (a *nssAdminController) listGroupsHandler(g *gin.Context) {
	if !a.viewerEnabled(g) {
		return
	}
	filter, ok := nssViewerFilter(g, nssGroupStates())
	if !ok {
		return
	}
	page, err := a.viewer.Groups(g.Request.Context(), filter)
	if err != nil {
		handleError(g, err)
		return
	}
	resp := webtypes.NSSGroupsResponse{
		Groups: make([]webtypes.NSSGroupSummaryResponse, 0, len(page.Groups)),
		Total:  page.Total, NextCursor: page.NextCursor,
	}
	for _, e := range page.Groups {
		resp.Groups = append(resp.Groups, webtypes.NSSGroupSummaryResponse{
			ID: e.Group.ID, Name: e.Group.Name, GID: e.Group.GID, DN: e.Group.DN, State: e.State,
			MemberCount: e.MemberCount, LastSyncedAt: e.Group.LastSyncedAt,
		})
	}
	respondData(g, resp)
}

// groupHandler handles GET /api/admin/nss/groups/:gid.
//
// @Summary     Read one stored NSS group (auditor and above)
// @Description The posix_groups row with this gid as stored, its count of stored membership rows, and what
// @Description a host's lookup by the gid is served now, built by the lookup code itself, with the member
// @Description names counted after nss.max_group_members (absent with not_served saying why when the
// @Description lookup finds nothing). The members are paged from /api/admin/nss/groups/{gid}/members.
// @Description Reads only the database: where a host's miss would ask the directory, this does not.
// @Tags        admin
// @Produce     json
// @Param       gid path integer true "The group's stored gid"
// @Success     200 {object} openapidoc.NSSGroupDetailEnvelope "The group"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The gid is not a number"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No stored group has that gid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/groups/{gid} [get]
func (a *nssAdminController) groupHandler(g *gin.Context) {
	if !a.viewerEnabled(g) {
		return
	}
	gid, ok := nssNumberParam(g, "gid")
	if !ok {
		return
	}
	view, err := a.viewer.Group(g.Request.Context(), gid)
	if err != nil {
		handleError(g, err)
		return
	}
	row := view.Group
	resp := webtypes.NSSGroupDetailResponse{
		ID: row.ID, Name: row.Name, GID: row.GID, DirectoryID: row.DirectoryID, DN: row.DN,
		PendingGID: row.PendingGID, PendingSince: row.PendingSince, MissingSince: row.MissingSince,
		DeletedAt: row.DeletedAt, WithheldAt: row.WithheldAt, WithheldReason: row.WithheldReason,
		State: view.State, MemberCount: view.MemberCount,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastSyncedAt: row.LastSyncedAt,
		NotServed: view.NotServed,
	}
	if s := view.Served; s != nil {
		resp.Served = &webtypes.NSSServedGroupResponse{
			Name: s.Name, GID: s.GID, MemberCount: len(s.Members), MembersTruncated: s.MembersTruncated,
		}
	}
	respondData(g, resp)
}

// groupMembersHandler handles GET /api/admin/nss/groups/:gid/members.
//
// @Summary     List a stored NSS group's membership rows (auditor and above)
// @Description One page of the posix_group_members rows for this gid, ordered by uid, each with the stored
// @Description account of that uid and its state when there is one (a member with no stored account is
// @Description never served). The cursor is the last uid of the previous page. Reads only the database.
// @Tags        admin
// @Produce     json
// @Param       gid   path  integer true  "The group's stored gid"
// @Param       after query string  false "Uid to start after (cursor): the previous page's next_cursor"
// @Param       limit query int     false "Page size (default 25, max 100)" example(25)
// @Success     200 {object} openapidoc.NSSGroupMembersEnvelope "One page of members"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The gid is not a number, invalid paging parameters, an after that is not a uid, or offset"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No stored group has that gid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/groups/{gid}/members [get]
func (a *nssAdminController) groupMembersHandler(g *gin.Context) {
	if !a.viewerEnabled(g) {
		return
	}
	gid, ok := nssNumberParam(g, "gid")
	if !ok {
		return
	}
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}
	page, err := a.viewer.GroupMembers(g.Request.Context(), gid, params)
	if err != nil {
		handleError(g, err)
		return
	}
	resp := webtypes.NSSGroupMembersResponse{
		Members: make([]webtypes.NSSGroupMemberResponse, 0, len(page.Members)),
		Total:   page.Total, NextCursor: page.NextCursor,
	}
	for _, m := range page.Members {
		resp.Members = append(resp.Members, webtypes.NSSGroupMemberResponse{UID: m.UID, Name: m.Name, State: m.State, AddedAt: m.AddedAt})
	}
	respondData(g, resp)
}

// accountHistoryHandler handles GET /api/admin/nss/users/:uid/audit.
//
// @Summary     View a stored NSS account's audit history (auditor and above)
// @Description The audit events about the posix_users row with this uid, newest first: its lifecycle
// @Description (created, renamed, missing, tombstoned, revived, purged), the group membership changes that
// @Description added or removed it, its collisions, pending and accepted uid changes, and shell changes.
// @Description The history belongs to the row, so it follows a rename or an accepted uid change.
// @Description Uses cursor-based pagination (not offset), as /api/admin/users/{id}/audit does.
// @Tags        admin
// @Produce     json
// @Param       uid   path  integer true  "The account's stored uid"
// @Param       after query string  false "Audit event ID to start after (cursor): the previous page's next_cursor; must be on this account's history"
// @Param       limit query int     false "Page size (default 25, max 100)" example(25)
// @Success     200 {object} openapidoc.NSSHistoryEnvelope "Audit events"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The uid is not a number, invalid paging parameters, an after that names no event on this history, or offset"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No stored account has that uid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/users/{uid}/audit [get]
func (a *nssAdminController) accountHistoryHandler(g *gin.Context) {
	// Checked before the method value is taken: viewer is nil with NSS off.
	if !a.viewerEnabled(g) {
		return
	}
	a.historyHandler(g, "uid", a.viewer.AccountHistory)
}

// groupHistoryHandler handles GET /api/admin/nss/groups/:gid/audit.
//
// @Summary     View a stored NSS group's audit history (auditor and above)
// @Description The audit events about the posix_groups row with this gid, newest first: its lifecycle
// @Description (created, renamed, missing, tombstoned, revived, purged), every change to its members,
// @Description its collisions, and pending and accepted gid changes.
// @Description The history belongs to the row, so it follows a rename or an accepted gid change.
// @Description Uses cursor-based pagination (not offset), as /api/admin/users/{id}/audit does.
// @Tags        admin
// @Produce     json
// @Param       gid   path  integer true  "The group's stored gid"
// @Param       after query string  false "Audit event ID to start after (cursor): the previous page's next_cursor; must be on this group's history"
// @Param       limit query int     false "Page size (default 25, max 100)" example(25)
// @Success     200 {object} openapidoc.NSSHistoryEnvelope "Audit events"
// @Failure     400 {object} openapidoc.ErrorEnvelope "The gid is not a number, invalid paging parameters, an after that names no event on this history, or offset"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No stored group has that gid, or NSS is disabled"
// @Security    sessionCookie
// @Router      /api/admin/nss/groups/{gid}/audit [get]
func (a *nssAdminController) groupHistoryHandler(g *gin.Context) {
	if !a.viewerEnabled(g) {
		return
	}
	a.historyHandler(g, "gid", a.viewer.GroupHistory)
}

// nssHistoryReader is AccountHistory or GroupHistory.
type nssHistoryReader func(ctx context.Context, number uint32, params paging.Params, actor *service.AuditSubject) (*service.AuditPage, error)

// historyHandler is the two history routes: the number from param, the
// paging, and the page in the per-user timeline's wire shape, so the UI's
// timeline renders either.
func (a *nssAdminController) historyHandler(g *gin.Context, param string, read nssHistoryReader) {
	number, ok := nssNumberParam(g, param)
	if !ok {
		return
	}
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}
	page, err := read(g.Request.Context(), number, params, a.actor(g))
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, auditPageResponse(page))
}
