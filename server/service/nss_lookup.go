package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/posixname"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// nssDirectoryRefresher is the one call from the lookups into the directory
// half of the NSS feature (server/service/nss_directory.go): read one entry
// from LDAP, write its row exactly as the sync would, and report whether
// the directory has it. A lookup calls it only when the database has no
// servable row, so a host asking for a name the sync has not reached yet
// still gets an answer, and a known name never costs a directory search.
// An error means the directory could not be asked; the lookup then answers
// from the database alone.
type nssDirectoryRefresher interface {
	RefreshUserByName(ctx context.Context, name string) (bool, error)
	RefreshUserByUID(ctx context.Context, uid uint32) (bool, error)
	RefreshGroupByName(ctx context.Context, name string) (bool, error)
	RefreshGroupByGID(ctx context.Context, gid uint32) (bool, error)
}

// NSSLookupService answers the NSS directory lookups (/api/nss/user/* and
// /api/nss/group/*) from posix_users, posix_groups and
// posix_group_members. See docs/proposals/nss-directory/1-server-api.md.
//
// Everything a host is served is derived on read: the status from the
// row's tombstone, the linked person's disable and the directory's own
// flag; the primary group from nss.auto_private_groups; the home and shell
// from the row or the configured defaults. Nothing derived is stored, so a
// SOC disable or a config change reaches every host on its next lookup.
type NSSLookupService struct {
	config *config.Config
	db     *gorm.DB

	// refresher reads one entry from the directory on a miss. Nil means
	// there is no directory half wired, and a miss is answered from the
	// database alone.
	refresher nssDirectoryRefresher

	// auditor reads a row's history for the viewer and records that it was
	// read. Nil until SetAuditor runs.
	auditor *AuditService

	// now is the clock, time.Now outside tests.
	now func() time.Time
}

// NewNSSLookupService builds the lookups. refresher may be nil.
func NewNSSLookupService(c *config.Config, db *gorm.DB, refresher nssDirectoryRefresher) *NSSLookupService {
	return &NSSLookupService{config: c, db: db, refresher: refresher, now: time.Now}
}

// SetAuditor wires the audit recorder. Called once at startup.
func (s *NSSLookupService) SetAuditor(a *AuditService) { s.auditor = a }

// ParseNSSID reads a uid or gid path segment. Anything that is not a
// decimal uint32 is a bad request rather than a miss, so the daemon never
// caches a typo as an authoritative "no such id".
func ParseNSSID(raw string) (uint32, error) {
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, errorresponses.NSSBadRequest(fmt.Sprintf("%q is not a uid or gid: it must be a decimal number from 0 to 4294967295", raw))
	}
	return uint32(id), nil
}

// validNSSName applies the POSIX name rules a host applies before it asks,
// so a name no host could ever look up is a bad request, not a miss.
func validNSSName(name string) error {
	if err := posixname.Valid(name); err != nil {
		return errorresponses.NSSBadRequest(err.Error())
	}
	return nil
}

// posixUserRow is a posix_users row with the linked person's disable, the
// one fact from users that the status needs.
type posixUserRow struct {
	model.PosixUser
	UserDisabledAt *time.Time `gorm:"column:user_disabled_at"`
}

// errNSSPrivateGroupClash marks an account nss.auto_private_groups cannot
// serve, because a directory group already holds the private group's
// number or name. Never returned to a caller: it becomes a 404.
var errNSSPrivateGroupClash = errors.New("a directory group holds this account's private group")

// errNSSNoGID marks an account stored without a gid attribute (accepted
// only under nss.auto_private_groups "true") read under a mode that needs
// one. Never returned to a caller: it becomes a 404.
var errNSSNoGID = errors.New("a posix account has no gid and nss.auto_private_groups is not true")

// UserByName looks a user up by name, matched byte for byte.
func (s *NSSLookupService) UserByName(ctx context.Context, name string) (*apitypes.NSSUserResponse, error) {
	if err := validNSSName(name); err != nil {
		return nil, err
	}
	where := func(db *gorm.DB) *gorm.DB { return db.Where("posix_users.name = ?", name) }
	return s.lookupUser(ctx, where, func(ctx context.Context) (bool, error) {
		return s.refresher.RefreshUserByName(ctx, name)
	})
}

