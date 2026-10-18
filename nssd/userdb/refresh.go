package userdb

import (
	"github.com/mnestor/ssoossh/internal/varlink"
)

// refreshUser answers com.github.mnestor.ssoossh.NSS.RefreshUser, sent
// one-way by `ssoossh host principals` before sshd's login lookups. It
// replies at once and queues a background refresh, at most one per name
// per login_soft_ttl and through the shared refresh_rate limiter, so a
// local user calling it in a loop costs nothing upstream (README R6). A
// name the short-circuit refuses is ignored without a word: the caller
// learns nothing it could not learn from GetUserRecord.
//
// service is optional here, since this is the daemon's own interface; when
// sent it must be ServiceName.
func (d *Daemon) refreshUser(call varlink.Call) result {
	var p refreshParams
	if res, ok := decodeParams(call, &p); !ok {
		return res
	}
	if p.Service != nil {
		if res, ok := checkService(p.Service); !ok {
			return res
		}
	}
	if p.UserName == nil {
		return invalidParameter("userName")
	}
	key := userKey{name: *p.UserName}
	if d.checkUserKey(key) == nil && d.takeDebounce(key.name) {
		d.refresher.Enqueue(key.String())
	}
	return reply(nil)
}

// takeDebounce reports whether name may be refreshed now, and if so
// records that it was.
func (d *Daemon) takeDebounce(name string) bool {
	d.debounceMu.Lock()
	defer d.debounceMu.Unlock()
	now := d.clock.Now()
	if last, ok := d.debounce[name]; ok && now.Sub(last) < d.cfg.LoginSoftTTL {
		return false
	}
	if len(d.debounce) >= maxDebounce {
		for n, last := range d.debounce {
			if now.Sub(last) >= d.cfg.LoginSoftTTL {
				delete(d.debounce, n)
			}
		}
		if len(d.debounce) >= maxDebounce {
			// Every entry is recent: a flood of distinct names. Dropping
			// the request is safe, the entry still ages out at its TTL.
			return false
		}
	}
	d.debounce[name] = now
	return true
}
