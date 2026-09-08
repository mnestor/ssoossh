# Client-side CA key cache

**Status:** idea. Not scheduled. No prerequisites; every piece it needs
already exists in the client. Written up after a deployment that holds its
CA key in an ssh-agent and therefore cannot start `ssoosshd` unattended
([the ssh-agent page](https://mnestor.github.io/ssoossh/operations/ssh-agent/)),
which turns a routine server restart into a client-visible outage that the
client has no reason to suffer.

## The problem

`RootCommand.Init` fetches `GET /api/ca` on every non-offline invocation
whenever `capubkey` is empty (`client/cmd/cmd.go:169`), and stores the
result in the same field an operator would have pinned. Two consequences:

**Every `Match exec` pays an HTTP round trip** before `ssh` starts
connecting, for a key that changes approximately never.

**A client with a perfectly good certificate fails when the server is
unreachable.** The certificate-reuse fast path in `runLogin` returns before
any network call (`client/cmd/ssh_login.go:312`), but it sits downstream of
the fetch, so it is never reached. Measured against a stopped server, with
a valid certificate on disk:

| client config | valid cert loaded | `ssoossh ssh login` |
| --- | --- | --- |
| `capubkey` pinned | yes | exit 0, reuses the certificate |
| not pinned | yes | exit 1, `get CA public key: ... connection refused` |

The second row is the bug. Nothing about that invocation needed the server.

## The change

When no `capubkey` is configured, cache the fetched key locally, use the
cache instead of fetching, and refresh it in the background about once a
day. A refresh that fails changes nothing and is not an error.

### Accept a list of keys

`/api/ca` already returns every active signer key, newline-separated, which
is how a CA rotation appears to a client: both keys are live at once while
the old one ages out of the server's registry.

The agent layer already handles this. `SetCA` is variadic and
`parseCAPublicKeys` splits each string on newlines for exactly this reason
(`internal/crypto/ssh/agent/certificate.go:37`), so a multi-line `capubkey:`
block scalar works today. What is missing is a YAML sequence on
`CAPubkey string` (`client/config/types.go:7`).

The one complication is `client/config/policy_windows.go:26`, which maps
`CAPubkey` to `capubkey` as a single registry value. A list needs a
representation there (`REG_MULTI_SZ`, or keep accepting the newline-joined
string), and the macOS plist reader needs the matching decision.

### Keep the cache out of the config merge

The obvious implementation is to write the fetched key into the per-user
config file. It should not be done that way.

The search paths are `systemDir`, `userFile`, `localFile` in *increasing*
order of precedence (`client/config/paths.go:16`), so
`~/.config/ssoossh.yaml` overrides `/etc/ssoossh/ssoossh.yaml`. A cache
written there permanently shadows any `capubkey` an administrator later
adds to the system config: silently, per-machine, and only on the machines
that happened to cache a value first. The `enforce` file and the
platform-native policy sources do win over the user file
(`client/config/config.go:43`, `:128`), but that is an obscure escape hatch
for what looks like an ordinary setting.

Store the cache in its own file instead, consulted only when the merged
`capubkey` is empty. A system config that later sets the key then always
wins, the cache cannot shadow anything, and the client never has to rewrite
a hand-edited YAML file (comments, key order, the user's other settings)
to store machine state.

That choice also settles concurrency. `Match exec` runs once per `ssh`
connection and people open several at once, so refreshes race.
`writeFileAtomic` (`client/cmd/atomicwrite.go:18`, already used for
enrollment keys and certificates) makes that last-writer-wins, which is
correct for a cache and would be a bad property for a file holding the
user's settings.

### Refresh semantics

- **Daily, best-effort.** On failure, keep the cached value and proceed. A
  refresh that can fail the command reintroduces the outage this exists to
  remove, just less often.
- **Also on mismatch.** If a returned certificate does not validate against
  the cached set, refresh immediately rather than waiting out the day. That
  is the signal that the cache is actually stale, and it is what makes a
  rotation converge in minutes instead of a day.
- **Compare sets, not values.** Any overlap between the cached and fetched
  sets is an ordinary rotation: adopt it quietly. A fully disjoint
  replacement is the only interesting case, and it is also what a rotation
  looks like to a client that was offline for the whole overlap window, so
  adopt it too but log a warning naming both fingerprints. Refusing would
  strand exactly the laptops that were shut for a week.

### Do not call it pinning

A cached key must not be reported as a pinned one. `CAPubkeyPinned` is
computed as `cfg.CAPubkey != ""` immediately before the fetch
(`client/cmd/cmd.go:167`), and `pinnedCAFingerprints` feeds
`TrustedCAFingerprints` on the certificate request
(`client/cmd/ssh_login.go:362`, `internal/apitypes/certrequest.go:65`) so
the approval page can warn that this client will refuse anything else. If
the cache lands in that field, every client in an estate starts asserting a
trust anchor no operator set, and the warning becomes noise.

Two fields, then: `capubkey` stays the operator's pin and is never written
by the client; the cache is separate and reports nothing.

## What this is not

It is a cache, not a trust decision. Auto-adopting whatever the server last
said is not pinning and gives none of pinning's guarantees.

That is defensible here because of what the client actually uses the CA list
for: recognising its *own* certificates, and nothing else
(`internal/crypto/ssh/agent/fileagent.go:255`, `:313`,
`internal/crypto/ssh/agent/agent.go:119`, `:202`). It is not host
verification. A wrong cached key means the client stops recognising its
certificates and requests new ones, which is an availability failure rather
than a compromise. An operator who wants a real trust anchor pins
`capubkey`, and this feature must never overwrite it.

## Incidental fix

`client/config/defaults.yaml:65` already tells operators that with
`capubkey` empty "the client fetches it from the server on first use". That
is not true today; it fetches on every use. This change makes the
documentation correct.

## Open questions

- Where the cache file lives. The per-user config is a file, not a
  directory, on non-Windows (`~/.config/ssoossh.yaml`), so a sibling path
  or a new `~/.config/ssoossh/` directory both need a decision, alongside
  the Windows `%AppData%\ssoossh\` case which is already a directory.
- The Windows policy representation for a list, above.
- Server-side registry TTL is 15 minutes (`server/bootstrap/services.go:63`)
  against a daily client cache, so during a rotation a client can present a
  certificate signed by a key the server has already dropped from
  `/api/ca`. Worth confirming that path behaves before shipping the daily
  refresh as a default.
