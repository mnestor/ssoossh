// Package policy holds ssoosshd-nss's host-side rules: the local
// short-circuit against /etc/passwd and /etc/group, the sanity rules every
// upstream answer must pass, host overrides, and the home and shell
// resolution, all from Part 3 of docs/proposals/nss-directory ("Request
// path" step 4 and "Policy and sanity rules").
//
// The daemon runs the full set twice: on the upstream answer before it is
// cached, so a failing entry is negative-cached, and again on every read,
// so an edit to nss.yaml's lists, /etc/passwd or the override file applies
// to cached entries at once, with no cache flush.
package policy

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/posixname"
	"github.com/mnestor/ssoossh/nssd/config"
)

// ErrRejected wraps every reason a name, id or record is not served. The
// daemon answers NoRecordFound for all of them.
var ErrRejected = errors.New("refused by host policy")

// rejected builds an ErrRejected with a reason for the log.
func rejected(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRejected}, args...)...)
}

// Paths are the host files the policy reads.
type Paths struct {
	// Passwd is /etc/passwd.
	Passwd string
	// Group is /etc/group.
	Group string
	// Shells is /etc/shells.
	Shells string
	// Overrides is the host override file.
	Overrides string
}

// DefaultPaths returns the system's files.
func DefaultPaths() Paths {
	return Paths{
		Passwd:    "/etc/passwd",
		Group:     "/etc/group",
		Shells:    "/etc/shells",
		Overrides: config.DefaultOverridesPath,
	}
}

// Engine applies the policy. It is safe for concurrent use.
type Engine struct {
	cfg    *config.Config
	log    *slog.Logger
	passwd *watched[localDB]
	group  *watched[localDB]
	shells *watched[[]string]

	// overridesFile re-reads the override file on change; lastGood is
	// what the last successful read gave, kept when a later edit fails to
	// parse so one typo does not take every overridden user away.
	overridesFile *watched[config.Overrides]
	overridesMu   sync.Mutex
	lastGood      config.Overrides
	lastErr       string

	// found remembers refused directory groups for warn-once and status;
	// now stamps them.
	found *findings
	now   func() time.Time
}

// New returns an Engine over cfg and the files in paths.
func New(cfg *config.Config, paths Paths, log *slog.Logger) *Engine {
	return &Engine{
		cfg:           cfg,
		log:           log,
		passwd:        newWatched(paths.Passwd, readLocalDB),
		group:         newWatched(paths.Group, readLocalDB),
		shells:        newWatched(paths.Shells, readShells),
		overridesFile: newWatched(paths.Overrides, config.LoadOverrides),
		lastGood:      config.Overrides{},
		found:         newFindings(),
		now:           time.Now,
	}
}

// Reload makes the next lookup re-read every host file, for SIGHUP and
// the admin socket's reload after `ssoosshd-nss override` edits the file.
func (e *Engine) Reload() {
	e.passwd.invalidate()
	e.group.invalidate()
	e.shells.invalidate()
	e.overridesFile.invalidate()
}

// Findings returns the group name collisions and id range refusals met
// since the daemon started or last cleared them, for status.
func (e *Engine) Findings() Findings {
	return e.found.snapshot()
}

// ClearFindings forgets them, for SIGHUP, so each is logged again when
// next met.
func (e *Engine) ClearFindings() {
	e.found.clear()
}

// CheckUserName is the short-circuit for a user name: the name rules, then
// filter_users, then /etc/passwd. A refused name never reaches the server.
func (e *Engine) CheckUserName(name string) error {
	if err := posixname.Valid(name); err != nil {
		return fmt.Errorf("%w: %w", ErrRejected, err)
	}
	if slices.Contains(e.cfg.FilterUsers, name) {
		return rejected("user %q is in filter_users", name)
	}
	local, err := e.passwd.get()
	if err != nil {
		// Fail closed: a host that cannot read its own passwd cannot tell
		// a directory name from a local one.
		return rejected("cannot read the local users: %v", err)
	}
	if local.hasName(name) {
		return rejected("user %q is a local account", name)
	}
	return nil
}