// UserByUID looks a user up by uid.
func (s *NSSLookupService) UserByUID(ctx context.Context, uid uint32) (*apitypes.NSSUserResponse, error) {
	where := func(db *gorm.DB) *gorm.DB { return db.Where("posix_users.uid = ?", uid) }
	return s.lookupUser(ctx, where, func(ctx context.Context) (bool, error) {
		return s.refresher.RefreshUserByUID(ctx, uid)
	})
}

// lookupUser reads the one row where selects, asking the directory first
// when the database has no servable row, and builds the response.
func (s *NSSLookupService) lookupUser(ctx context.Context, where func(*gorm.DB) *gorm.DB, refresh func(context.Context) (bool, error)) (*apitypes.NSSUserResponse, error) {
	row, err := s.readServableUser(ctx, where)
	if err != nil {
		return nil, err
	}
	if row == nil {
		var refreshErr error
		if s.refresher != nil {
			_, refreshErr = refresh(ctx)
			// Read again whatever the refresh said: it may have written the
			// row, and the sync may have written it meanwhile.
			if row, err = s.readServableUser(ctx, where); err != nil {
				return nil, err
			}
		}
		if row == nil {
			return nil, s.missAfterRefresh(ctx, refreshErr)
		}
	}

	response, err := s.buildUser(ctx, row)
	if errors.Is(err, errNSSPrivateGroupClash) {
		// Not the caller's fault and not something they can fix: a
		// directory group took the number or name the private group
		// needs. Rejecting the account rather than shadowing the group is
		// the rule; the warning is how an operator finds out.
		slog.WarnContext(ctx, "not serving an NSS account: nss.auto_private_groups is true and a directory group already holds its private group's gid or name",
			"name", row.Name, "uid", row.UID, "dn", row.DN)
		return nil, errorresponses.NSSNotFound()
	}
	if errors.Is(err, errNSSNoGID) {
		// sssd would never have stored it: an entry without a gid is
		// refused in every mode but "true".
		slog.WarnContext(ctx, "not serving an NSS account: it has no gid attribute, which nss.auto_private_groups requires unless it is true",
			"name", row.Name, "uid", row.UID, "dn", row.DN, "auto_private_groups", s.config.NSS.AutoPrivateGroups)
		return nil, errorresponses.NSSNotFound()
	}
	return response, err
}

// missAfterRefresh is the answer when no servable row exists after the
// directory was asked: an authoritative miss, unless the directory could
// not be asked, in which case the server does not know and says so with a
// 503 the daemon will not cache.
func (s *NSSLookupService) missAfterRefresh(ctx context.Context, refreshErr error) error {
	if refreshErr == nil {
		return errorresponses.NSSNotFound()
	}
	slog.WarnContext(ctx, "an NSS lookup missed the database and the directory could not be asked; answering unavailable",
		"error", refreshErr)
	return &errorresponses.UserStatusCheckError{}
}

// readServableUser reads the posix_users row where selects, with the
// linked person's disable, or nil when there is none a host may be served:
// absent, withheld by a collision, or a tombstone past
// nss.tombstone_retention that the purge has not reached yet.
func (s *NSSLookupService) readServableUser(ctx context.Context, where func(*gorm.DB) *gorm.DB) (*posixUserRow, error) {
	row, err := s.readUserRow(ctx, where)
	if err != nil || row == nil {
		return nil, err
	}
	if !s.servable(row.WithheldAt, row.DeletedAt) {
		return nil, nil
	}
	return row, nil
}

// readUserRow reads the posix_users row where selects, with the linked
// person's disable, whether or not it may be served; nil when there is
// none. The directory viewer reads through it too, so the row it shows is
// the row a lookup would judge.
func (s *NSSLookupService) readUserRow(ctx context.Context, where func(*gorm.DB) *gorm.DB) (*posixUserRow, error) {
	var row posixUserRow
	err := where(s.db.WithContext(ctx).Table("posix_users").
		Select("posix_users.*, users.disabled_at AS user_disabled_at").
		Joins("LEFT JOIN users ON users.id = posix_users.user_id")).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read posix account: %w", err)
	}
	return &row, nil
}

