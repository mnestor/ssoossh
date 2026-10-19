package controller

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// The admin key list's handler, registered by NewNSSAdminController behind
// the auditor middleware.

// nssAdminKeyFilter reads the list's parameters. An unknown status,
// expiring_within or flag value is dropped rather than refused, as every
// list filter is (DESIGN.md, Lists); only the paging parameters can fail,
// with the 400 paging.Parse gives.
func nssAdminKeyFilter(g *gin.Context) (service.NSSAdminKeyFilter, error) {
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		return service.NSSAdminKeyFilter{}, err
	}
	filter := service.NSSAdminKeyFilter{Page: params}
	switch status := model.NSSKeyStatus(strings.TrimSpace(g.Query("status"))); status {
	case model.NSSKeyStatusActive, model.NSSKeyStatusExpired, model.NSSKeyStatusRevoked:
		filter.Status = status
	}
	// The three windows the page offers, in days.
	const day = 24 * time.Hour
	switch strings.TrimSpace(g.Query("expiring_within")) {
	case "7":
		filter.ExpiringWithin = 7 * day
	case "30":
		filter.ExpiringWithin = 30 * day
	case "90":
		filter.ExpiringWithin = 90 * day
	}
	filter.Unused = queryFlag(g, "unused")
	filter.Alerts = queryFlag(g, "alerts")
	return filter, nil
}

// queryFlag reads a boolean filter: "1" or "true" turns it on, anything
// else leaves it off.
func queryFlag(g *gin.Context, name string) bool {
	switch strings.ToLower(strings.TrimSpace(g.Query(name))) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// listKeysHandler handles GET /api/admin/nss/keys.
//
// @Summary     List every fleet API key (auditor and above)
// @Description One page of every fleet key, whoever manages it and whether or not it has a group, by name then id.
// @Description q matches a case-insensitive substring of the key's name or group or the creator's username or email.
// @Description A q that reads as an IP address (IPv4 or IPv6, an IPv4-mapped one read as IPv4) or a CIDR range
// @Description searches the usage instead: the keys hosts used from that address or range within nss.usage_retention,
// @Description each with up to 5 of the matching addresses (newest first) and how many matched; address_search then
// @Description says how q was read. A q shaped like an address or a range that is not a valid one is a 400 naming the
// @Description problem, never a text search. status, expiring_within (7, 30 or 90 days, active keys only), unused
// @Description (no lookup in 30 days, never used included) and alerts (a miss rate or key source alert in 7 days)
// @Description narrow it; an unknown value is dropped. Each key carries its creator, its distinct source addresses
// @Description in 7 days and its last lookup. Never a secret or a secret's hash. Read only: a key's own page,
// @Description /api/nss/keys/{id}, keeps every action. Uses cursor-based pagination (not offset).
// @Tags        admin
// @Produce     json
// @Param       after           query string false "Key id to start after (cursor): the previous page's next_cursor"
// @Param       limit           query int    false "Page size (default 25, max 100)" example(25)
// @Param       q               query string false "Text matched against name, group, creator username and email; or an IP address or CIDR range" example(10.0.4.0/24)
// @Param       status          query string false "Status filter; omitted or all returns every status" Enums(all, active, expired, revoked)
// @Param       expiring_within query int    false "Only active keys expiring within this many days" Enums(7, 30, 90)
// @Param       unused          query bool   false "Only keys with no lookup in the last 30 days"
// @Param       alerts          query bool   false "Only keys with a miss rate or key source alert in the last 7 days"
// @Success     200 {object} openapidoc.NSSAdminKeysEnvelope "One page of keys"
// @Failure     400 {object} openapidoc.ErrorEnvelope "A q shaped like an address or range that is not one, invalid paging parameters, an after that names no key, or offset"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_disabled: nss.enabled is false"
// @Security    sessionCookie
// @Router      /api/admin/nss/keys [get]
func (a *nssAdminController) listKeysHandler(g *gin.Context) {
	if a.keys == nil {
		handleError(g, errorresponses.NSSDisabled())
		return
	}
	filter, err := nssAdminKeyFilter(g)
	if err != nil {
		handleError(g, err)
		return
	}
	page, err := a.keys.AdminList(g.Request.Context(), filter)
	if err != nil {
		handleError(g, err)
		return
	}
	resp := webtypes.NSSAdminKeysResponse{
		Keys:                  make([]webtypes.NSSAdminKeyResponse, 0, len(page.Keys)),
		Total:                 page.Total,
		NextCursor:            page.NextCursor,
		UsageRetentionSeconds: int(page.UsageRetention / time.Second),
	}
	if page.Address != nil {
		resp.AddressSearch = page.Address.Display
	}
	for i := range page.Keys {
		resp.Keys = append(resp.Keys, newNSSAdminKeyResponse(&page.Keys[i]))
	}
	respondData(g, resp)
}

// newNSSAdminKeyResponse maps one admin list entry to the wire.
func newNSSAdminKeyResponse(entry *service.NSSAdminKeyEntry) webtypes.NSSAdminKeyResponse {
	key := entry.Key
	resp := webtypes.NSSAdminKeyResponse{
		ID: key.ID, Name: key.Name, GroupName: key.GroupName, Status: entry.Status,
		CurrentGeneration: key.CurrentGeneration, ExpiresAt: key.ExpiresAt,
		PreviousExpiresAt: key.PreviousExpiresAt, RevokedAt: key.RevokedAt, CreatedAt: key.CreatedAt,
		Creator: webtypes.NSSKeyCreatorResponse{
			UserID: key.CreatedBy, Username: entry.CreatorUsername, Email: entry.CreatorEmail, Exists: entry.CreatorFound,
		},
		Hosts7d: entry.Hosts, LastSeenAt: entry.LastSeenAt,
		MissRateAlertAt: entry.MissRateAlertAt, KeySourceAlertAt: entry.KeySourceAlertAt,
	}
	if m := entry.Matched; m != nil {
		resp.Matched = &webtypes.NSSKeyMatchedUseResponse{
			Addresses: make([]webtypes.NSSKeyAddressUseResponse, 0, len(m.Addresses)), Total: m.Total,
		}
		for _, use := range m.Addresses {
			resp.Matched.Addresses = append(resp.Matched.Addresses, webtypes.NSSKeyAddressUseResponse{
				SourceIP: use.SourceIP, LastSeen: use.LastSeen, Generation: use.Generation, Secret: use.Secret,
			})
		}
	}
	return resp
}
