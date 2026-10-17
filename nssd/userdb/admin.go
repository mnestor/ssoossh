package userdb

import (
	"context"
	"fmt"
	"time"

	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/upstream"
)

// Status is what `ssoosshd-nss status` shows of a running daemon.
type Status struct {
	// Upstream is the directory connection's state.
	Upstream upstream.Status `json:"upstream"`
	// CacheMode is persistent or memory.
	CacheMode string `json:"cache_mode"`
	// Cache counts the cached entries.
	Cache cache.Counts `json:"cache"`
	// CacheError is set when the counts could not be read.
	CacheError string `json:"cache_error,omitempty"`
	// NegativeEntries counts remembered misses.
	NegativeEntries int `json:"negative_entries"`
	// PendingRefreshes counts queued background refreshes.
	PendingRefreshes int `json:"pending_refreshes"`
	// Overrides counts host overrides in force.
	Overrides int `json:"overrides"`
	// StartedAt is when the daemon started.
	StartedAt time.Time `json:"started_at"`
}

// Status reports the daemon's state.
func (d *Daemon) Status(ctx context.Context) Status {
	s := Status{
		Upstream:         d.upstream.Status(),
		CacheMode:        d.cfg.CacheMode,
		NegativeEntries:  d.negative.Len(),
		PendingRefreshes: d.refresher.Pending(),
		Overrides:        len(d.policy.Overrides()),
		StartedAt:        d.started,
	}
	counts, err := d.store.Counts(ctx)
	if err != nil {
		s.CacheError = err.Error()
	}
	s.Cache = counts
	return s
}

// Invalidate marks entries stale, `ssoosshd-nss cache`'s work: they are
// refetched on their next lookup and still served while the directory is
// unreachable. Never deleted. The matching remembered misses are forgotten
// too, so a name just added to the directory resolves at once.
func (d *Daemon) Invalidate(ctx context.Context, sel cache.Selector) (int, error) {
	n, err := d.store.MarkStale(ctx, sel)
	if err != nil {
		return n, fmt.Errorf("invalidate the cache: %w", err)
	}
	if sel.Name == "" {
		d.negative.Clear()
		return n, nil
	}
	if sel.Users {
		d.negative.Delete(userKey{name: sel.Name}.String())
	}
	if sel.Groups {
		d.negative.Delete(groupKey{name: sel.Name}.String())
	}
	return n, nil
}

// ReloadPolicy re-reads the host files, for the admin socket's reload
// after `ssoosshd-nss override` changed the override file.
func (d *Daemon) ReloadPolicy() {
	d.policy.Reload()
	d.log.Info("host files reloaded")
}

// Reset is SIGHUP: re-read the host files and the fleet key, and clear
// the 401 latch and the offline backoff.
func (d *Daemon) Reset() {
	d.ReloadPolicy()
	d.upstream.Reset()
}