// CheckUID is the short-circuit for a uid: min_id..max_id, then
// /etc/passwd.
func (e *Engine) CheckUID(uid uint32) error {
	if !e.cfg.InRange(uid) {
		return rejected("uid %d is outside min_id..max_id", uid)
	}
	local, err := e.passwd.get()
	if err != nil {
		return rejected("cannot read the local users: %v", err)
	}
	if local.hasID(uid) {
		return rejected("uid %d belongs to a local account", uid)
	}
	return nil
}

// CheckGroupName is the short-circuit for a group name.
func (e *Engine) CheckGroupName(name string) error {
	if err := posixname.Valid(name); err != nil {
		return fmt.Errorf("%w: %w", ErrRejected, err)
	}
	if slices.Contains(e.cfg.FilterGroups, name) {
		return rejected("group %q is in filter_groups", name)
	}
	local, err := e.group.get()
	if err != nil {
		return rejected("cannot read the local groups: %v", err)
	}
	if local.hasName(name) {
		return rejected("group %q is a local group", name)
	}
	return nil
}

// CheckGID is the short-circuit for a gid.
func (e *Engine) CheckGID(gid uint32) error {
	if !e.cfg.InRange(gid) {
		return rejected("gid %d is outside min_id..max_id", gid)
	}
	local, err := e.group.get()
	if err != nil {
		return rejected("cannot read the local groups: %v", err)
	}
	if local.hasID(gid) {
		return rejected("gid %d belongs to a local group", gid)
	}
	return nil
}

// User is a user answer after policy: what the userdb record is built
// from.
type User struct {
	// Name is the login name, verbatim.
	Name string
	// UID and GID are after overrides and override_gid.
	UID, GID uint32
	// RealName is the sanitised gecos.
	RealName string
	// Home and Shell are resolved.
	Home, Shell string
	// Locked is set for a disabled or deleted account.
	Locked bool
	// Status is the server's status.
	Status string
	// Groups are the group names to answer GetMemberships with, after
	// the gid rule (see memberships): initgroups resolves each name
	// through /etc/group first (README U12), so a name is only listed
	// when it resolves to the membership's own gid.
	Groups []string
}

// User applies the whole policy to a user answer: host override, then the
// sanity rules, then gid, home and shell resolution. An error wraps
// ErrRejected and says why.
func (e *Engine) User(resp *apitypes.NSSUserResponse) (User, error) {
	if resp == nil {
		return User{}, rejected("the server sent no user record")
	}
	name := resp.Name
	if err := posixname.Valid(name); err != nil {
		return User{}, fmt.Errorf("%w: server name: %w", ErrRejected, err)
	}

	// The override goes first so the sanity rules below see, and can
	// refuse, the values it sets: an override uid below min_id fails the
	// range rule like a server uid would.
	uid, gid, home, shell := resp.UID, resp.GID, deref(resp.Home), deref(resp.Shell)
	if o, ok := e.overrides()[name]; ok {
		if o.UID != nil {
			uid = *o.UID
		}
		if o.GID != nil {
			gid = *o.GID
		}
		if o.Home != "" {
			home = o.Home
		}
		if o.Shell != "" {
			// A host override replaces the directory's shell; only the
			// global override_shell still wins over it.
			shell = o.Shell
		}
	}

	if err := e.sanity(name, uid, gid); err != nil {
		return User{}, err
	}
	// require_groups is checked against the memberships the gid rule
	// keeps, so a directory group whose name collides with a local group
	// cannot satisfy it by name.
	groups, kept, err := e.memberships(resp.Groups)
	if err != nil {
		return User{}, err
	}
	if len(e.cfg.RequireGroups) > 0 && !slices.ContainsFunc(kept, func(g string) bool {
		return slices.Contains(e.cfg.RequireGroups, g)
	}) {
		return User{}, rejected("user %q is in none of require_groups", name)
	}
	// override_gid applies after the range rule, so it may name a shared
	// group below min_id such as gid 100 "users". It is never 0: that
	// value means unset.
	if e.cfg.OverrideGID != 0 {
		gid = e.cfg.OverrideGID
	}

	resolvedHome, err := e.home(name, uid, deref(resp.Home), home)
	if err != nil {
		return User{}, err
	}
	u := User{Name: name, UID: uid, GID: gid, Home: resolvedHome, Status: resp.Status}
	u.RealName, _ = posixname.SanitizeGECOS(resp.Gecos, name)
	u.Groups = groups

	switch resp.Status {
	case apitypes.NSSStatusActive:
		u.Shell = e.shell(shell)
	default:
		// disabled, deleted, and anything this daemon does not know yet,
		// which fails closed: locked, with the disabled shell, which is
		// exempt from the shells check (README R4).
		u.Shell, u.Locked = e.cfg.DisabledShell, true
	}
	return u, nil
}

