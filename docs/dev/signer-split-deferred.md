# Splitting the signer into its own process

**Status: implemented, 2026-08-23.**

Consolidates the original signer-separation design notes and the deferred
NATS phase of the pipeline plan. The pipeline it builds on is described in
[signing-pipeline.md](../internals/signing-pipeline.md) and works today, in one process, on
Watermill's in-memory `gochannel`.

The split shipped as the `ssoosshd serve api` and `ssoosshd sign` startup
modes; operator-facing documentation is in
[deployment.md](../operations/deployment.md#6-startup-modes-full-api-and-sign). The
seam it runs on is unchanged: `server/certmsg`, `server/signer`'s
zero-database test, and `server/bootstrap/pipeline.go`. This document is
kept as the design record.

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

## Durability: NATS core, not JetStream

**Decided: NATS core with at-most-once delivery.** A dropped signing job is
acceptable because the flow is interactive — the person who just approved sees
the CLI hanging and re-runs login, which costs one `client_timeout` wait. JetStream
durability was considered and deferred as a future hardening: the ack-timing
discussion in [signing-pipeline.md](../internals/signing-pipeline.md) becomes moot for
multi-process deployments (the transport's own acks suffice), and durability
gains from JetStream can be added later without breaking the API.

The wake topic (`certrequest.wait.<id>`) stays fine on plain core NATS. Same
reasoning as in gochannel mode: the database is truth and `Wait` re-derives
state on reconnect.

## Concurrency

Signing is effectively single-threaded when the CA key lives in an ssh-agent,
which serializes on its socket. A concurrency-limited consumer group on the
signer's subscription serializes access and absorbs bursts without extra code
— no separate queue in front of it.

## Startup modes

**Decided: cobra subcommands.** `ssoosshd serve` with modes (full/api), and
`ssoosshd sign` for the signer. Subcommands are cleaner since signer-only
doesn't need most HTTP/OIDC/DB flags and validation.

- **Full mode (default)** — webserver + listener/resolver + signer goroutine
  in one process. This is what exists today; it becomes one option rather
  than the only one. Invoked via `ssoosshd serve` (or bare `ssoosshd serve full`).
- **API-only mode** — webserver + listener/resolver, no signer. For deployments
  where the signer runs elsewhere. Invoked via `ssoosshd serve api`. Requires
  NATS; fails with a clear error on gochannel, since signing jobs would be
  published into a void.
- **Signer-only mode** — signer component alone: pub/sub connection and CA key
  access, no database, no HTTP server. Runs as its own process. Invoked via
  `ssoosshd sign`. Requires NATS; fails with a clear error on gochannel,
  since the signer cannot bridge separate OS processes.

## Decisions closed

- **Config reuse (decided): full `Config` struct.** Signer mode loads the
  entire config as v1's simpler option. Mode-aware validation skips unneeded
  checks (no DB, HTTP, OIDC validation). A future hardening (secrets in
  memory the signer never uses) is documented as residual concern.
- **NATS subject naming (decided): per-implementation.** `subjectCalculator`
  in `server/pubsub.go` maps topics to queue groups: `certrequest.sign` →
  "signer", `certrequest.signed` → "signed-listeners", `certrequest.wait.*`
  → no queue group (fan-out). Shared reply topic confirmed in multi-instance
  testing.

## Open questions for future work

- Certificate rotation and issuance for the internal mTLS PKI between
  webserver nodes, NATS, and the signer. Today it is assumed operators bring
  their own PKI (ssoossh's own, a standalone internal CA, or a cluster mesh).
- Whether signer replicas (for HA or future HSM-backed concurrency) share one
  consumer group off the same subject, and how that affects ordering.

## Related

- [signing-pipeline.md](../internals/signing-pipeline.md) — what exists today.
- [multi-instance-safety-plan.md](multi-instance-safety-plan.md) — NATS is a
  precondition for running more than one `ssoosshd` against a shared database,
  but does not by itself make the system multi-instance safe.
