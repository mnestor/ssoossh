// Package userdb is ssoosshd-nss's userdb service: systemd's
// io.systemd.UserDatabase interface (docs/USER_GROUP_API.md in systemd),
// the daemon's own com.github.mnestor.ssoossh.NSS.RefreshUser, and
// org.varlink.service.GetInfo, over the cache, the policy and the upstream
// client. Part 3 of docs/proposals/nss-directory ("Userdb service",
// "Record mapping", "Request path").
//
// One Daemon holds every dependency, and each varlink method is a method
// on it. nssd/server owns the sockets, deadlines and peer credentials and
// hands each decoded call to Handle.
package userdb

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/upstream"
)

// ServiceName is the userdb service name callers must send in every call,
// and the name of the socket under /run/systemd/userdb/.
const ServiceName = "com.github.mnestor.ssoossh"

// Interfaces this service implements.
const (
	// InterfaceUserDatabase is systemd's userdb interface.
	InterfaceUserDatabase = "io.systemd.UserDatabase"
	// InterfaceNSS is the daemon's own interface.
	InterfaceNSS = "com.github.mnestor.ssoossh.NSS"
	// InterfaceVarlink is the interface every varlink service implements.
	InterfaceVarlink = "org.varlink.service"
)

// Methods.
const (
	// MethodGetUserRecord looks a user up by uid or name.
	MethodGetUserRecord = InterfaceUserDatabase + ".GetUserRecord"
	// MethodGetGroupRecord looks a group up by gid or name.
	MethodGetGroupRecord = InterfaceUserDatabase + ".GetGroupRecord"
	// MethodGetMemberships lists memberships by user or group name.
	MethodGetMemberships = InterfaceUserDatabase + ".GetMemberships"
	// MethodRefreshUser asks for a background refresh of one user.
	MethodRefreshUser = InterfaceNSS + ".RefreshUser"
	// MethodGetInfo describes the service, for `varlinkctl info`.
	MethodGetInfo = InterfaceVarlink + ".GetInfo"
)

// io.systemd.UserDatabase errors.
const (
	// ErrorNoRecordFound is a name or id this service does not serve.
	ErrorNoRecordFound = InterfaceUserDatabase + ".NoRecordFound"
	// ErrorBadService is a call whose service is not ServiceName.
	ErrorBadService = InterfaceUserDatabase + ".BadService"
	// ErrorServiceNotAvailable is a lookup that could not be answered in
	// time or at all; glibc callers see it as not found (README U3).
	ErrorServiceNotAvailable = InterfaceUserDatabase + ".ServiceNotAvailable"
	// ErrorConflictingRecordFound is a uid and a name naming different
	// records.
	ErrorConflictingRecordFound = InterfaceUserDatabase + ".ConflictingRecordFound"
	// ErrorEnumerationNotSupported is a call with no key.
	ErrorEnumerationNotSupported = InterfaceUserDatabase + ".EnumerationNotSupported"
	// ErrorNonMatchingRecordFound is a record found that the call's
	// filters exclude.
	ErrorNonMatchingRecordFound = InterfaceUserDatabase + ".NonMatchingRecordFound"
)

// Outcomes of a resolution, mapped to varlink errors by the handlers.
var (
	// errNoRecord answers NoRecordFound.
	errNoRecord = errors.New("no record")
	// errUnavailable answers ServiceNotAvailable.
	errUnavailable = errors.New("service not available")
)

// maxDebounce bounds the RefreshUser debounce map, pruned past it.
const maxDebounce = 10000

// Peer is what the server learnt about a caller from its socket.
type Peer struct {
	// UID is the caller's uid from SO_PEERCRED.
	UID uint32
	// SSHD is set when the caller is uid 0 and its systemd cgroup is one
	// of sshd_units.
	SSHD bool
}

// Options are a Daemon's dependencies.
type Options struct {
	// Config is the daemon's configuration.
	Config *config.Config
	// Store is the record cache.
	Store cache.Store
	// Upstream is the directory.
	Upstream upstream.Upstream
	// Policy is the host policy.
	Policy *policy.Engine
	// Clock tells the time for every TTL decision.
	Clock cache.Clock
	// Log receives the daemon's log.
	Log *slog.Logger
}

// Daemon answers userdb calls.
type Daemon struct {
	cfg       *config.Config
	store     cache.Store
	upstream  upstream.Upstream
	policy    *policy.Engine
	clock     cache.Clock
	log       *slog.Logger
	ttl       cache.TTL
	negative  *cache.Negative
	flight    cache.Flight
	refresher *cache.Refresher
	started   time.Time

	debounceMu sync.Mutex
	debounce   map[string]time.Time
}

// New returns a Daemon. Run must be running for background refreshes to
// happen; lookups work without it.
func New(opts Options) *Daemon {
	d := &Daemon{
		cfg:      opts.Config,
		store:    opts.Store,
		upstream: opts.Upstream,
		policy:   opts.Policy,
		clock:    opts.Clock,
		log:      opts.Log,
		ttl: cache.TTL{
			NowaitPercentage: opts.Config.EntryCacheNowaitPercentage,
			ExpireOffline:    opts.Config.CacheExpireOffline,
		},
		negative: cache.NewNegative(opts.Config.EntryNegativeTimeout, opts.Clock),
		debounce: map[string]time.Time{},
		started:  opts.Clock.Now(),
	}
	d.refresher = cache.NewRefresher(opts.Config.RefreshRate, d.refresh)
	return d
}