// sanity applies the rules every served user must pass, in the order the
// proposal lists them.
func (e *Engine) sanity(name string, uid, gid uint32) error {
	if slices.Contains(e.cfg.FilterUsers, name) {
		return rejected("user %q is in filter_users", name)
	}
	if !e.cfg.InRange(uid) {
		return rejected("user %q: uid %d is outside min_id..max_id", name, uid)
	}
	if !e.cfg.InRange(gid) {
		return rejected("user %q: gid %d is outside min_id..max_id", name, gid)
	}
	local, err := e.passwd.get()
	if err != nil {
		return rejected("cannot read the local users: %v", err)
	}
	if local.hasName(name) {
		return rejected("user %q is a local account", name)
	}
	// A directory uid that a local account already uses would hand that
	// account's files to the directory user.
	if local.hasID(uid) {
		return rejected("user %q: uid %d belongs to a local account", name, uid)
	}
	return nil
}

// memberships applies the gid rule (decided 2026-10-18 by the owner) to a
// user's memberships. systemd userdb carries a membership as a group name,
// and initgroups resolves that name through /etc/group first (README U12),
// so a name is only ever listed when it resolves to the membership's gid.
// For each membership, first match wins:
//
//  1. gid outside min_id..max_id: dropped, as a group answer is.
//  2. gid of a local group: the local group's name, which resolves to
//     that same gid through files.
//  3. name of a local group (with another gid): a name collision, dropped
//     and warned about; the member does not get the directory gid here.
//  4. invalid name, or in filter_groups: dropped.
//  5. otherwise the directory name.
//
// filter_groups also applies to the local name of rule 2, since it is a
// name this daemon would answer with. groups is the names to list, kept
// the directory names of the memberships listed, for require_groups.
func (e *Engine) memberships(in []apitypes.NSSMembership) (groups, kept []string, err error) {
	local, err := e.group.get()
	if err != nil {
		// Fail closed: without /etc/group no name can be checked against
		// the gid it resolves to.
		return nil, nil, rejected("cannot read the local groups: %v", err)
	}
	groups = make([]string, 0, len(in))
	for _, m := range in {
		name, ok := e.membership(local, m)
		if !ok {
			continue
		}
		kept = append(kept, m.Name)
		if !slices.Contains(groups, name) {
			groups = append(groups, name)
		}
	}
	return groups, kept, nil
}

// membership is the gid rule for one membership: the name to list, or
// false to drop it.
func (e *Engine) membership(local localDB, m apitypes.NSSMembership) (string, bool) {
	if !e.cfg.InRange(m.GID) {
		e.noteRefused(m.Name, m.GID)
		return "", false
	}
	if name, ok := local.nameOf(m.GID); ok {
		if posixname.Valid(name) != nil || slices.Contains(e.cfg.FilterGroups, name) {
			return "", false
		}
		if name != m.Name {
			e.log.Debug("a directory membership's gid is a local group's; listing the local name",
				"group", m.Name, "gid", m.GID, "local_group", name)
		}
		return name, true
	}
	if e.noteCollision(local, m.Name, m.GID) {
		return "", false
	}
	if posixname.Valid(m.Name) != nil || slices.Contains(e.cfg.FilterGroups, m.Name) {
		return "", false
	}
	return m.Name, true
}

