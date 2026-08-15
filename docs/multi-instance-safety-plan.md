# Multi-instance safety — plan

**Status: planned, not implemented.** Prerequisite for running more than one
ssoosshd against a shared database. Overlaps heavily with
`docs/signer-split-deferred.md` — NATS is a *precondition* for most
of this, but replacing the transport alone does **not** make the system
multi-instance safe. This document covers what else has to change.

## Context

Phases 1–5 built the certificate pipeline against an in-process, in-memory
transport, with one server process assumed throughout. Several pieces
quietly depend on that assumption in ways that a bigger broker won't fix by
itself. The most important is that a certificate currently lives only in the
memory of whichever instance's listener resolved it — so with a load
balancer in front, the instance holding the client's SSE connection
generally isn't the one holding its certificate.

The goal is: N instances behind a load balancer, sharing one database and
one NATS cluster, with no client able to tell which instance it reached.

## The central problem: delivery is instance-local

Certificates are deliberately never persisted
(`docs/signing-pipeline.md`). They're delivered once, via
`CertRequestService`'s in-memory `resolved` cache and a wake message.

With several instances, the request path fans out:

- the client's SSE connection (`Wait`) lands on **instance B**
- the browser approval lands on **instance A**
- the signer's reply is consumed by **whichever instance's listener wins**

Nothing guarantees these are the same process. Today instance B's `resolved`
is empty, so `Wait` falls through to the database, finds `approved`, and
returns `CertificateUnavailableError` (410) — telling a user their
certificate is gone when it was successfully issued a moment ago on another
instance. That's the single biggest blocker, and it would present as
intermittent, load-balancer-dependent failures.

### The fix is small, because the wake message already carries the certificate

`requestOutcomeMessage` already includes `Certificate` and `Code`, and
`notifyWaiter` already populates them. `Wait` simply throws the payload away:

```go
case msg, ok := <-messages:
    if ok {
        msg.Ack()
    }
    continue        // ← re-reads the DB and the local cache instead
```

Change `Wait` to decode that payload and, when it carries a terminal
outcome, return it directly (populating `resolved` on the way through).
Then delivery follows the transport rather than process memory: the listener
publishes to `certrequest.wait.<id>`, NATS routes it to whichever instance
holds that subscription, and that instance answers its client.

This keeps certificates ephemeral — no schema change, no reversal of the
Phase 4 decision — and it degrades exactly as designed: a client that
reconnects *after* the message is gone still gets the 410 and re-requests.

Worth noting the DB read stays as the authority for *status*; the message is
only how the certificate body travels. A malformed or unexpected payload
should fall back to the existing DB path rather than being trusted blindly.

## The rest, in priority order

### 1. Competing consumers, not fan-out (blocker)

gochannel delivers **every message to every subscriber**. NATS core
subscriptions do the same. With N instances each running a signer and a
listener, one approval would produce N certificates and N audit rows.

Both consumers must use queue-group semantics (NATS queue groups /
JetStream durable consumers with a shared name) so exactly one instance
handles each message. This is a property of how the subscription is created,
so it belongs in `server/pubsub`'s NATS constructor rather than in the
handlers — the handlers shouldn't have to know.

The wake topic is the opposite case: it *wants* fan-out semantics scoped to
one subscriber, since only the instance holding that client cares. Per-request
subjects already give that naturally.

### 2. The sweep's `RequestTTL = 0` fallback (blocker in that configuration)

With `RequestTTL` disabled there's no derivable bound, so the sweep treats
every `signing` row as stranded. That's safe as a single-process boot pass
(nothing this process queued can be in flight yet) and actively wrong with
several instances: a restarting instance B would invalidate instance A's
live in-flight requests.

Options, in preference order:

1. **Add `signing_started_at`.** Removes the derivation, the imprecision,
   *and* this special case in one column — the sweep gets an absolute bound
   that's correct regardless of instance count. This is the option deferred
   in `docs/signing-pipeline.md`, and multi-instance is
   the argument that tips it.
