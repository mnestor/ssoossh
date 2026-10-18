# Part 3: NSS daemon `ssoosshd-nss`

**Repo:** `mnestor/ssoossh`. **Depends on:** Part 1 wire JSON. **Implements:**
systemd's `io.systemd.UserDatabase` varlink interface; stock `nss-systemd`
is the glibc side (README U1). Shared context in [README.md](README.md).

## Scope

- New Linux-only binary `cmd/ssoosshd-nss`.
- Daemon code in a new top-level `/nssd` (README R18), importing
  only `internal/`, never `server/` or `client/`.
- A userdb service on `/run/systemd/userdb/com.github.mnestor.ssoossh`,
  socket-activated by systemd. No NSS module of our own.
- New shared package `internal/varlink`: minimal varlink codec (client and
  server halves), used by the daemon and by `ssoossh host principals`.
- `internal/api` additions: TLS pinning, SRV discovery, fleet key headers,
  NSS lookup methods (none exist today; README R3).
- Packaging: one `ssoosshd-nss` `.deb`/`.rpm` via this repo's nfpm holding
  the daemon and its `.socket` and `.service` units. No `.apk`: musl
  cannot load NSS modules and has no systemd (README U2).
- SELinux policy for the daemon and its socket, shipped as
  `ssoossh-nss-selinux` (`.rpm`) from this repo (README R19). Never linked
  into the cross-platform `ssoossh` client.

## Platform floor

systemd 245 or later, glibc, and `systemd` on the `passwd:`, `group:`
**and `shadow:`** lines of `/etc/nsswitch.conf`. Measured on 2026-10-17
(README "Open check results"): `passwd:` and `group:` carry it by default
on EL9, EL10, Fedora and Arch, and on Debian and Ubuntu once
`libnss-systemd` is installed; `shadow:` carries it only on EL10, Fedora
and Arch; openSUSE Leap 15.6 ships `compat` on all three and needs an
edit. Without `systemd` on `shadow:`, sshd with `UsePAM yes` refuses
every directory user and `UsePAM no` admits a locked one (Alma 9). The
daemon reads `nsswitch.conf` at start and logs an error, and reports in
`status`, when any of the three lines lacks the `systemd` token, so a
missing entry is not mistaken for missing users.

## Layout

| Path | Purpose |
| --- | --- |
| `cmd/ssoosshd-nss/main.go` | entrypoint only; `//go:build linux` |
| `nssd/config` | `/etc/ssoossh/nss.yaml` load + validate (viper), override file |
| `nssd/upstream` | wraps `internal/api` NSS calls, offline state, 401 latch, concurrency cap |
| `nssd/cache` | memory and SQLite stores behind one interface, TTL logic, singleflight |
| `nssd/policy` | sanity rules, host overrides, shell resolution, filters, local-name check |
| `nssd/userdb` | `io.systemd.UserDatabase` and `RefreshUser` handlers: parameter validation, record mapping, error mapping |
| `nssd/server` | socket activation (`LISTEN_FDS`) or self-bind for tests, accept loop, deadlines, peer credential checks, admin socket |
| `nssd/cmd` | cobra commands: `run`, `cache`, `override`, `status`, `lookup` |
| `internal/varlink` | NUL-framed JSON codec, call/reply types, fuzz tests; no Linux-only code so the client compiles everywhere |

Struct-based DI per `.claude/rules/go.md`: one `Daemon` struct holds config,
store, upstream client, clock and logger; handlers are methods on it.
Interfaces (`Store`, `Upstream`, `PeerInspector`, `Clock`) are defined in the
implementing package.

## Userdb service

**Socket.** `AF_UNIX` `SOCK_STREAM` at
`/run/systemd/userdb/com.github.mnestor.ssoossh`, mode 0666, created and
held by `ssoosshd-nss.socket`. The service name, which callers must send in
every call, is `com.github.mnestor.ssoossh`. When `LISTEN_FDS` is unset
(development, tests) the daemon binds a path given by `--listen` itself.