// noteGroup records a directory group from a server answer, or one of a
// user's memberships, that the gid rule refuses: one outside the id range,
// or one whose name a local group with another gid has. Only answers reach
// it, never the local-name short-circuit, so it cannot flood the log.
func (e *Engine) noteGroup(name string, gid uint32) {
	if !e.cfg.InRange(gid) {
		e.noteRefused(name, gid)
		return
	}
	local, err := e.group.get()
	if err != nil {
		// The caller's own check fails closed on the same error.
		return
	}
	if _, ok := local.nameOf(gid); !ok {
		e.noteCollision(local, name, gid)
	}
}

// noteRefused records and, the first time, logs a directory group outside
// min_id..max_id.
func (e *Engine) noteRefused(name string, gid uint32) {
	reason := fmt.Sprintf("gid %d is outside min_id..max_id (%d..%s)", gid, e.cfg.MinID, maxIDText(e.cfg.MaxID))
	if e.found.refused(RefusedGroup{Name: name, GID: gid, Reason: reason, FirstSeen: e.now().UTC()}) {
		e.log.Warn("a directory group's gid is outside min_id..max_id; it is not served on this host and its members do not get it here",
			"group", name, "gid", gid, "min_id", e.cfg.MinID, "max_id", e.cfg.MaxID)
	}
}

// noteCollision reports whether name is a local group's name, recording
// and, the first time, logging it as a collision. The caller has checked
// that gid is no local group's.
func (e *Engine) noteCollision(local localDB, name string, gid uint32) bool {
	id, ok := local.idOf(name)
	if !ok {
		return false
	}
	c := NameCollision{Name: name, GID: gid, FirstSeen: e.now().UTC()}
	attrs := []any{"group", name, "gid", gid}
	if id.known {
		c.LocalGID = &id.id
		attrs = append(attrs, "local_gid", id.id)
	}
	if e.found.collision(c) {
		e.log.Warn("a directory group has the same name as a local group; it is not resolved on this host: "+
			"its members do not get its gid here, files owned by its gid show the number, and the local group is unaffected. "+
			"Rename one of the two to fix it", attrs...)
	}
	return true
}

// maxIDText is max_id for a message, "unlimited" when unset.
func maxIDText(maxID uint32) string {
	if maxID == 0 {
		return "unlimited"
	}
	return strconv.FormatUint(uint64(maxID), 10)
}

// Group is a group answer after policy.
type Group struct {
	// Name is the group name, verbatim.
	Name string
	// GID is the group id.
	GID uint32
	// Members are the member names worth answering GetMemberships with;
	// nil when ignore_group_members is set.
	Members []string
}

// Group applies the policy to a group answer.
func (e *Engine) Group(resp *apitypes.NSSGroupResponse) (Group, error) {
	if resp == nil {
		return Group{}, rejected("the server sent no group record")
	}
	// A colliding name or an out-of-range gid is still refused below, as
	// by the short-circuit; noting it first makes it visible.
	e.noteGroup(resp.Name, resp.GID)
	if err := e.CheckGroupName(resp.Name); err != nil {
		return Group{}, err
	}
	if err := e.CheckGID(resp.GID); err != nil {
		return Group{}, err
	}
	g := Group{Name: resp.Name, GID: resp.GID}
	if e.cfg.IgnoreGroupMembers {
		return g, nil
	}
	if resp.MembersTruncated {
		e.log.Warn("the server truncated a group's member list", "group", resp.Name, "members", len(resp.Members))
	}
	local, err := e.passwd.get()
	if err != nil {
		return Group{}, rejected("cannot read the local users: %v", err)
	}
	for _, m := range resp.Members {
		// A local account or a filtered name is never made a member from
		// the directory: GetMemberships by that user's name answers
		// NoRecordFound (README U8), and the group's own list agrees.
		if posixname.Valid(m) != nil || slices.Contains(e.cfg.FilterUsers, m) || local.hasName(m) || slices.Contains(g.Members, m) {
			continue
		}
		g.Members = append(g.Members, m)
	}
	return g, nil
}