// servable reports whether a row with these two marks may be served: not
// withheld, and not a tombstone older than nss.tombstone_retention.
func (s *NSSLookupService) servable(withheldAt, deletedAt *time.Time) bool {
	if withheldAt != nil {
		return false
	}
	return deletedAt == nil || !s.pastRetention(*deletedAt)
}

// pastRetention reports whether a tombstone set at deletedAt has outlived
// nss.tombstone_retention.
func (s *NSSLookupService) pastRetention(deletedAt time.Time) bool {
	return s.now().Sub(deletedAt) > s.config.NSS.TombstoneRetention
}

// retentionCutoff is the oldest deleted_at still served, in UTC because
// SQLite compares stored timestamps as text (see server/dbtime).
func (s *NSSLookupService) retentionCutoff() time.Time {
	return s.now().Add(-s.config.NSS.TombstoneRetention).UTC()
}

// buildUser turns a servable row into the wire response, resolving the
// primary group, the memberships, the status, home and shell. KeyID is
// left for the caller, which knows the key.
func (s *NSSLookupService) buildUser(ctx context.Context, row *posixUserRow) (*apitypes.NSSUserResponse, error) {
	primary, err := s.primaryGroup(ctx, &row.PosixUser)
	if err != nil {
		return nil, err
	}
	gid := primary.gid
	groups := make([]apitypes.NSSMembership, 0, 4)
	if primary.group != nil {
		groups = append(groups, *primary.group)
	}
	memberships, err := s.memberships(ctx, row.UID, gid)
	if err != nil {
		return nil, err
	}
	groups = append(groups, withOriginalGroup(memberships, primary.original)...)

	status := apitypes.NSSStatusActive
	switch {
	case row.DeletedAt != nil:
		status = apitypes.NSSStatusDeleted
	case row.UserDisabledAt != nil || row.DirDisabled:
		status = apitypes.NSSStatusDisabled
	}

	return &apitypes.NSSUserResponse{
		Name:   row.Name,
		UID:    row.UID,
		GID:    gid,
		Groups: groups,
		Home:   s.home(ctx, row),
		Shell:  s.shell(row, status),
		Gecos:  row.Gecos,
		Status: status,
	}, nil
}

// home is the row's home attribute, else nss.default_home with %u replaced
// by the name, else nothing. A value that is not absolute is never served:
// a host would resolve it against whatever directory the caller is in.
func (s *NSSLookupService) home(ctx context.Context, row *posixUserRow) *string {
	home := ""
	switch {
	case row.Home != nil && *row.Home != "":
		home = *row.Home
	case s.config.NSS.DefaultHome != "":
		home = strings.ReplaceAll(s.config.NSS.DefaultHome, "%u", row.Name)
	default:
		return nil
	}
	if !strings.HasPrefix(home, "/") {
		slog.WarnContext(ctx, "not serving an NSS home directory that is not an absolute path",
			"name", row.Name, "dn", row.DN, "home", home)
		return nil
	}
	return &home
}

// shell is nss.disabled_shell for an account that is not active, else the
// row's shell attribute, else nss.default_shell, else nothing.
func (s *NSSLookupService) shell(row *posixUserRow, status string) *string {
	if status != apitypes.NSSStatusActive {
		shell := s.config.NSS.DisabledShell
		return &shell
	}
	if row.Shell != nil && *row.Shell != "" {
		shell := *row.Shell
		return &shell
	}
	if s.config.NSS.DefaultShell != "" {
		shell := s.config.NSS.DefaultShell
		return &shell
	}
	return nil
}

// nssPrimary is what nss.auto_private_groups makes of one account.
type nssPrimary struct {
	// gid is the primary gid served.
	gid uint32
	// group names gid for the groups list, nil when no servable group does.
	group *apitypes.NSSMembership
	// original is, under "true" only, the directory group the entry's own
	// gid attribute names, served as a supplementary group; nil when there
	// is none or it is not servable.
	original *apitypes.NSSMembership
}

