# Part 4: `mnestor/ssoossh-nss` repository (agent handoff)

**Audience:** an agent starting a new repository with no access to
`mnestor/ssoossh`. Everything needed is in this document. Where it says
"read `ssoossh-pam`", that repository (`github.com/mnestor/ssoossh-pam`) is
public and is the model to copy for layout, build, tests and CI.

**This repo ships source, not packages.** `mnestor/ssoossh` builds the module
from a pinned release tarball of this repo with its own toolchain (`zig cc`,
glibc 2.28 floor) and puts it in its `ssoosshd-nss` package next to the
daemon. SELinux policy also lives there. Your release artefact is a tagged
source tarball whose Makefile builds cleanly with that compiler.

## 1. Context

ssoossh is an SSO-to-SSH-certificate system. Its server, `ssoosshd`, is
becoming a POSIX directory: it serves users' uid, gid, groups, home and
shell. On each host a root daemon, `ssoosshd-nss` (Go, built and packaged
from `mnestor/ssoossh`, **not your job**), fetches those over pinned HTTPS,
caches them, applies host policy, and answers on a Unix socket.

Your job is the piece glibc loads: `libnss_ssoossh.so.2`, a tiny C NSS module
that forwards `getpwnam`/`getpwuid`/`getgrnam`/`getgrgid`/`initgroups` to that
socket. It is the same split as sssd (`libnss_sss.so` + `sssd`).

The module is loaded into **every** process that resolves a user, including
setuid binaries (`sudo`, `su`, `passwd`, `sshd`). That is the whole threat
model for this repo: anything in the `.so` is attack surface in those
processes. Hence: no TLS, no JSON, no environment reads, no network, no
config file, no logging, libc only.

## 2. Deliverables

| Item | Notes |
| --- | --- |
| `libnss_ssoossh.so.2` | C11, glibc Linux only (musl has no NSS module loading; do not build for Alpine) |
| Tagged source release | `vX.Y.Z` tag plus a GitHub release with `ssoossh-nss-X.Y.Z.tar.gz` and its SHA-256; tarball contents are reproducible (`git archive`) |
| Fuzz targets, ASan/UBSan test build | libFuzzer |
| Test harness with a mock daemon | no dependency on the real daemon |
| `docs/` | `nsswitch.conf` setup, man page `nss_ssoossh(8)`, troubleshooting |
| CI | GitHub Actions: build, tests, sanitizers, fuzz smoke, zig cross-build check, release on tag |

## 3. Repository layout (mirror `ssoossh-pam`)

```
src/nss_ssoossh.c      entry points
src/proto.c, proto.h   socket I/O and reply parsing (pure, fuzzable)
src/fill.c, fill.h     copy parsed reply into struct passwd/group + buffer
src/exports.map        version script: export only _nss_ssoossh_*
tests/                 unit tests, mock daemon, integration via a test nsswitch
fuzz/                  libFuzzer targets
docs/                  nss_ssoossh.8, README sections
Makefile
COMPAT                 SOCKET_PROTOCOL=1
```

Read `ssoossh-pam` first and match its Makefile targets, compiler flags,
sanitizer and fuzz setup. Do not copy its nfpm packaging or SELinux
directory; neither belongs here. Where that repo pins `SSOOSSHD_COMPAT` to
the server wire contract, this repo records in `COMPAT` the socket protocol
version it implements. Because module and daemon ship in one package, there
is no minimum-daemon-version check.

## 4. Socket protocol v1 (normative; do not change)

Copied verbatim from the daemon's `docs/nss-socket-protocol.md`. If it ever
changes, the version byte changes.

**Transport.** `AF_UNIX` `SOCK_STREAM` at `/run/ssoossh/nss.sock`. One
request per connection; the daemon closes after the reply. All integers
big-endian. Strings are `str := u16 len | len bytes`, no NUL, no terminator,
`len <= 4095`.

**Request**

```
u8  version        = 1
u8  op
u16 key_len        <= 255
key bytes
```

| op | Name | Key |
| --- | --- | --- |
| 1 | `passwd_by_name` | name bytes |
| 2 | `passwd_by_uid` | 4 bytes, u32 uid |
| 3 | `group_by_name` | name bytes |
| 4 | `group_by_gid` | 4 bytes, u32 gid |
| 5 | `initgroups` | user name bytes |
| 6 | `refresh_user` | user name bytes; fire-and-forget (the module never sends this) |

**Reply header (8 bytes)**

```
u8  version        = 1
u8  status         0=found 1=notfound 2=unavail 3=tryagain 4=bad_request
u8  op             echo of request op
u8  flags          bit0 members_truncated (op 3,4); bit1 user_disabled (op 1,2)
u32 body_len       0 unless status=found; <= 65536
```

**Bodies (status=found)**

```
passwd (op 1,2):  u32 uid | u32 gid | str name | str passwd | str gecos
                  | str home | str shell
group  (op 3,4):  u32 gid | str name | str passwd | u32 nmem | str member * nmem
initgroups (5):   u32 ngids | u32 gid * ngids        (all gids, primary included)
```