// GroupStub checks a membership taken from a user answer before it fills
// the group cache: the same rules as a group answer, without members.
func (e *Engine) GroupStub(m apitypes.NSSMembership) error {
	e.noteGroup(m.Name, m.GID)
	if err := e.CheckGroupName(m.Name); err != nil {
		return err
	}
	return e.CheckGID(m.GID)
}

// home resolves the home directory: override_homedir, then the server's
// (or the host override's) home, then fallback_homedir. The first that is
// a valid absolute path after expansion wins. serverHome is what %h
// expands to.
func (e *Engine) home(name string, uid uint32, serverHome, home string) (string, error) {
	candidates := []string{
		e.expand(e.cfg.OverrideHomedir, name, uid, serverHome),
		home,
		e.expand(e.cfg.FallbackHomedir, name, uid, serverHome),
	}
	for _, c := range candidates {
		if c != "" && config.ValidPath(c) {
			return c, nil
		}
	}
	return "", rejected("user %q has no absolute home directory", name)
}

// expand substitutes %u (name), %U (uid), %d (domain), %h (the server's
// home) and %% in a homedir template. An unknown sequence is kept as
// written. A template that uses %h when the server sent no home expands to
// nothing, so "%h/x" never becomes "/x".
func (e *Engine) expand(template, name string, uid uint32, serverHome string) string {
	if template == "" || (serverHome == "" && strings.Contains(strings.ReplaceAll(template, "%%", ""), "%h")) {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(template); i++ {
		if template[i] != '%' || i+1 == len(template) {
			b.WriteByte(template[i])
			continue
		}
		i++
		switch template[i] {
		case 'u':
			b.WriteString(name)
		case 'U':
			b.WriteString(strconv.FormatUint(uint64(uid), 10))
		case 'd':
			b.WriteString(e.cfg.Domain)
		case 'h':
			b.WriteString(serverHome)
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(template[i])
		}
	}
	return b.String()
}

// shell resolves an active account's shell, first acceptable wins:
// override_shell, then the directory's (or host override's) shell, then
// shell_fallback. The fallback is the operator's own last resort and is
// used even if it is not otherwise acceptable, rather than refusing the
// account outright. The person never chooses: the shell comes from the
// directory and host policy only.
func (e *Engine) shell(shell string) string {
	shells, err := e.shells.get()
	if err != nil {
		e.log.Error("cannot read /etc/shells; only allowed_shells can match", "error", err)
		shells = nil
	}
	candidates := []string{e.cfg.OverrideShell, shell}
	for _, c := range candidates {
		if c != "" && e.acceptable(c, shells) {
			return c
		}
	}
	return e.cfg.ShellFallback
}

// acceptable reports whether a shell may be served: a valid path, listed
// in allowed_shells (or in /etc/shells when that list is empty), and not
// in vetoed_shells.
func (e *Engine) acceptable(shell string, shells []string) bool {
	if !config.ValidPath(shell) || slices.Contains(e.cfg.VetoedShells, shell) {
		return false
	}
	if len(e.cfg.AllowedShells) > 0 {
		return slices.Contains(e.cfg.AllowedShells, shell)
	}
	return slices.Contains(shells, shell)
}

// overrides returns the current host overrides. An override file that
// fails to read keeps the last good overrides in force and is logged once
// per failure, not per lookup.
func (e *Engine) overrides() config.Overrides {
	current, err := e.overridesFile.get()
	e.overridesMu.Lock()
	defer e.overridesMu.Unlock()
	if err != nil {
		if msg := err.Error(); msg != e.lastErr {
			e.log.Error("cannot read the host override file; keeping the last good overrides", "error", err)
			e.lastErr = msg
		}
		return e.lastGood
	}
	e.lastGood, e.lastErr = current, ""
	return current
}

// Overrides returns the overrides in force, for `override list` and
// status.
func (e *Engine) Overrides() config.Overrides {
	return e.overrides()
}

// deref returns *s, or "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