**Wire.** Varlink: each message is one JSON object followed by a NUL byte.
A call is `{"method", "parameters", "more"?, "oneway"?}`; a reply is
`{"parameters"}` or `{"error", "parameters"}`. With `more`, every reply but
the last carries `"continues": true`. Unknown top-level fields are ignored.
A connection may carry several calls in sequence; the daemon closes it at
EOF, after 1 s idle, or on any framing error. A message over 64 KiB closes
the connection.

**Methods** (systemd `docs/USER_GROUP_API.md`):

| Method | Primary keys | Behaviour |
| --- | --- | --- |
| `io.systemd.UserDatabase.GetUserRecord` | `uid`, `userName` | cache or upstream user lookup. Both set and naming different users: `ConflictingRecordFound`. Neither: `EnumerationNotSupported` |
| `io.systemd.UserDatabase.GetGroupRecord` | `gid`, `groupName` | group cache, filled from user lookups (gid and name) and group lookups (members). Neither key: `EnumerationNotSupported` |
| `io.systemd.UserDatabase.GetMemberships` | `userName`, `groupName` | `userName`: one reply per group in the user's `groups`. `groupName`: one reply per member from a group lookup. Both: one reply if the pair is a membership. Neither: `EnumerationNotSupported`. No match: `NoRecordFound` |
| `com.github.mnestor.ssoossh.NSS.RefreshUser` | `userName` | see [`RefreshUser`](#refreshuser); accepts `oneway` |
| `org.varlink.service.GetInfo` | none | vendor, product, version, interface list, so `varlinkctl info` works. Introspection is not implemented (systemd's own userdb services omit it too) |

`service` missing or not `com.github.mnestor.ssoossh`: `BadService`.
Secondary filters on a found record: `dispositionMask`, `uidMin`/`uidMax`,
`gidMin`/`gidMax` are applied and a mismatch is `NonMatchingRecordFound`;
`fuzzyNames` and `uuid` return `org.varlink.service.InvalidParameter`.
`nss-systemd` sends none of them on keyed lookups (`userdb-glue.c` passes
no match filter); `userdbctl` may.

**Errors**

| Daemon situation | Reply | What a glibc caller sees |
| --- | --- | --- |
| found | record | success |
| unknown upstream, filtered, failed a sanity rule, local name or id (U8), invalid name | `NoRecordFound` | not found |
| upstream still running at the answer deadline, offline or 401-latched with no cache entry, over the concurrency cap | `ServiceNotAvailable` | not found (README U3) |
| wrong `service` | `BadService` | n/a |

Send `ServiceNotAvailable` even though glibc callers cannot tell it apart:
it is correct per the API, `userdbctl` shows it, and a later systemd may
use it.

**Timing** (README U4, U5). The caller's bound is systemd's 45 s, so:

- the daemon reads a call within 1 s or closes;
- it answers every call within `socket_answer_deadline` (2.5 s); an upstream
  fetch still running then completes into the cache, and the call gets
  `ServiceNotAvailable`;
- the accept loop never waits on upstream; over the concurrency cap it
  answers `ServiceNotAvailable` immediately rather than leaving a
  connection unaccepted;
- start-up opens the cache and starts accepting before any upstream call,
  because socket activation queues lookups until then.

**Connection limits** *(added 2026-10-18 after the threat review)*. The
socket is world-connectable and the accept loop spawned a goroutine per
connection with no bound, so a local user could hold connections open
(sending a call inside each 1 s read timeout) until descriptors, threads or
memory ran out, and a dead daemon stalls every lookup on the host. Now:

- at most `socket_max_connections` (1024) connections are served at once,
  a quarter of them kept for uid 0 peers. sshd, logind, setuid `sudo` and
  `su`, and `systemd-userwork` all connect as uid 0, so a flood from one
  unprivileged uid cannot stop logins or `sudo`. Peer credentials are read
  in the accept loop (`SO_PEERCRED` does not block) to decide;
- a connection over the cap has its call read (within 250 ms, so closing
  does not reset the connection before the caller reads the answer),
  answered `ServiceNotAvailable` like a lookup over the upstream cap, and
  closed. At most 64 refusals run at once; past that a connection is
  closed unanswered, which the caller sees as the same not-found. Refusals
  are logged once a minute with a count;
- `socket_idle_timeout` (10 s) is the total a connection may spend waiting
  for calls over its life. Calls never reset it and a call in progress (an
  upstream wait, a `GetMemberships` stream, a blocked write) never counts
  against it, so no call is cut;
- `EMFILE`, `ENFILE`, `ENOBUFS` and `ENOMEM` from accept are waited out
  (5 ms doubling to 1 s), not returned, since returning stopped the daemon;
- the service unit sets `LimitNOFILE=8192`, `MemoryMax=512M` with
  `GOMEMLIMIT=400MiB`, and `TasksMax=4096`, above what 1024 connections
  can use (measured: 10 KiB an idle connection, 78 KiB one holding a
  64 KiB call that never ends).

The cap's size comes from systemd's client behaviour (v257 sources).
nss-systemd passes `USERDB_EXCLUDE_NSS` (`userdb-glue.c`), so it skips the
multiplexer and opens one connection per service for each lookup, closing
it after the answer. `initgroups` holds the `GetMemberships` stream open
while it resolves each group with `groupdb_by_name` and NSS allowed, which
goes through `io.systemd.Multiplexer` when systemd-userdbd runs, whose
`systemd-userwork` workers (at most `USERDB_WORKERS_MAX`, 4096) each serve
one client at a time: so two connections per thread in a lookup at most.
Steady load is therefore a few connections; the large case is the
listen backlog after a restart (`Backlog=` is capped by
`net.core.somaxconn`, typically 4096), and a lookup refused then is a
not-found once, as an upstream timeout is.

## Record mapping

**User record**, built from the Part 1 user JSON after policy:

| Field | Value |
| --- | --- |
| `userName` | `name` |
| `uid` | `uid` |
| `gid` | `gid`, after `override_gid` |
| `realName` | `gecos` |
| `homeDirectory` | resolved home (policy) |
| `shell` | resolved shell; `disabled_shell` for `disabled`/`deleted` |
| `disposition` | `regular` |
| `locked` | `true` for `disabled`/`deleted`, otherwise omitted |
| `service` | `com.github.mnestor.ssoossh` |

No `memberOf` (the API makes `GetMemberships` authoritative). The
`privileged` section is `{"hashedPassword": ["*"]}` and is sent only when
the peer uid from `SO_PEERCRED` is 0, as the userdb API requires for
privileged data; every other peer gets the record without it and with
`incomplete: true` (README R27, U7). `realName` is passed through as the
server rendered it; the daemon re-runs the same sanitiser (no `:`, no
control characters, 256-byte cap) as defence in depth, since `nss-systemd`
would otherwise mangle the field.

**Group record:** `groupName`, `gid`, `disposition: regular`, `service`.
No `members`; `nss-systemd` fetches them through `GetMemberships`.

**What callers get**, from `src/nss-systemd/userdb-glue.c` and
`nss-systemd.c` on systemd `main`:

- `pw_passwd` is always `x`; `pw_gecos` is `realName`.
- Where `shadow:` lists `systemd`: `sp_pwdp` is the first
  `privileged.hashedPassword` entry, so `*` for a uid 0 caller and `!*`
  when the section is absent; `sp_expire` is 1 when `locked`; `sp_lstchg`
  is -1 because the record carries no password-change time, so `pam_unix`
  never demands a change (`userdb-glue.c:160-178`; U6, U7, R27).
- `initgroups` calls `GetMemberships(userName)` on every service, then
  `GetGroupRecord(groupName)` per group, skipping the primary gid. One
  login is 1 + N calls, all served from cache. Those group lookups may
  arrive through `systemd-userdbd` rather than from the caller, and they
  run with the NSS exclusion cleared (`nss-systemd.c:1025`), so each name
  resolves through `files` first (README U12). The daemon therefore lists
  a membership only under a name that resolves to its own gid: the gid
  rule under "Policy and sanity rules".
- `nss-systemd` never resolves `root` or `nobody` through userdb.

## `internal/api` additions

- `Config.TLSCAFile`, `Config.TLSSPKIPins []string` (base64 SHA-256 of
  SPKI). When either is set: system roots ignored, TLS 1.3 minimum,
  `VerifyConnection` checks the leaf SPKI against pins. `Proxy: nil` on the
  transport (do not use `http.ProxyFromEnvironment`). Existing callers keep
  current behaviour when unset.
- `Config.Headers` or a dedicated `KeyID/KeySecret` pair sent on NSS calls
  only; secret redacted in `traceRequest`/`logHeaders`.
- `DiscoverServer(ctx, domain)`: `net.Resolver.LookupSRV("ssoossh", "tcp",
  domain)`, RFC 2782 priority/weight order. Pin unchanged: a spoofed SRV
  points at a server that fails the pin.
- `GetNSSUser(ctx, byName|byUID)`, `GetNSSGroup(ctx, byName|byGID)`
  returning typed structs in `internal/apitypes`; distinguish 404, 401, 429
  (honour `Retry-After`), other.

## Configuration `/etc/ssoossh/nss.yaml`

Root 0600; environment not consulted. Option names follow `sssd.conf(5)`.

```yaml
server: ""                        # https URL; empty -> SRV via dns_discovery_domain
dns_discovery_domain: ""          # empty -> host's domain
tls_ca_file: ""                   # one of these two is required
tls_spki_pin: []
api_key_file: /etc/ssoossh/nss.key  # 0600, line 1 key_id, line 2 key_secret
timeout: 5s
reconnection_retries: 3
offline_timeout: 60s              # backoff base while offline
socket_answer_deadline: 2.5s

cache_mode: persistent            # persistent | memory
entry_cache_timeout: 5400s
entry_cache_user_timeout: 0       # 0 inherits
entry_cache_group_timeout: 0
entry_negative_timeout: 15s
entry_cache_nowait_percentage: 50
refresh_expired_interval: 0       # 0 -> 75% of TTL when persistent, off in memory
refresh_rate: 2                   # upstream refreshes/s, background
cache_expire_offline: 0           # 0 = never

login_soft_ttl: 60s
login_refresh_timeout: 1s
sshd_units: [sshd.service, ssh.service, sshd@.service, ssh@.service]  # "@.service" matches any instance

min_id: 1000
max_id: 0                         # 0 = no max
override_gid: 0
override_homedir: ""
fallback_homedir: /home/%u        # %u user, %U uid, %d domain, %h original
override_shell: ""
shell_fallback: /bin/sh
allowed_shells: []                # empty -> /etc/shells
vetoed_shells: []
disabled_shell: /usr/sbin/nologin
ignore_group_members: false
filter_users: [root]
filter_groups: [root]
require_groups: []
```

`auto_private_groups` is server-only (README R12).

## Request path

1. Accept on the activation socket; read `SO_PEERCRED`.
2. Read and decode one call; wrong `service` is `BadService`, malformed
   input closes the connection.
3. Names are matched byte for byte (README R23). `nss-systemd` has
   already applied `valid_user_group_name(VALID_USER_RELAX)` before
   calling; the daemon re-applies the same rules plus the 32-byte cap and
   answers `NoRecordFound` for a failure without going upstream, so a
   direct `varlinkctl` caller cannot push an invalid name to the server.
4. **Local short-circuit** (README U8): `filter_users`/`filter_groups`,
   names present in `/etc/passwd`/`/etc/group` (re-read on mtime change),
   their uids and gids, and ids outside `min_id..max_id` are
   `NoRecordFound`, never sent upstream.
5. Cache lookup:
   - fresh (age < `nowait_percentage` of TTL): answer;
   - stale-but-valid: answer, schedule background refresh;
   - expired or miss: fetch upstream through singleflight, bounded by
     `socket_answer_deadline`;
   - negative cache hit within `entry_negative_timeout`: `NoRecordFound`.
6. **sshd freshness**: for `GetUserRecord` when the peer is sshd (below)
   and entry age > `login_soft_ttl`, refresh synchronously within
   `login_refresh_timeout`; on timeout answer from cache. The user JSON
   carries groups, so the later `initgroups` sees the refreshed list.
7. Apply policy (next section) to the upstream answer **before** caching;
   host overrides apply on read so editing overrides needs no cache flush.
8. Map to a record (above), reply, and wait for the next call or close.

**Peer is sshd** (README R5): peer uid is 0 **and** the last component of
its systemd cgroup (`/proc/<pid>/cgroup`, the v2 line or, on hybrid hosts,
`name=systemd`) is in `sshd_units`. `comm` alone is not trusted. Not the
executable path: with an empty capability bounding set,
`readlink(/proc/<pid>/exe)` of sshd fails with `EACCES` on every call,
because the kernel allows it only to a reader holding every capability
the target holds, or `CAP_SYS_PTRACE` (measured on EL9 and EL10). The
cgroup file needs no capability, and only root can move a process into
`sshd.service`. A failed read is "not sshd", quietly. This works because
`nss-systemd` connects directly from the calling process for user and
membership lookups (README U1). `SYSTEMD_NSS_SERVER_SIDE_NSS=1` has no
effect on systemd 252 or 257 (U13).

**`RefreshUser`**: replies at once (or not at all when `oneway`) and
refreshes in the background. Debounce per name: at most one refresh per
name per `login_soft_ttl`, and all `RefreshUser` refreshes share the
`refresh_rate` limiter (README R6). Sent by `ssoossh host principals`
(below).

**Upstream.** Concurrency cap 8; beyond it, `ServiceNotAvailable`. Connect
timeout 2 s, total `timeout`. `User-Agent: ssoosshd-nss/<version>`. On 401:
latch `unauthorized`, stop fetching, log at error once per minute, serve
cache as if offline until `cache_expire_offline`; latch clears on SIGHUP or
key file change. Offline: exponential backoff from `offline_timeout`, serve
cache.

*(added 2026-10-18 after a field failure)* A middlebox on the path silently
dropped the host-to-server half of an idle kept-alive HTTP/2 connection;
the next lookup was written into it and spent the whole `timeout` there,
and the retries reused the same pooled connection, so the host went
offline for minutes. Now the ordinary-call transport pings a connection
that has read nothing for 15 s and closes it when no answer comes within
5 s (`http.HTTP2Config.SendPingTimeout`, `PingTimeout`; not on the events
stream). Each attempt but the last is bounded by
`min(left/2, max(socket_answer_deadline - 1s, socket_answer_deadline/2))`,
1.5 s by default, so a retry still answers inside the answer deadline, and
an attempt that got no HTTP answer retires its client so the retry dials a
fresh connection (closing idle connections alone does not drop an HTTP/2
connection whose cancelled stream is still being torn down). A timeout now
says whether it was on a reused connection or before one was made.

## Policy and sanity rules

Applied to every upstream answer before caching; a failing entry is logged
with the reason and treated as `NoRecordFound` (and negative-cached).

- uid and gid within `min_id..max_id`.
  Operators bracket their directory's real allocation with both, since
  the default `max_id: 0` leaves systemd's reserved ranges (homed,
  `DynamicUser=`, container uids) servable: see `hosts/nss.md`, "Choosing
  min_id and max_id" (added 2026-10-18 after the threat review).
- Name absent from `/etc/passwd` / `/etc/group`.
- `require_groups`: user must be a member of one, else `NoRecordFound`,
  checked against the memberships the gid rule keeps, by directory name.
- **Gid rule for memberships** (decided 2026-10-18 by the owner; README
  U12): match on gid, never on name. For each membership in a user
  answer, first match wins:
  1. gid outside `min_id..max_id`: dropped, as the group answer is;
     logged once per name and gid per daemon run.
  2. gid of a local group: the local group's name is listed (it resolves
     to the same gid through `files`), unless `filter_groups` lists it; a
     debug line when the names differ.
  3. name of a local group with another gid: a name collision. Dropped,
     since userdb cannot carry a gid without a name that resolves to it,
     so the member does not get the directory gid on this host. Warned
     about once per name, directory gid and local gid per daemon run.
  4. invalid name or in `filter_groups`: dropped.
  5. otherwise the directory name.
