# Multi-instance safety — plan

**Status: implemented; kept as the design record.** Every item in the
"Remaining" list below is done; operator-facing documentation is in
[Running more than one instance](https://mnestor.github.io/ssoossh/operations/multi-instance/) and
[Startup modes](https://mnestor.github.io/ssoossh/operations/startup-modes/).
This was the prerequisite plan for running more than one
ssoosshd against a shared database. Overlaps heavily with
`docs/dev/signer-split-deferred.md` — NATS is a *precondition* for most
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
(https://mnestor.github.io/ssoossh/internals/architecture/). They're delivered once, via
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

### The fix is small — and as of 2026-08-22 it is already done

**Status: implemented.** This section described `Wait` discarding the wake
payload and re-reading the database instead. That is no longer the code.
`Wait` now calls `tryHandleWakeMessage` (`server/service/certrequest.go`),
which decodes `requestOutcomeMessage`, accepts only terminal statuses,
re-reads the request, and returns the message's certificate **only** once
the database confirms the same status. The `Wait` loop comment states the
intent directly: "This is what lets an SSE client on instance B receive a
certificate issued on instance A."

So delivery already follows the transport rather than process memory: the
listener publishes to `certrequest.wait.<id>`, and whichever instance holds
that subscription answers its client. **The only thing still missing is a
transport that crosses processes.** `server/pubsub` is hardcoded to
gochannel, which routes in-process only, so the wake never leaves the
instance that published it.

This is why the remaining NATS lift is smaller than this plan originally
assumed — the application-layer change it called for has landed, and what
is left is transport, configuration, and the consumer-semantics work in
item 1 below.

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

### 2. An unbounded approval window would break the sweep

If the approval window could be disabled there would be no derivable bound,
so the sweep would treat every `signing` row as stranded. That is safe as a
single-process boot pass (nothing this process queued can be in flight yet)
and actively wrong with several instances: a restarting instance B would
invalidate instance A's live in-flight requests.

Resolved structurally rather than by rule: `cert_options.client_timeout`
must be positive, and both the approval TTL and the signing grace derive
from it, so no configuration produces an unbounded window. Every instance
derives the same bound from the same value.

`signing_started_at` would still remove the derivation and its imprecision,
and is the option deferred in https://mnestor.github.io/ssoossh/internals/architecture/ — but it is now an
accuracy improvement, not a fix for a hazard.

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

- ~~It grows unbounded — one entry per request, for process lifetime.~~
  **Fixed 2026-08-22.** `EvictResolved` ages entries out at the approval
  TTL,
  scheduled as its own job. `resolvedAt` is never earlier than the
  request's `created_at`, so an entry that old belongs to a request that
  has itself expired and no client can still be waiting on it. Worth noting
  it was a security wrinkle as well as a memory one: each entry held a
  signed certificate, resident for the life of a process that otherwise
  takes care never to write one to disk.
- Its absence must never be treated as "no such request" anywhere. `Wait` is
  already written this way; worth an explicit test so it stays that way.

## Suggested order

Revised 2026-08-22. Three of the original five steps are done, which is why
what remains is mostly transport.

- ~~1. `Wait` decodes the wake payload (the central fix).~~ **Done.**
  `tryHandleWakeMessage` decodes it and re-verifies the status against the
  database before trusting the certificate.
- ~~2. `signing_started_at` column and sweep rework.~~ **Superseded, and the
  hazard is gone.** Rather than add a column to make the disabled-TTL case
  safe, the approval TTL may no longer be zero (`cert_options.client_timeout`,
  which it derives from, must be positive):
  `config.CertificateOptions.Validate` rejects it at startup, and the
  sweep's disabled-TTL branch has been deleted. That removes the
  multi-instance hazard in item 2 at the source — there is no longer a
  configuration in which the sweep lacks a bound. `signing_started_at`
  remains worth having for precision (the sweep still derives its cutoff
  rather than reading an absolute timestamp), but it is no longer a
  blocker.
- ~~5. `resolved` eviction.~~ **Done.** `CertRequestService.EvictResolved`
  runs on its own schedule, deliberately not folded into the database sweep
  so it can never inherit leader gating — see item 3 and the function's
  doc comment.

Remaining, in order:

~~1. **NATS transport with queue groups.**~~ **Done (2026-08-22).**
   `server/pubsub.New` branches on `config.Backend` and uses either gochannel
   or watermill-nats with queue groups. See below for details.
~~2. **Startup validation.**~~ **Done.** `NewConfig` validates that
   `MultiInstance=true` requires an explicit `http.cookie_key`, and
   `PubSubConfig.Validate()` requires mTLS credentials when `Backend=nats`.

### The NATS implementation (completed 2026-08-22)

`server/pubsub.New` now accepts a `*config.PubSubConfig` and branches on
`Backend`. Watermill's `message.Publisher`/`message.Subscriber` interfaces
let both transports coexist without changing handlers.

1. ~~**Config.**~~ **Done.** `server/config/types_pubsub.go` defines
   `PubSubConfig` with backend selection and mTLS material. `Validate()`
   enforces credentials at startup. Defaults are in `server/config/defaults.yaml`.
2. ~~**The constructor.**~~ **Done.** `server/pubsub.New()` branches on
   `Backend` and calls either `newGoChannel()` or `newNATS()`. Both build a
   shared `message.Router` with the same middleware and `CloseTimeout`.
3. ~~**Queue groups.**~~ **Implemented, unit tested.** `subjectCalculator()`
   derives queue groups from topic names: `certrequest.sign` → "signer",
   `certrequest.signed` → "signed-listeners", and `certrequest.wait.*` → empty
   (fan-out). Unit test `TestSubjectCalculator_ShouldReturnQueueGroupForSignTopics`
   verifies derivation. Full integration test (two subscribers per queue receiving
   exactly one message each) would require a TLS-configured NATS server; the
   mechanism itself is delegated to watermill-nats/v2.2.0, which has its own tests.
4. ~~**Durability for the sign queue.**~~ **Done.** `JetStream.Disabled = true`
   for NATS core (at-most-once). A dropped job costs a full `client_timeout`
   wait,
   acceptable for interactive approval. See `server/pubsub/pubsub.go` for
   documentation of this choice.
5. ~~**Declare multi-instance explicitly.**~~ **Done.** Added explicit
   `multi_instance` config field. Checked in `NewConfig`: if true, requires
   explicit `http.cookie_key`. Avoids conflating transport (NATS) with
   topology (multi-instance); a single instance may use NATS to isolate the
   signer into a separate process (see docs/dev/signer-split-deferred.md).

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

- ~~**How is "multi-instance" declared?**~~ **Resolved (2026-08-22):** Explicit
  `multi_instance` config field. Avoids conflating transport choice (NATS can
  be used by a single instance to isolate the signer, per docs/dev/signer-split-deferred.md)
  with deployment topology. When `multi_instance=true`, `http.cookie_key` must
  be explicitly set (see startup validation above). This separation lets
  operators choose NATS for signer isolation without declaring multi-instance
  topology.
- ~~**Is the certificate acceptable on the wire in the wake message?**~~ **Resolved:** Yes, certificate on the wire is fine. It is already published that way and is public data. Multi-instance crosses the network but that is mTLS/authorization (NATS subject layout per `docs/dev/signer-split-deferred.md`), not a new exposure in kind.
- **Does the 410 path get noisier?** With instances restarting behind a load
  balancer, in-flight deliveries are lost more often than in a single-process
  deployment. If that proves common, persisting certificates (the option
  Phase 4 rejected) becomes worth revisiting — but only with evidence.