`body_len` must equal the sum of the parsed fields; any mismatch, oversize,
embedded NUL, unknown status, op echo mismatch, or version other than 1 is a
protocol error.

**Timing.** The daemon answers within 2.5 s (replying `tryagain` if its
upstream is slow). The module's total budget is 3 s.

## 5. Entry points

Exported (glibc NSS ABI, `<nss.h>`, `<pwd.h>`, `<grp.h>`):

```c
enum nss_status _nss_ssoossh_getpwnam_r(const char *name, struct passwd *pwd,
        char *buf, size_t buflen, int *errnop);
enum nss_status _nss_ssoossh_getpwuid_r(uid_t uid, struct passwd *pwd,
        char *buf, size_t buflen, int *errnop);
enum nss_status _nss_ssoossh_getgrnam_r(const char *name, struct group *grp,
        char *buf, size_t buflen, int *errnop);
enum nss_status _nss_ssoossh_getgrgid_r(gid_t gid, struct group *grp,
        char *buf, size_t buflen, int *errnop);
enum nss_status _nss_ssoossh_initgroups_dyn(const char *user, gid_t group,
        long int *start, long int *size, gid_t **groupsp, long int limit,
        int *errnop);
```

Enumeration (`setpwent`/`getpwent`/`endpwent` and group equivalents) is not
exported; glibc treats a missing function as unavailable for that source.
Everything else is `static` with `-fvisibility=hidden` and the version
script.

## 6. Behaviour

**Status mapping**

| Situation | Return | `*errnop` |
| --- | --- | --- |
| `found`, filled | `NSS_STATUS_SUCCESS` | unchanged |
| `notfound` | `NSS_STATUS_NOTFOUND` | `ENOENT` |
| caller buffer too small | `NSS_STATUS_TRYAGAIN` | `ERANGE` (glibc retries with a bigger buffer) |
| `tryagain` from daemon | `NSS_STATUS_TRYAGAIN` | `EAGAIN` |
| socket missing, refused, timeout, short read, any protocol error, `unavail`, `bad_request` | `NSS_STATUS_UNAVAIL` | `ENOENT` |
| name longer than 255 bytes or empty | `NSS_STATUS_NOTFOUND` | `ENOENT` |

`UNAVAIL` rather than `NOTFOUND` on failure is load-bearing: `NOTFOUND`
makes a user vanish as if authoritative; `UNAVAIL` lets
`[UNAVAIL=continue]` work and `sudo` fail closed.

**I/O rules**

- `socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0)`, fresh per call, closed
  on every path.
- One monotonic deadline (`clock_gettime(CLOCK_MONOTONIC)`) of 3 s across
  connect, send and all reads; use non-blocking connect plus `poll`, retry
  on `EINTR` against the remaining budget.
- `send(..., MSG_NOSIGNAL)`; never raise `SIGPIPE`.
- Read exactly the header, validate, then exactly `body_len`.
- No global or static mutable state; reentrant and thread-safe.
- Preserve the caller's `errno` except where the NSS contract sets
  `*errnop`.
- Never call any `getpw*`/`getgr*`/`getaddrinfo`, `syslog`, `printf`,
  `getenv`, `secure_getenv`, or anything that could recurse into NSS.
- No heap allocation, with one exception: `initgroups_dyn` may `realloc`
  `*groupsp` as the glibc contract requires (below).

**Filling `struct passwd` / `struct group`** (ops 1-4)

1. If `body_len > buflen`, return `ERANGE`. Otherwise read the body into the
   tail of the caller's buffer: `buf + buflen - body_len`.
2. Validation pass over the body: parse every field with bounds checks,
   reject NULs, compute `need` = sum of `len + 1` for every string, plus for
   groups alignment padding and `(nmem + 1) * sizeof(char *)`.
3. If `need + body_len > buflen`, return `ERANGE`. This is conservative and
   keeps the copy trivially non-overlapping; glibc doubles the buffer.
4. Copy strings from the body region to the front of the buffer with
   explicit lengths, NUL-terminate; point struct fields only into `buf`.
   For groups, place the aligned `gr_mem` array first, then strings,
   `gr_mem[nmem] = NULL`.
5. `pw_passwd` / `gr_passwd` come from the reply (`*` or `!`); never `x`.
6. Verify the returned name equals the requested name for ops 1 and 3, and
   the uid/gid equals the request for ops 2 and 4; mismatch is `UNAVAIL`.

**`initgroups_dyn`** (op 5)

- Read the gid list from the socket in small stack chunks (e.g. 64 gids);
  do not buffer the whole body.
- Skip any gid equal to `group` (the caller adds the primary itself) and
  any already present in `(*groupsp)[0..*start)`.
- When `*start == *size`: if `limit > 0` and `*size >= limit`, stop and
  return `SUCCESS`; otherwise grow to `min(2 * *size, limit)` (or `2 *
  *size` when `limit <= 0`) with `realloc`; on failure return
  `NSS_STATUS_TRYAGAIN` with `ENOMEM`, leaving the old array intact.