- Group answers (and membership stubs): a name collision is still not
  served by name or gid, and is recorded and warned about under the same
  once-per-run key, as is a gid outside the range. Only server answers and
  memberships are checked, never the local-name short-circuit, so the log
  cannot flood.
- Home: `override_homedir` -> server `home` -> `fallback_homedir`;
  must be absolute after expansion.
- Shell, first acceptable wins:
  `override_shell` -> server `shell` (or a host override's) ->
  `shell_fallback`. Acceptable = in `allowed_shells` (or `/etc/shells` when
  empty) and not in `vetoed_shells`. The user's own shell choice and the
  `shell_map` that resolved it are gone (removed 2026-10-18 after the threat review: the shell comes from the directory and host policy only); a host config that still
  sets `shell_map` fails to load, because unknown keys are refused.
- `status` `disabled`/`deleted`: shell = `disabled_shell`, **exempt from the
  shells check** (README R4); `locked: true` (README U6); log warning when
  served to sshd.
- `override_gid` replaces the primary gid.
- Group responses: drop members when `ignore_group_members`; log when the
  server set `members_truncated`.
- Host overrides from `/etc/ssoossh/nss-overrides.yaml` (same shape as
  `principals.yaml`): per-user uid/gid/home/shell, applied after the
  server's answer and before the sanity rules; cannot set a uid below
  `min_id`.

## Cache

`Store` interface with `memory` and `sqlite` implementations. SQLite at
`/var/lib/ssoossh/db/cache.db`, WAL, 0600 in 0700; use the SQLite driver
already in `go.mod` (verify; prefer the pure-Go one so `CGO_ENABLED=0`
builds work). Tables: users by name and uid, groups by name and gid, user to
gids; each row stores the fetched JSON, fetched_at, expires_at, and a stale
flag. Trust model is sssd's: entries are trusted on load; file permissions
are the boundary. A host rebooted while the server is down resolves known
users from disk.

A refresh that changes the uid or gid of a cached name is applied, since
the server only does that after an admin accepted it (README R38), but is
logged at warning with the name and both numbers so the change is visible
in the journal of every host it reached.

Background sweep at `refresh_expired_interval`, jittered, bounded by
`refresh_rate`. Singleflight per `(method, key)`; use `golang.org/x/sync`
only if already a dependency, else a small local implementation.

## Admin interface and CLI

A second socket `/run/ssoossh/nss-admin.sock`, 0600 root, carries admin ops
(JSON lines; internal to this repo) so the CLI never writes the SQLite file
concurrently and works in memory mode.

| Command | Behaviour |
| --- | --- |
| `ssoosshd-nss run` | default; the daemon |
| `ssoosshd-nss cache -E \| -u NAME \| -g NAME \| -U \| -G` | `sss_cache` equivalent: mark stale, never delete |
| `ssoosshd-nss override list \| set USER --uid --gid --home --shell \| unset USER` | edits `nss-overrides.yaml` atomically, signals reload |
| `ssoosshd-nss status` | online/offline/unauthorized, server, key id, cache counts, last error, group name collisions and groups refused by the id range since the start or the last SIGHUP (twenty of each, then a count), `nsswitch.conf` check, and a warning when nscd or unscd is running (README R26) |
| `ssoosshd-nss lookup user NAME \| group NAME \| memberships NAME` | calls the userdb socket exactly as `nss-systemd` does; for debugging |

`userdbctl user NAME` and `getent passwd NAME` exercise the full path
through systemd.

## Client change: `ssoossh host principals`

Before printing principals, send `RefreshUser` for the user as a one-way
varlink call via `internal/varlink` to
`/run/systemd/userdb/com.github.mnestor.ssoossh`, with a 50 ms deadline;
ignore every error, including a missing socket and non-Linux platforms. It
still never touches the network.

## systemd units

`ssoosshd-nss.socket`:

```ini
[Unit]
Before=nss-user-lookup.target
Wants=nss-user-lookup.target

[Socket]
ListenStream=/run/systemd/userdb/com.github.mnestor.ssoossh
SocketMode=0666

[Install]
WantedBy=sockets.target
```

`ssoosshd-nss.service`:

```ini
[Unit]
Requires=ssoosshd-nss.socket
After=ssoosshd-nss.socket

[Service]
ExecStart=/usr/sbin/ssoosshd-nss run
Restart=always
Environment=SYSTEMD_BYPASS_USERDB=com.github.mnestor.ssoossh
RuntimeDirectory=ssoossh
RuntimeDirectoryMode=0700
StateDirectory=ssoossh
StateDirectoryMode=0700
ProtectSystem=strict
ReadOnlyPaths=/etc/ssoossh
PrivateTmp=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectHome=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
```

systemd holds the userdb socket across service restarts, so lookups queue
instead of failing (README check 2), and the daemon needs no write access
under `/run/systemd`. `RuntimeDirectory` now holds only the admin socket.
`StateDirectory` is required for the persistent cache (README R7).
`SYSTEMD_BYPASS_USERDB` keeps the daemon from ever querying itself (README
U9); with `CGO_ENABLED=0` it never calls NSS anyway. The bounding set stays
empty: the sshd check reads `/proc/<pid>/cgroup`, which needs no
capability (see "Peer is sshd").

## Packaging

**nfpm `ssoosshd-nss`:**

| Content | Destination |
| --- | --- |
| daemon binary | `/usr/sbin/ssoosshd-nss` |
| units | `/usr/lib/systemd/system/ssoosshd-nss.{socket,service}`, not enabled |
| example config | `/etc/ssoossh/nss.yaml`, 0600, `config\|noreplace` |
| `ssoosshd-nss(8)` | man page |

Dependencies: deb `systemd (>= 245)` and `libnss-systemd` (its postinst
adds `systemd` to `nsswitch.conf`); rpm `systemd >= 245` plus whichever
package ships `libnss_systemd.so.2` (README check 5).

postinstall: `systemctl daemon-reload`; never edits `nsswitch.conf`, never
enables units. `/etc/ssoossh/nss.key` is never shipped.
`Recommends: ssoossh-nss-selinux` on rpm.

**`ssoossh-nss-selinux` (rpm):** policy source in
`packaging/selinux/ssoossh_nss.{te,fc,if}` (README R19):

- `ssoosshd_nss_t` domain for `/usr/sbin/ssoosshd-nss`
  (`ssoosshd_nss_exec_t`); config `ssoosshd_nss_conf_t`
  (`/etc/ssoossh/nss.yaml`, `nss.key`, `nss-overrides.yaml`); state
  `ssoosshd_nss_var_lib_t` (`/var/lib/ssoossh(/.*)?`); runtime
  `ssoosshd_nss_var_run_t` (`/run/ssoossh(/.*)?`); outbound TCP 443 and
  DNS; read `/etc/passwd`, `/etc/group`, `/etc/shells`,
  `/etc/nsswitch.conf`; read `/proc/<pid>/exe` of `sshd_t`.
- `nsswitch_domain` may `connectto` the userdb socket. Which domain that
  rule names depends on how systemd labels the activation socket (README
  check 3).

Built in a Fedora container job (`selinux-policy-devel`, `make -f
/usr/share/selinux/devel/Makefile`) because the goreleaser host lacks the
toolchain; the `.pp` is an input to an nfpm package with `post`
`semodule -i` and `preun` `semodule -r`. Use `ssoossh-pam/selinux/` for
house style.

**Integration test** (CI, every PR touching `nssd/` or `internal/varlink`):
container with `libnss-systemd` installed and `nsswitch.conf` set to
`files systemd` on `passwd:`, `group:` and `shadow:`; the daemon self-binds
the userdb socket (no systemd as PID 1 needed) and points at an
httptest-style fake server with a test CA. Assert `getent passwd`,
`getent group`, `id`, `userdbctl user`, a disabled user's shell and
`sp_expire`, and quick not-found with the daemon stopped. A second job with
systemd as PID 1 covers socket activation across a restart.

- Release builds `linux/amd64`, `linux/arm64`; `CGO_ENABLED=0`; the same
  `GOFIPS140` setting as the server build in `.goreleaser.yml` (README
  R32).
- No change to the `ssoossh` client package beyond the `RefreshUser` call.

## Tests

- `internal/varlink`: round-trip table tests for calls, replies, errors and
  `continues` streams; `FuzzDecode` (missing NUL, oversize, invalid JSON,
  truncated stream).
- `internal/api`: httptest TLS server with correct and wrong SPKI pin, CA
  file, proxy env set and ignored, key headers redacted in trace output,
  SRV ordering with a fake resolver.
- `nssd/userdb`: every method with each primary-key combination,
  `BadService`, `ConflictingRecordFound`, `NonMatchingRecordFound`,
  `InvalidParameter` for `fuzzyNames`/`uuid`, `EnumerationNotSupported`,
  record mapping for active, disabled and deleted users; `privileged`
  present for a uid 0 peer only and `incomplete: true` otherwise;
  `realName` sanitising.
- `nssd/policy`: table tests for each sanity rule, shell precedence, home
  expansion, disabled exemption, local short-circuit, overrides cannot go
  below `min_id`.
- `nssd/cache`: fake clock; fresh, nowait refresh, expired, negative,
  offline serving, `cache_expire_offline`, persistence across reopen.
- `nssd/server`: real Unix socket in `t.TempDir()`; peer inspector
  interface faked for the sshd path; `ServiceNotAvailable` on slow upstream
  within the deadline; concurrency cap answers immediately; `RefreshUser`
  debounce; several calls on one connection; idle close.
- `upstream`: 401 latch and clear, 429 `Retry-After`, singleflight.
- Code needing a live `SO_PEERCRED`, `/proc` or `LISTEN_FDS` carries a
  `not covered:` comment only where a fake cannot reach it.

## User docs

Written with the implementation, in the same PRs, not before. Until then
the proposal is the only description.

| Page (`user-docs/src/content/docs/`) | Content |
| --- | --- |
| `project/roadmap.md` (line 69 today) | Still says "four parts ... an NSS daemon and an NSS module". Change to three parts and the userdb design when the first daemon PR lands; remove the entry when NSS ships |
| `hosts/nss.md` (new) | Platform floor (systemd 245+, glibc, `systemd` on `passwd:`/`group:`; RHEL 8 and musl unsupported), install, enabling `ssoosshd-nss.socket`, `nss.yaml` reference, fleet key file, SELinux package; `pam_mkhomedir` or `oddjob-mkhomedir` in sshd's PAM session stack (pam_ssoossh is `auth` only and an ephemeral host has no home directories); nscd or unscd must not cache passwd or group, or they cache a U5 miss (README R26) |
| `hosts/nss.md`, behaviour | A directory outage shows as "user not found" (README U3); a first login while the server is slow can fail once (U5); disabled users are `locked` with `nologin` (U6); the `UsePAM no` outcome of open check 1 (U7); group changes reach a host on the next sshd login or at cache expiry |
| `hosts/nss.md`, troubleshooting | `ssoosshd-nss status`, `ssoosshd-nss lookup`, `userdbctl user NAME`, `getent passwd NAME` |
| `concepts/security-model.md` | Fleet key exposure model; never write deny rules (sudoers `!%group`) against directory groups (README threat model item 6, U9) |
| `operations/` (new NSS page) | Server side: `nss.*` config, fleet keys, rotation report, roles. `reference/config` regenerates from `make confdocs` |
| `packages.md` | The `ssoosshd-nss` and `ssoossh-nss-selinux` packages and their dependencies |
| `internals/architecture.md` | The daemon, the userdb socket, and why there is no NSS module of our own |
| `project/decisions.md` | Declined on purpose: an NSS module of our own, HTTP inside an NSS module, the sssd proxy provider |

`SSOOSSH.md` lists "NSS directory (4 parts)" under "Not built yet". That
list changes only with the owner's explicit say-so: ask at the first daemon
PR, and move NSS into the shipped sections when it ships.

## Done when

`go test ./...` and `golangci-lint run` pass on Linux; `GOOS=darwin go
build ./client/...` still passes (client imports `internal/varlink` only);
`getent passwd <user>` resolves through `nss-systemd` against a Part 1
server in the integration container; README open checks 1 to 6 have
recorded results; the [user docs](#user-docs) pages are merged.