// primaryGroup applies nss.auto_private_groups to one account, the way
// sssd does (sssd 2.14.0-beta1-37-g493e37fe; decided 2026-10-18 by the
// owner: match sssd):
//
//   - "true": the primary gid is the uid and the group is the private one,
//     named after the account (sysdb_add_user sets gid = uid in an MPG
//     domain). The entry's own gid, when it has one other than the uid,
//     is served as a supplementary group, as sssd's initgroups adds
//     origPrimaryGroupGidNumber (src/responder/nss/nss_protocol_grent.c
//     sss_nss_protocol_fill_initgr). A directory group already holding the
//     uid as its gid, or the account's name, is errNSSPrivateGroupClash:
//     sssd refuses whichever of the two it caches second (sysdb_add_user
//     and sysdb_add_group return EEXIST), so the outcome there depends on
//     lookup order; here the directory group always wins and the account
//     is not served.
//   - "hybrid": the primary gid is the entry's gid. A private group is
//     made only when the gid equals the uid and no directory group holds
//     it; one that does is the primary group (getgrgid finds it first,
//     and sss_nss_get_hybrid_gid_verify_done drops the private group it
//     would shadow). Otherwise the gid resolves only to a directory group.
//   - "false": the entry's gid, resolving only to a directory group. A gid
//     no group holds is still served, as sssd serves it (manual: "the GID
//     is simply not resolvable").
//
// An account stored with no gid (accepted under "true") has no primary
// gid in the other two, which sssd would never have stored, and is
// errNSSNoGID.
//
// A directory group "holds" a gid or a name whatever its state: a
// tombstone keeps both until it is purged, and a withheld group still owns
// them, so neither may be shadowed by a private group.
func (s *NSSLookupService) primaryGroup(ctx context.Context, user *model.PosixUser) (nssPrimary, error) {
	holders, err := s.groupHolders(ctx, user)
	if err != nil {
		return nssPrimary{}, err
	}
	byGID, nameHeld := holders.byGID, holders.nameHeld(user.Name)
	private := &apitypes.NSSMembership{GID: user.UID, Name: user.Name}

	if s.config.NSS.AutoPrivateGroups == config.AutoPrivateGroupsOn {
		if byGID(user.UID) != nil || nameHeld {
			return nssPrimary{}, errNSSPrivateGroupClash
		}
		p := nssPrimary{gid: user.UID, group: private}
		if user.GID != nil && *user.GID != user.UID && *user.GID != 0 {
			// sssd adds the number even when no group object has it; the
			// groups list carries a name with every gid (the wire
			// contract), so only a servable directory group is listed.
			p.original = s.servableMembership(byGID(*user.GID))
		}
		return p, nil
	}
	if user.GID == nil {
		return nssPrimary{}, errNSSNoGID
	}
	gid := *user.GID
	if s.config.NSS.AutoPrivateGroups == config.AutoPrivateGroupsHybrid && gid == user.UID && byGID(gid) == nil {
		if !nameHeld {
			return nssPrimary{gid: gid, group: private}, nil
		}
		// sssd would still answer getgrgid with the private group while
		// getgrnam finds the directory group of that name. Here the groups
		// list carries both, and ssoossh-nss caches a group stub by name
		// from it, so a host would then answer getgrnam with the private
		// gid: the private group is left out and the account keeps its
		// unnamed gid.
		slog.WarnContext(ctx, "not giving an NSS account a private group: a directory group already has its name",
			"name", user.Name, "uid", user.UID, "dn", user.DN)
		return nssPrimary{gid: gid}, nil
	}
	return nssPrimary{gid: gid, group: s.servableMembership(byGID(gid))}, nil
}

// nssGroupHolders are the directory groups holding an account's uid, its
// stored gid or its name, as primaryGroup weighs them.
type nssGroupHolders []model.PosixGroup

// groupHolders reads the nssGroupHolders of user.
func (s *NSSLookupService) groupHolders(ctx context.Context, user *model.PosixUser) (nssGroupHolders, error) {
	gids := []uint32{user.UID}
	if user.GID != nil {
		gids = append(gids, *user.GID)
	}
	var holders []model.PosixGroup
	if err := s.db.WithContext(ctx).Where("gid IN ? OR name = ?", gids, user.Name).Find(&holders).Error; err != nil {
		return nil, fmt.Errorf("read the groups holding a posix account's gid: %w", err)
	}
	return holders, nil
}

// byGID is the holder of gid, or nil.
func (h nssGroupHolders) byGID(gid uint32) *model.PosixGroup {
	for i := range h {
		if h[i].GID == gid {
			return &h[i]
		}
	}
	return nil
}

