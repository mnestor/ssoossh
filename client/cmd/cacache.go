package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
)

// caCacheTTL is how long a cached CA key set is used without asking the
// server again. A CA key changes on the order of years; a day is short
// enough that a rotation converges on its own and long enough that the
// fetch is invisible next to the certificates it supports.
const caCacheTTL = 24 * time.Hour

// caRefreshTimeout bounds a refresh that a person is waiting on. The client
// runs from ssh_config's Match exec, so this delay lands in front of an SSH
// connection: better to keep yesterday's key set and move on than to hold a
// terminal open while an unreachable server times out. It applies only to
// the refresh — the first fetch, with nothing cached to fall back to, uses
// the API client's own timeout.
const caRefreshTimeout = 2 * time.Second

// caCache is the on-disk cache of the CA public keys fetched from the
// server, for clients that have not pinned capubkey.
//
// It is a cache and not a trust decision. The client uses these keys only
// to recognize its own certificates, so a wrong entry costs a needless
// certificate request rather than misplaced trust, and nothing here is ever
// reported to the server as a pinned CA. An operator who wants a trust
// anchor sets capubkey, which this never touches.
type caCache struct {
	// Keys are authorized_keys lines, one per active signer key.
	Keys []string `json:"keys"`
	// FetchedAt is when the server last answered, for the TTL. Written from
	// the client's clock, so a clock that jumps backwards only means an
	// extra fetch.
	FetchedAt time.Time `json:"fetched_at"`
}

// fresh reports whether the cache can be used without asking the server.
// A cache from the future is not fresh: that is a clock that moved, and
// re-fetching is the cheap way to be sure.
func (c caCache) fresh(now time.Time) bool {
	if len(c.Keys) == 0 {
		return false
	}
	age := now.Sub(c.FetchedAt)
	return age >= 0 && age < caCacheTTL
}

// loadCACache reads the cache at path.
//
// Every failure is reported as "nothing cached" rather than as an error: a
// missing file is the normal first run, and a truncated or hand-edited one
// is not worth failing a login over when the fix is to fetch again. Only
// the fetch that follows can fail the command.
func loadCACache(path string) caCache {
	if path == "" {
		return caCache{}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("could not read the CA key cache", "path", path, "error", err)
		}
		return caCache{}
	}

	var c caCache
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Debug("could not parse the CA key cache", "path", path, "error", err)
		return caCache{}
	}
	return c
}

// saveCACache writes keys to path, creating the directory if needed.
//
// Best-effort by design: a client that cannot write its cache should still
// log in, just without the benefit. Written atomically because Match exec
// runs once per SSH connection and people open several at once, so two
// refreshes can race; a rename makes that last-writer-wins instead of
// leaving a half-written file behind.
func saveCACache(path string, keys []string) {
	if path == "" || len(keys) == 0 {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Debug("could not create the CA key cache directory", "path", path, "error", err)
		return
	}

	data, err := json.Marshal(caCache{Keys: keys, FetchedAt: time.Now()})
	if err != nil {
		// not covered: caCache holds a string slice and a time, neither of
		// which json.Marshal can fail on.
		slog.Debug("could not encode the CA key cache", "error", err)
		return
	}

	// 0600 despite holding only public keys: whoever can rewrite this file
	// chooses which certificates this client recognizes, which is a
	// denial of service against its owner and nobody else's business.
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		slog.Debug("could not write the CA key cache", "path", path, "error", err)
	}
}

// resolveCAKeys returns the CA public keys to trust when none is pinned,
// preferring a fresh cache and falling back to one that is merely stale
// when the server cannot be reached.
//
// The order matters more than it looks. The certificate-reuse fast path in
// runLogin never runs if this fails, so a client holding a perfectly good
// certificate used to fail whenever the server was down, for a key it
// already had. Cached keys are returned even when a refresh fails, for
// exactly that reason.
func resolveCAKeys(ctx context.Context, client api.Client, path string) ([]string, error) {
	cached := loadCACache(path)
	if cached.fresh(time.Now()) {
		slog.Debug("using the cached CA public key", "path", path, "keys", len(cached.Keys))
		return cached.Keys, nil
	}

	fetchCtx := ctx
	if len(cached.Keys) > 0 {
		// Something usable is already in hand, so this refresh is an
		// optimization and must not hold up an SSH connection.
		var cancel context.CancelFunc
		fetchCtx, cancel = context.WithTimeout(ctx, caRefreshTimeout)
		defer cancel()
	}

	fetched, err := client.GetCA(fetchCtx)
	if err != nil {
		if len(cached.Keys) > 0 {
			slog.Debug("keeping the cached CA public key after a failed refresh",
				"path", path, "error", err)
			return cached.Keys, nil
		}
		return nil, err
	}

	keys := config.SplitKeyList(fetched)
	if len(keys) == 0 {
		if len(cached.Keys) > 0 {
			slog.Debug("keeping the cached CA public key: the server returned none", "path", path)
			return cached.Keys, nil
		}
		return nil, fmt.Errorf("the server returned no CA public key")
	}

	warnOnCAReplacement(cached.Keys, keys)
	saveCACache(path, keys)
	return keys, nil
}

// warnOnCAReplacement reports a refresh that replaced the CA key set
// outright, sharing nothing with what was cached.
//
// Only the disjoint case is worth saying anything about. A set that gained
// a key is a rotation starting, and one that lost a key is the same
// rotation finishing; both are routine and silent. Nothing overlapping is
// either a server that was rebuilt around a new CA, or a rotation whose
// whole overlap window passed while this client was switched off.
//
// It warns rather than refuses. The client cannot tell those apart, and
// refusing would strand exactly the machines that were off the longest,
// turning an ordinary rotation into an estate-wide outage. An operator who
// wants the change refused pins capubkey.
func warnOnCAReplacement(cached, fetched []string) {
	if len(cached) == 0 {
		return
	}

	cachedPrints := caFingerprints(cached)
	fetchedPrints := caFingerprints(fetched)
	if len(cachedPrints) == 0 || len(fetchedPrints) == 0 {
		return
	}

	for fingerprint := range fetchedPrints {
		if _, ok := cachedPrints[fingerprint]; ok {
			return
		}
	}

	slog.Warn("the server's CA key set was replaced entirely; adopting it",
		"cached", mapKeys(cachedPrints), "server", mapKeys(fetchedPrints),
		"note", "pin capubkey to refuse a change instead of adopting it")
}

// caFingerprints is the SHA256 fingerprint of each key that parses, as a
// set. Keys that do not parse are skipped: this drives a log line, and a
// malformed entry should not turn into a spurious "replaced entirely".
func caFingerprints(keys []string) map[string]struct{} {
	prints := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			continue
		}
		prints[ssh.FingerprintSHA256(parsed)] = struct{}{}
	}
	return prints
}

// mapKeys is the sorted key list of a fingerprint set, so a log line reads
// the same way twice.
func mapKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
