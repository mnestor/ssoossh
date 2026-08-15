# Splitting the signer into its own process (deferred)

**Status: deferred, not started, not scheduled.**

Consolidates the original signer-separation design notes and the deferred
NATS phase of the pipeline plan. The pipeline it builds on is described in
[signing-pipeline.md](signing-pipeline.md) and works today, in one process, on
Watermill's in-memory `gochannel`.

Implementation is not planned until there is an actual reason to go
multi-process — real HA requirements, or wanting the signer physically
isolated from the webserver for CA key hygiene. The
[release plan](release-plan.md) explicitly keeps the signer in-process and
preserves the split *seam* instead: `server/certmsg`, `server/signer`'s
zero-database test, and `server/bootstrap/pipeline.go`.

## Motivation

Pull SSH CA signing out of the `ssoosshd` webserver process so the CA private
key never lives in the same memory space as HTTP handling, OIDC calls, and
LDAP calls — a much larger attack surface.

## Why message-passing rather than RPC

A direct gRPC/mTLS call from the approving node to the signer would have to
either block that node's thread waiting for the result, or re-publish the
result to whichever node holds the SSE connection — two hops instead of one,
with a blocked thread in between.

With Watermill the signer publishes the result directly to wherever the SSE
thread is listening. One signaling model end to end, and the cluster stays
stateless with respect to "which node owns this request": no node is pinned to
a request from approval through delivery.

## Authorization: mTLS to NATS

**Considered and rejected:** encrypting the request payload to the signer and
treating decryption failure as "invalid requestor."

- Plain encryption does not authenticate a sender — that needs an AEAD tag or
  a sender-authenticated scheme like libsodium's `crypto_box`, not "decrypts
  or doesn't."
- The payload isn't secret anyway. It is essentially the future certificate's
  own contents (public key, principals, options). Confidentiality was never
  the requirement; **authenticity** was.
- A shared symmetric key across the fleet gives coarse "some legitimate
  cluster member" auth, not per-node identity, and is awkward to rotate.

**Decided:** mTLS to NATS, using the broker's own transport security instead
of custom crypto.

- Each webserver node and the signer get their own client certificate from an
  internal CA (possibly ssoossh's own).
- NATS `authorization` maps certificate identity (CN/SAN) to permissions:
  webserver nodes publish on the sign subject and subscribe to their reply
  subject; the signer subscribes to the sign subject and publishes replies.
- Revocation is pulling or rotating one node's certificate, not rotating a
  shared secret across the fleet.
- The NATS connection itself requires TLS (`tls { verify: true }`), so this
  isn't app-layer auth bolted onto a plaintext transport.

## Config

Top level — infrastructure, not certificate-issuance policy, so **not** under
`CertOptions`:

```go
type PubSubConfig struct {
    NATS NATSConfig `mapstructure:"nats"`
}

type NATSConfig struct {
    URL string        `mapstructure:"url"` // empty = in-process gochannel (default)
    TLS NATSTLSConfig `mapstructure:"tls"`
}

type NATSTLSConfig struct {
    CertFile string `mapstructure:"cert_file"`
    KeyFile  string `mapstructure:"key_file"`
    CAFile   string `mapstructure:"ca_file"`
}
```

**Hard requirement, not a warning:** if `NATS.URL != ""` then `CertFile`,
`KeyFile`, and `CAFile` must all be set or startup fails closed. Validated at
config load or `pubsub.New` construction.

## Durability upgrade

The sign queue (`certrequest.sign`) and the signed-reply topic
(`certrequest.signed`) want JetStream once running across processes: durable
consumer, ack-on-persist (**not** ack-on-signed — see the ack-timing section
of [signing-pipeline.md](signing-pipeline.md)), redelivery if a consumer dies
mid-job.

The wake topic (`certrequest.wait.<id>`) stays fine on plain core NATS. Same
reasoning as in gochannel mode: the database is truth and `Wait` re-derives
state on reconnect.

This is also what finally delivers at-least-once redelivery across a restart,
which gochannel cannot provide at any ack timing.

## Concurrency

Signing is effectively single-threaded when the CA key lives in an ssh-agent,
which serializes on its socket. A concurrency-limited consumer group on the
signer's subscription serializes access and absorbs bursts without extra code
— no separate queue in front of it.

## Startup modes

One binary, mode selected at startup. Flag versus cobra subcommands
(`ssoosshd serve` / `ssoosshd sign`) — subcommands are probably cleaner, since
signer-only doesn't need most of the flags and config validation the full
server does. Decide when implementing.

- **Full (default)** — webserver + listener/resolver + signer goroutine in one
  process. This is what exists today; it becomes one option among three rather
  than the only one.
- **API only** — webserver + listener/resolver, no signer thread. For
  deployments where the signer runs elsewhere.
- **Signer only** — signer component alone: pub/sub connection and CA key
  access, no database, no HTTP server. Since this genuinely runs as a separate
  process it **requires NATS** — gochannel is in-process by definition and
  cannot bridge two OS processes. Fail fast with a clear error if selected
  without `pubsub.nats.url`.

## Open questions

- Does signer-only need a slimmed-down config (just `pubsub` plus CA key
  location), or is reusing the full `Config` struct with unused
  OIDC/LDAP/DB/HTTP fields acceptable for v1? Reusing is simpler; a dedicated
  config is more correct from a "don't let secrets it doesn't need sit in its
  config file" standpoint.
- Exact NATS subject naming and partitioning. The gochannel design settled on
  a **shared** reply topic filtered by request ID; confirm that still holds
  with a real cluster, or whether per-node reply subjects change it.
- Certificate rotation and issuance for the internal mTLS PKI between
  webserver nodes, NATS, and the signer.
- Whether signer replicas (for HA or future HSM-backed concurrency) share one
  consumer group off the same subject, and how that affects ordering.

## Related

- [signing-pipeline.md](signing-pipeline.md) — what exists today.
- [multi-instance-safety-plan.md](multi-instance-safety-plan.md) — NATS is a
  precondition for running more than one `ssoosshd` against a shared database,
  but does not by itself make the system multi-instance safe.