- `notfound` returns `NSS_STATUS_NOTFOUND`.

## 7. Build

- `-std=c11 -O2 -fPIC -shared -fvisibility=hidden
  -Wl,--version-script=src/exports.map -Wl,-z,relro,-z,now
  -Wl,-soname,libnss_ssoossh.so.2 -fstack-protector-strong
  -D_FORTIFY_SOURCE=3 -Wall -Wextra -Werror -Wconversion`.
- Link libc only; `nm -D` must show only the five `_nss_ssoossh_*`
  symbols (CI check).
- **Makefile contract** (the consuming repo depends on it):
  - `make` builds `build/libnss_ssoossh.so.2` and nothing else needing
    network, git, or tools beyond `CC`, `make`, `sh`;
  - honours `CC`, `CFLAGS`, `LDFLAGS`, `DESTDIR`, `PREFIX`, `LIBDIR`
    (appending its required flags rather than replacing the caller's);
  - `make install` copies the `.so` to `$(DESTDIR)$(LIBDIR)`; no
    `ldconfig`, no `nsswitch.conf` edits;
  - builds with gcc, clang, and `zig cc -target x86_64-linux-gnu.2.28` /
    `aarch64-linux-gnu.2.28`. CI runs both zig targets on every PR and
    checks `objdump -T` shows no glibc symbol newer than 2.28.
- **Headers:** confirm zig's bundled glibc headers provide `<nss.h>` for
  the 2.28 target. If they do not, vendor the minimal `enum nss_status`
  definition in `src/nss_compat.h` (stable glibc ABI) rather than relying
  on host headers. `<pwd.h>`/`<grp.h>` come from libc.
- Targets: `linux/amd64`, `linux/arm64`, glibc 2.28+.
- Size target: under 32 KB stripped. Report it in CI.

## 8. SELinux

Not in this repo. The policy (daemon domain plus the rule letting
`nsswitch_domain` connect to the socket) lives in `mnestor/ssoossh` beside
the daemon. The module needs no policy of its own: it runs in its caller's
domain and only connects to `/run/ssoossh/nss.sock`.

## 9. Testing

- **Unit:** table-driven tests for the reply parser: every op, every
  status, boundary lengths (0, 4095, 4096), `body_len` mismatches, NULs,
  bad version, op echo mismatch, `nmem` overflow, 65536 vs 65537.
- **Fill:** buffer exactly `need + body_len`, one byte short (`ERANGE`),
  alignment of `gr_mem` at odd buffer addresses.
- **Mock daemon:** a small C (or Python) server on a temp socket path; the
  socket path is a compile-time constant, so build a test variant with
  `-DSSOOSSH_NSS_SOCKET=\"...\"` (production builds must not accept it at
  runtime). Scenarios: found, notfound, slow (> 3 s, expect `UNAVAIL`
  within ~3 s), close mid-header, close mid-body, garbage, `tryagain`.
- **glibc integration:** in a container, install the test `.so`, point
  `nsswitch.conf` at `files ssoossh`, run `getent passwd`, `getent group`,
  `id`, and `initgroups` via a tiny C program; assert outputs.
- **Sanitizers:** whole test suite under ASan + UBSan.
- **Fuzz:** `fuzz_reply_passwd`, `fuzz_reply_group`, `fuzz_initgroups`
  feeding arbitrary bytes as the daemon's reply; CI runs each 60 s, longer
  on a schedule.
- **Fork safety:** test that resolves in a child after `fork()` from a
  threaded parent.

## 10. Documentation

`nsswitch.conf`:

```
passwd: files ssoossh
group:  files ssoossh
```

Document `[UNAVAIL=continue]` semantics, that `files` must come first, that
there is no `shadow` support (authentication is SSH certificates), and that
all configuration lives in the daemon's `/etc/ssoossh/nss.yaml`. Installing
the package never edits `nsswitch.conf`; the operator does. Troubleshooting points at
`ssoosshd-nss status` and `ssoosshd-nss lookup`.

## 11. Do not

- Add TLS, HTTP, JSON, config parsing, environment reads or logging.
- Add enumeration support.
- Cache anything in the module.
- Make the socket path runtime-configurable in production builds.
- Change the protocol; raise an issue against `mnestor/ssoossh` instead.
- Build for musl, FreeBSD or macOS.
- Add packaging (nfpm, deb, rpm) or SELinux policy.

## 12. Done when

- `make`, `make test`, `make sanitize`, `make fuzz-smoke` pass in CI.
- `nm -D` shows exactly five exports; stripped size reported.
- The glibc integration test passes against the mock daemon.
- Both zig targets build in CI with a 2.28 glibc floor.
- A `v0.1.0` tag produces a release with the tarball and its SHA-256, and
  `tar xf && make CC="zig cc -target x86_64-linux-gnu.2.28"` builds from
  it in a clean container.