// nameHeld reports whether a holder is named name.
func (h nssGroupHolders) nameHeld(name string) bool {
	return slices.ContainsFunc(h, func(g model.PosixGroup) bool { return g.Name == name })
}

// servableMembership names a directory group for a user's groups list, or
// nil when it may not be listed: absent, withheld, or tombstoned (a
// tombstone grants nothing, so it is in nobody's groups).
func (s *NSSLookupService) servableMembership(group *model.PosixGroup) *apitypes.NSSMembership {
	if group == nil || group.WithheldAt != nil || group.DeletedAt != nil {
		return nil
	}
	return &apitypes.NSSMembership{GID: group.GID, Name: group.Name}
}

// memberships lists every live group with a membership row for uid, by gid,
// leaving out primaryGID, which the caller has already put first.
func (s *NSSLookupService) memberships(ctx context.Context, uid, primaryGID uint32) ([]apitypes.NSSMembership, error) {
	var rows []struct {
		GID  uint32 `gorm:"column:gid"`
		Name string `gorm:"column:name"`
	}
	err := s.db.WithContext(ctx).Table("posix_group_members").
		Select("posix_groups.gid AS gid, posix_groups.name AS name").
		Joins("JOIN posix_groups ON posix_groups.gid = posix_group_members.gid").
		Where("posix_group_members.uid = ?", uid).
		Where("posix_groups.gid <> ?", primaryGID).
		Where("posix_groups.withheld_at IS NULL AND posix_groups.deleted_at IS NULL").
		Order("posix_groups.gid").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read posix group memberships: %w", err)
	}
	memberships := make([]apitypes.NSSMembership, 0, len(rows))
	for _, row := range rows {
		memberships = append(memberships, apitypes.NSSMembership{GID: row.GID, Name: row.Name})
	}
	return memberships, nil
}

// withOriginalGroup adds original to memberships, in gid order, unless it
// is nil or already one of them (sssd adds origPrimaryGroupGidNumber only
// when the user is not an explicit member).
func withOriginalGroup(memberships []apitypes.NSSMembership, original *apitypes.NSSMembership) []apitypes.NSSMembership {
	if original == nil {
		return memberships
	}
	i, found := slices.BinarySearchFunc(memberships, original.GID, func(m apitypes.NSSMembership, gid uint32) int {
		return cmp.Compare(m.GID, gid)
	})
	if found {
		return memberships
	}
	return slices.Insert(memberships, i, *original)
}

// GroupByName looks a group up by name, matched byte for byte: a directory
// group, or an account's private group under nss.auto_private_groups.
func (s *NSSLookupService) GroupByName(ctx context.Context, name string) (*apitypes.NSSGroupResponse, error) {
	if err := validNSSName(name); err != nil {
		return nil, err
	}
	read := func(ctx context.Context) (*apitypes.NSSGroupResponse, error) {
		return s.readGroup(ctx, "name = ?", name, func(ctx context.Context) (*posixUserRow, error) {
			return s.readServableUser(ctx, func(db *gorm.DB) *gorm.DB { return db.Where("posix_users.name = ?", name) })
		})
	}
	return s.lookupGroup(ctx, read,
		func(ctx context.Context) (bool, error) { return s.refresher.RefreshGroupByName(ctx, name) },
		func(ctx context.Context) (bool, error) { return s.refresher.RefreshUserByName(ctx, name) })
}

// GroupByGID looks a group up by gid, the same two ways as GroupByName.
func (s *NSSLookupService) GroupByGID(ctx context.Context, gid uint32) (*apitypes.NSSGroupResponse, error) {
	read := func(ctx context.Context) (*apitypes.NSSGroupResponse, error) {
		return s.readGroup(ctx, "gid = ?", gid, func(ctx context.Context) (*posixUserRow, error) {
			return s.readServableUser(ctx, func(db *gorm.DB) *gorm.DB { return db.Where("posix_users.uid = ?", gid) })
		})
	}
	return s.lookupGroup(ctx, read,
		func(ctx context.Context) (bool, error) { return s.refresher.RefreshGroupByGID(ctx, gid) },
		func(ctx context.Context) (bool, error) { return s.refresher.RefreshUserByUID(ctx, gid) })
}