2. Require `RequestTTL > 0` when multi-instance is enabled, and fail
   startup otherwise. Cheap, but pushes a subtle constraint onto operators.

Recommend (1).

### 3. Duplicate scheduled work (correctness-safe, wasteful)

Every instance registers and runs the sweep. The guarded `UPDATE` means
only one wins each row, so this is *correct* — but it's N× the queries, and
any future job that isn't idempotent would be a real bug.

Options: leader election (a lease row in the database), or accept
duplication and make idempotency an explicit, documented requirement for
every scheduled job. For one already-idempotent job, documenting the
requirement is proportionate; revisit when a second job appears that isn't.

### 4. Session key must be shared (operational)

`resolveSessionSecret` generates a random key when `http.cookie_key` is
unset, which is process-local — sessions break on every request that lands
on a different instance. Already documented on the config field and in the
function, so this is about enforcement, not discovery: **fail startup**
rather than warn when multi-instance is enabled without an explicit
`cookie_key`, since the failure mode otherwise is confusing intermittent
logouts.

### 5. The `resolved` cache becomes a local optimization only

Once (the central fix) lands, `resolved` is a same-instance fast path rather
than a source of truth. Two consequences worth handling deliberately:

- It grows unbounded — one entry per request, for process lifetime. Fine as
  a short-lived cache; needs eviction (TTL or size cap) if it's now expected
  to be long-lived. Same class of problem as the gochannel persisted-message
  growth found in Phase 2.
- Its absence must never be treated as "no such request" anywhere. `Wait` is
  already written this way; worth an explicit test so it stays that way.

## Suggested order

1. `Wait` decodes the wake payload (the central fix) — self-contained, and
   testable *today* against gochannel with two `CertRequestService`
   instances sharing one database and transport, before NATS exists.
2. `signing_started_at` column and sweep rework — also independent of NATS.
3. NATS transport with queue groups (this is Phase 6's core).
4. Startup validation: require `cookie_key`; document job idempotency.
5. `resolved` eviction.

Steps 1 and 2 are worth doing regardless of whether multi-instance ever
ships: 1 makes delivery robust to *which* component resolved a request, and
2 removes an accepted imprecision plus a config special case.

## Verification

The decisive test doesn't need NATS: construct **two** `CertRequestService`
instances over one shared database and one shared transport, then assert
that a client waiting on instance B receives a certificate whose entire
lifecycle (approve, sign, listener resolve) happened on instance A. That
fails today and would pass after step 1 — it's the regression test for the
whole problem.

Beyond that:

- Two listeners on one reply topic produce exactly one audit row (needs
  NATS queue groups; can't be asserted meaningfully on gochannel).
- A sweep running concurrently on two instances doesn't invalidate a
  request another instance just approved (needs `signing_started_at`).
- Startup fails when multi-instance is configured without `cookie_key`.

## Open questions

- **How is "multi-instance" declared?** Several checks above want to behave
  differently when it's expected (fail on missing `cookie_key`, require a
  bounded sweep). Options: infer it from NATS being configured, or an
  explicit setting. Inferring is fewer knobs but conflates transport choice
  with deployment topology — a single instance might reasonably use NATS to
  isolate the signer.
- **Is the certificate acceptable on the wire in the wake message?** It's
  already published that way today, and a certificate is public data, but
  multi-instance means it now crosses the network to reach the waiting
  instance. That's a mTLS/authorization question for the NATS subject layout
  (`docs/signer-split-deferred.md` covers the model), not a new exposure in kind.
- **Does the 410 path get noisier?** With instances restarting behind a load
  balancer, in-flight deliveries are lost more often than in a single-process
  deployment. If that proves common, persisting certificates (the option
  Phase 4 rejected) becomes worth revisiting — but only with evidence.