// Run does the background work until ctx ends: the refresh queue and, when
// configured, the periodic sweep.
func (d *Daemon) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { d.refresher.Run(ctx) })
	if interval := d.cfg.SweepInterval(); interval > 0 {
		wg.Go(func() { d.sweepLoop(ctx, interval) })
	}
	wg.Wait()
}

// sweepLoop runs sweep every interval, give or take a tenth, so a fleet
// booted together does not refresh in lockstep.
func (d *Daemon) sweepLoop(ctx context.Context, interval time.Duration) {
	for {
		jitter := time.Duration(rand.Int64N(int64(interval)/5+1)) - interval/10 //nolint:gosec // G404: spreads load, guards nothing.
		timer := time.NewTimer(interval + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			d.sweep(ctx)
		}
	}
}

// sweep queues a refresh for every cached user, and every group that holds
// members, whose nowait share of the TTL has passed. The queue's rate
// limit bounds the upstream traffic this causes.
func (d *Daemon) sweep(ctx context.Context) {
	now := d.clock.Now()
	users, err := d.store.Users(ctx)
	if err != nil {
		d.log.Error("cannot list the user cache for the sweep", "error", err)
	}
	for _, u := range users {
		if d.ttl.Freshness(u.FetchedAt, u.ExpiresAt, u.Stale, now) != cache.Fresh {
			d.refresher.Enqueue(userKey{name: u.Name}.String())
		}
	}
	groups, err := d.store.Groups(ctx)
	if err != nil {
		d.log.Error("cannot list the group cache for the sweep", "error", err)
	}
	for _, g := range groups {
		if g.Members && d.ttl.Freshness(g.FetchedAt, g.ExpiresAt, g.Stale, now) != cache.Fresh {
			d.refresher.Enqueue(groupKey{name: g.Name}.String())
		}
	}
}

// refresh is the refresh queue's worker: one forced upstream fetch for a
// key, waited for in full so the queue's rate is the upstream rate.
func (d *Daemon) refresh(ctx context.Context, key string) {
	if uk, ok := parseUserKey(key); ok {
		if _, err := d.fetchUser(ctx, uk); err != nil && !errors.Is(err, errNoRecord) {
			d.log.Debug("background refresh failed", "key", key, "error", err)
		}
		return
	}
	if gk, ok := parseGroupKey(key); ok {
		if _, err := d.fetchGroup(ctx, gk); err != nil && !errors.Is(err, errNoRecord) {
			d.log.Debug("background refresh failed", "key", key, "error", err)
		}
	}
}

// Handle answers one call on conn. ctx carries the answer deadline. A
// oneway call is acted on and never answered. The error is a failed write,
// after which the server closes the connection.
func (d *Daemon) Handle(ctx context.Context, call varlink.Call, peer Peer, conn *varlink.ServerConn) error {
	var res result
	switch call.Method {
	case MethodGetUserRecord:
		res = d.getUserRecord(ctx, call, peer)
	case MethodGetGroupRecord:
		res = d.getGroupRecord(ctx, call)
	case MethodGetMemberships:
		res = d.getMemberships(ctx, call)
	case MethodRefreshUser:
		res = d.refreshUser(call)
	case MethodGetInfo:
		res = d.getInfo()
	default:
		res = unknownMethod(call.Method)
	}
	if call.Oneway {
		return nil
	}
	return res.write(conn)
}

// result is a handler's answer: an error, one reply, or a stream.
type result struct {
	errName   string
	errParams any
	replies   []any
}

// reply is a one-reply result.
func reply(params any) result { return result{replies: []any{params}} }

// failure is an error result.
func failure(name string, params any) result { return result{errName: name, errParams: params} }

// write sends the result: the error, or each reply with all but the last
// marked continues.
func (r result) write(conn *varlink.ServerConn) error {
	if r.errName != "" {
		return conn.ReplyError(r.errName, r.errParams)
	}
	for i, p := range r.replies {
		if i < len(r.replies)-1 {
			if err := conn.ReplyContinues(p); err != nil {
				return err
			}
			continue
		}
		return conn.Reply(p)
	}
	// not covered: every handler returns an error or at least one reply.
	return conn.Reply(nil)
}

// unknownMethod answers a method this service does not have: unknown
// interface or unknown method, as varlink distinguishes them.
func unknownMethod(method string) result {
	iface, _, found := cutLast(method, '.')
	switch {
	case !found || (iface != InterfaceUserDatabase && iface != InterfaceNSS && iface != InterfaceVarlink):
		return failure(varlink.ErrorInterfaceNotFound, map[string]string{"interface": iface})
	default:
		return failure(varlink.ErrorMethodNotFound, map[string]string{"method": method})
	}
}

// cutLast splits s around its last sep.
func cutLast(s string, sep byte) (before, after string, found bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// getInfo answers org.varlink.service.GetInfo.
func (d *Daemon) getInfo() result {
	return reply(info())
}