// lookupGroup reads a group, asking the directory when the database has
// nothing servable: for the group itself, and, when private groups are on,
// for the account whose private group it might be.
func (s *NSSLookupService) lookupGroup(ctx context.Context, read func(context.Context) (*apitypes.NSSGroupResponse, error),
	refreshGroup, refreshUser func(context.Context) (bool, error)) (*apitypes.NSSGroupResponse, error) {
	group, err := read(ctx)
	if err != nil || group != nil {
		return group, err
	}
	if s.refresher == nil {
		return nil, errorresponses.NSSNotFound()
	}

	_, refreshErr := refreshGroup(ctx)
	if group, err = read(ctx); err != nil || group != nil {
		return group, err
	}
	if s.config.NSS.AutoPrivateGroups != config.AutoPrivateGroupsOff {
		// A private group is not a directory group, so the group search
		// cannot find it; the account it belongs to can.
		_, userErr := refreshUser(ctx)
		if group, err = read(ctx); err != nil || group != nil {
			return group, err
		}
		refreshErr = errors.Join(refreshErr, userErr)
	}
	return nil, s.missAfterRefresh(ctx, refreshErr)
}

// readGroup reads the directory group matching where, or else the private
// group of the account owner reads, or nil when neither is servable.
func (s *NSSLookupService) readGroup(ctx context.Context, where string, arg any, owner func(context.Context) (*posixUserRow, error)) (*apitypes.NSSGroupResponse, error) {
	var groups []model.PosixGroup
	if err := s.db.WithContext(ctx).Where(where, arg).Limit(1).Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("read posix group: %w", err)
	}
	if len(groups) == 1 {
		group := groups[0]
		if !s.servable(group.WithheldAt, group.DeletedAt) {
			// The row still holds the name and number, so no private group
			// may answer in its place.
			return nil, nil
		}
		return s.buildGroup(ctx, &group)
	}

	if s.config.NSS.AutoPrivateGroups == config.AutoPrivateGroupsOff {
		return nil, nil
	}
	user, err := owner(ctx)
	if err != nil || user == nil {
		return nil, err
	}
	primary, err := s.primaryGroup(ctx, &user.PosixUser)
	if errors.Is(err, errNSSPrivateGroupClash) || errors.Is(err, errNSSNoGID) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if primary.group == nil || *primary.group != (apitypes.NSSMembership{GID: user.UID, Name: user.Name}) {
		// The account's primary group is a directory group or none, not a
		// private one. A directory group matching the lookup would have
		// been found above, so equality here means the private group.
		return nil, nil
	}
	return &apitypes.NSSGroupResponse{Name: user.Name, GID: user.UID, Members: []string{}}, nil
}

// buildGroup turns a servable directory group into the wire response. A
// tombstoned group is served with no members, so ls -l still names it but
// it grants nothing. The member list is capped at nss.max_group_members.
func (s *NSSLookupService) buildGroup(ctx context.Context, group *model.PosixGroup) (*apitypes.NSSGroupResponse, error) {
	response := &apitypes.NSSGroupResponse{Name: group.Name, GID: group.GID, Members: []string{}}
	if group.DeletedAt != nil {
		return response, nil
	}

	limit := s.config.NSS.MaxGroupMembers
	var names []string
	err := s.db.WithContext(ctx).Table("posix_group_members").
		Joins("JOIN posix_users ON posix_users.uid = posix_group_members.uid").
		Where("posix_group_members.gid = ?", group.GID).
		Where("posix_users.withheld_at IS NULL").
		Where("posix_users.deleted_at IS NULL OR posix_users.deleted_at >= ?", s.retentionCutoff()).
		// By uid rather than name: numbers order the same on both
		// backends, so a truncated list is the same list everywhere.
		Order("posix_users.uid").
		Limit(limit+1).
		Pluck("posix_users.name", &names).Error
	if err != nil {
		return nil, fmt.Errorf("read posix group members: %w", err)
	}
	if len(names) > limit {
		names = names[:limit]
		response.MembersTruncated = true
		slog.WarnContext(ctx, "an NSS group has more members than nss.max_group_members; serving the first ones by uid",
			"group", group.Name, "gid", group.GID, "max_group_members", limit)
	}
	if names != nil {
		response.Members = names
	}
	return response, nil
}
