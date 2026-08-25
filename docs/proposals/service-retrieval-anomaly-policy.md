# Service retrieval anomaly policy

**Status: designed, nothing built.** No code has been written for this. The
design below is settled to the point where an implementation plan can be
derived from it. Every `file:line` anchor was verified against `5d23809`
(2026-08-24) and will drift.

> **Before planning from this document**, re-run the verification pass in
> [Provenance](#provenance-what-was-verified-and-how), and re-read the
> reasoning in [Decisions](#decisions-and-the-reasoning-behind-each) rather
> than only the decisions. Several rest on judgement calls about operator
> behaviour and deployment shape, not on facts about the code; those are
> flagged **[judgement]** and are the ones worth re-opening first.

This builds on the service enrollment flow described in
[flows.md](../guide/flows.md) and the notification machinery in
[email-notifications.md](../operations/email-notifications.md). Read the latter first: the
registry-driven notification design is reused here almost unchanged, and the
one place this design has to extend it is called out explicitly.

## What this proposes

An enrollment code is a bearer credential. It is deliberately long-lived —
`enrollment_duration` defaults to a year — precisely so an unattended job can
redeem it from cron without a human in the loop
(`server/config/types_certificates.go:141`, default named at `:86`). The compensating control today
is that the code is bound to one public key at approval and never re-paired,
so a code lifted on its own mints nothing
(`server/model/enrollment.go:5-9`).

That leaves one uncovered case: a code lifted **together with its private
key**, off the host that legitimately holds both. The redemptions that
follow are indistinguishable from legitimate ones in every respect except
one — they come from somewhere else. The retrieval log already records that
(`server/model/enrollment.go:68`), and nothing reads it for this.

This proposes reading it: count the distinct source addresses that redeemed
one code inside a sliding window, alert when that count crosses a threshold,
and — at a second, higher threshold — take a configured enforcement action
against the code, escalating to a security address that is not the code
owner's.

Two thresholds, because they answer different questions. The first is "a
human should look at this". The second is "do not wait for a human".

## What exists today (verified)

| Piece | Where | State |
| --- | --- | --- |
| Per-redemption source IP | `server/model/enrollment.go:65-81` | Recorded, indexed by `enrollment_id` only |
| Redemption path | `server/service/enrollment.go:94` | Writes the retrieval row at `:148`, before queueing the signing job at `:185` |
| Failed redemptions | `server/service/enrollment.go:148-160` | Logged too — the row is written before signing, `succeeded` flips at `:576` |
| Expiry as a gate | `server/service/enrollment.go:104-108` | An expired code answers exactly like an unknown one |
| Admin expire lever | `server/controller/admin.go:107` | `PATCH /api/admin/enrollments/{id}/expire`, sets `expires_at = now` |
| Notification registry | `server/notify/notify.go:74` | Adding a kind is four local edits, everything else is driven off it |
| Notification delivery | `server/service/notification.go:314` | Resolves `users.id` -> address, checks the stored preference, renders, sends |
| Recipient addressing | `server/notify/event.go:25` | `Event` names a recipient **only** by `users.id` |
| Transport recipient | `server/mail/sender.go:17-21` | `Outgoing.To` is already a plain address string |
| Source address derivation | `server/controller/enrollment.go:60` | `g.ClientIP()`, i.e. subject to `http.trusted_proxies` |

Two facts from that table drive most of the design below:

1. **`Outgoing.To` is already just a string.** Sending to a security mailbox
   needs no transport change — only a way for an `Event` to name a literal
   address instead of a user.
2. **Group membership is never persisted** (`server/CLAUDE.md:23`; groups
   arrive as OIDC claims per session and are dropped). There is no query
   that returns "the admins". Anything addressed to an administrator must be
   addressed by configuration, not by role lookup.

## The model

### Counting

For one enrollment, at redemption time:

```sql
SELECT source_ip FROM enrollment_retrievals
WHERE enrollment_id = ? AND retrieved_at > ? -- now - window
```

Distinct-count the results after normalization (below). The row for the
attempt in hand is already written when this runs, so the attempt that
crosses a threshold is itself counted — and can itself be blocked.

Every row in the window counts, successes and signing failures alike. A row
exists because a valid code was presented; whether the signer then answered
says nothing about who presented it, and excluding failures would blind the
detector to exactly the noisy, half-working case worth catching.

### Normalization

Raw addresses are the wrong unit twice over:

- **IPv6.** A single dual-stack host with SLAAC privacy extensions rotates
  through many addresses inside its own `/64` as a matter of routine. Counted
  raw, one legitimate cron host trips a threshold of 3 within a day.
- **IPv4 egress pools.** A job behind a NAT pool or a CGNAT range leaves a
  different address per redemption without moving an inch.

So each address is masked to a configured prefix before the distinct count —
`/32` and `/64` by default, which is a no-op for IPv4 and collapses the IPv6
case. An operator with a known-wide egress pool widens `ipv4_prefix`, or
lists the pool in `exempt_networks`, whose members are excluded from the
count entirely rather than collapsed to one.

Empty `source_ip` values (the column defaults to `''`) are excluded. A row
that recorded no address is missing evidence, not evidence of a new address.

### The state machine

State lives on the enrollment row, and moves only through compare-and-swap
updates:

```
                 count >= alert_threshold
   normal  ─────────────────────────────────►  alerted
      ▲                                           │
      │  count < alert_threshold                  │  count >= lockdown_threshold
      └───────────────────────────────────────────┤
                                                  ▼
                                               locked   ──► admin unlock ──► normal
```

- `normal -> alerted` sends the owner alert and the alert-address copy.
- `alerted -> normal` is automatic: once the window drains below the alert
  threshold, the code is quiet again and a later spike should alert again.
  Nothing is sent on this edge.
- `-> locked` applies the configured action and sends the lockdown copy.
  **`locked` never self-clears.** The window draining is not evidence the
  credential is clean.
- Re-alerting while `alerted` is suppressed until `alert_cooldown` has passed
  *and* the distinct count has grown past the count recorded at the last
  alert. Both conditions, because a code sitting steadily at four addresses
  is one situation to report once, not every five minutes.

Every transition is a conditional `UPDATE ... WHERE id = ? AND
anomaly_state = ?`; the instance whose update reports `RowsAffected == 1`
owns the notification. This is the multi-instance case, which is supported
and shipped (`docs/dev/multi-instance-safety-plan.md`) — N instances share
one database, all of them see the same crossing, and exactly one must mail
about it.

### Where it runs

Inside `EnrollmentService.Retrieve`, in this order:

1. Load the enrollment; expiry check as today (`:104`).
2. **New:** if `anomaly_state = 'locked'`, return `NotFoundError` — the same
   answer expiry gives, for the same reason stated at `:105-107`.
3. Decode principals and options; allocate the serial (unchanged).
4. Write the retrieval row (unchanged, `:148`).
5. **New:** evaluate the window. Apply the state transition. If the action
   taken blocks, return `NotFoundError` **before** the signing job is
   published at `:185`.
6. Sign, deliver, notify as today.

Step 5 sits between the row write and the publish so that the crossing
redemption is gated rather than merely reported. It costs one indexed query
on the redemption path; that query is the feature.

The notification for a crossing is queued the same way redemption
notifications are (`server/service/notification.go:76`) — it never blocks the
caller and never fails the retrieval.

## Config shape

Global, under the existing service block. No per-enrollment override and no
runtime store: this is one deployment-wide policy, configured where every
other certificate policy is configured.

```yaml
cert_options:
  service:
    enrollment_duration: 8760h
    valid_duration: 12h

    retrieval_anomaly:
      # Off by default. An upgrade must not start locking codes that have
      # been running from a NAT pool for a year.
      enabled: false

      # Sliding window the distinct count is taken over.
      window: 1h

      # Distinct source networks in the window that mean "a human should
      # look at this". Must be >= 2: one address is every healthy job.
      alert_threshold: 3

      # Distinct source networks that mean "do not wait for a human".
      # Zero disables the second level entirely, leaving alert-only.
      lockdown_threshold: 10

      # What crossing lockdown_threshold does to the code:
      #   lock        - refuse redemptions until an admin unlocks. Reversible.
      #   expire      - set expires_at = now. Irreversible; re-enroll to recover.
      #   alert_only  - change nothing about the code, just escalate.
      lockdown_action: lock

      # Suppresses repeat alerts for a code already in the alerted state.
      # A repeat also requires the distinct count to have grown.
      alert_cooldown: 6h

      # Who hears about it.
      notify_owner: true          # the approving user, via their preferences
      alert_addresses: []         # e.g. ["ssh-admins@example.com"]
      lockdown_addresses: []      # e.g. ["soc@example.com"] - the SOC copy

      # Counting units. /32 and /64 make IPv4 exact and collapse IPv6
      # privacy-address churn to one host.
      ipv4_prefix: 32
      ipv6_prefix: 64

      # Networks excluded from the count outright, for known-wide egress.
      exempt_networks: []         # e.g. ["203.0.113.0/24"]
```

Validation, at startup, alongside the existing `enrollment_duration` check
(`server/config/types_certificates.go:85`):

- `window > 0` when enabled.
- `alert_threshold >= 2` when enabled. A threshold of 1 fires on the first
  redemption of every code ever issued.
- `lockdown_threshold == 0 || lockdown_threshold >= alert_threshold`. A
  lockdown below the alert threshold means the code locks before anyone is
  told why.
- `lockdown_action` is one of the three spellings.
- `ipv4_prefix` in 8..32, `ipv6_prefix` in 32..128.
- every `exempt_networks` entry parses as a CIDR; every address in
  `alert_addresses` / `lockdown_addresses` parses as a mail address.
- **A warning, not an error**, when enabled with `mail.enabled: false` or
  with both address lists empty: enforcement still works, but nobody is
  told. See the decision below.

## Recipients, and the one extension to the notification path

Three audiences, two mechanisms.

**The code owner** is a user, and rides the existing path unchanged: a new
`notify.Kind`, `DefaultEnabled: true`, delivered by `users.id` and subject to
the stored preference (`server/service/notification.go:314-360`). They own
the code; they can turn its mail off.

**The alert and lockdown addresses** are not users. There is no way to
resolve them from a role, and no user row to hang a preference on. They need
an `Event` that names an address.

That is the only extension this design makes to `server/notify`:

```go
// Event, server/notify/event.go:25
type Event struct {
    Kind       Kind            `json:"kind"`
    UserID     string          `json:"user_id,omitempty"`
    Address    string          `json:"address,omitempty"` // NEW
    OccurredAt time.Time       `json:"occurred_at"`
    Payload    json.RawMessage `json:"payload"`
}
```

Exactly one of `UserID` and `Address` is set; `NewEvent` keeps its signature
and a sibling `NewAddressEvent` builds the other shape, both rejecting
unregistered kinds at the publishing call site as `NewEvent` does today.
`NotificationHandler.handle` branches at its user lookup: an address event
skips the `users` read, skips the preference read, and goes straight to
render and send. `mail.Outgoing.To` already takes a plain string
(`server/mail/sender.go:17`), so nothing below the handler changes.

Skipping the preference check for address events is deliberate and is the
point: a SOC copy that any user could silence is not a control.

`Notifier` grows one method rather than changing its existing one, so every
current call site compiles untouched:

```go
type Notifier interface {
    Notify(ctx context.Context, kind notify.Kind, userID string, payload any)
    NotifyAddress(ctx context.Context, kind notify.Kind, address string, payload any) // NEW
}
```

### The two kinds

| Kind | Sent to | When |
| --- | --- | --- |
| `service_enrollment_retrieval_anomaly` | owner + `alert_addresses` | `normal -> alerted` |
| `service_enrollment_locked` | owner + `lockdown_addresses` | `-> locked` |

One kind serves both a user and an address recipient — the rendering is
identical, only the addressing differs.

Payload for both: enrollment ID, certificate request ID, service account,
key ID, public key fingerprint, the window, the distinct count, the
threshold crossed, the observed networks (**bounded to the 10 most recent**,
with a total), first and last redemption instants in the window, and the
server URL. `service_enrollment_locked` adds the action taken and, when it
is `lock`, what an admin does to clear it.

**The code itself is never in a payload**, the rule
[email-notifications.md](../operations/email-notifications.md#what-is-never-emailed)
already states and `ServiceEnrollmentCreated` already honours
(`server/notify/payloads.go:9-16`). An alert about a possibly-leaked bearer
credential that mails the credential would be the worst possible version of
this feature.

## Schema

Columns on `enrollments`, one migration per driver, matching the existing
pair layout in `server/resources/migrations/{postgres,sqlite}/`:

```sql
ALTER TABLE enrollments ADD COLUMN anomaly_state TEXT NOT NULL DEFAULT 'normal';
ALTER TABLE enrollments ADD COLUMN anomaly_alerted_at TIMESTAMPTZ;
ALTER TABLE enrollments ADD COLUMN anomaly_alerted_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE enrollments ADD COLUMN locked_at TIMESTAMPTZ;
ALTER TABLE enrollments ADD COLUMN locked_reason TEXT NOT NULL DEFAULT '';
```

`anomaly_state` is not constrained to an enum for the same reason
`notification_preferences.kind` is not
(`server/resources/migrations/postgres/20260101000000_init.up.sql:247-249`):
the Go side is the authority, and a downgrade must leave rows inert rather
than block a migration.

And the index the detector needs. The existing one covers `enrollment_id`
alone (`...init.up.sql:240`); the window query filters on `retrieved_at`
within that:

```sql
CREATE INDEX IF NOT EXISTS idx_enrollment_retrievals_enrollment_retrieved_at
    ON enrollment_retrievals(enrollment_id, retrieved_at);
```

`model.Enrollment` gains the matching fields, and
`webtypes.ServiceEnrollmentResponse` (`server/webtypes/webtypes.go:140`)
gains `anomaly_state`, `locked_at`, and `locked_reason` so the owner can see
on the service codes page why their job stopped working.

## Decisions, and the reasoning behind each

### Scope is one enrollment code

Aggregating per service account or per approving user would catch a leak
spread across several codes, but it blurs the one thing the response needs:
*which code to lock*. The retrieval log is already keyed by enrollment, the
existing index is on `enrollment_id`, and the credential that leaked is a
code. Per-account detection is a later, additive layer that reads the same
rows; nothing here forecloses it.

### Config is global YAML only

Same reasoning the rest of the certificate policy follows. A per-enrollment
override would have to be fixed at approval time to respect
evaluate-at-enrollment-time (`server/service/enrollment.go:79-84`), which
means a code approved before a policy change keeps the old thresholds
forever — the exact opposite of what an operator tightening a threshold
after an incident wants. A runtime store would make the policy state to
seed, audit, and reconcile against YAML.

### The enforcement action is configurable, defaulting to `lock` **[judgement]**

The three actions differ in what they cost when they are wrong, and that
cost is a property of the deployment, not of the code:

- `alert_only` never breaks a working job and never stops an attacker.
- `lock` stops both, and an admin clears it in one click.
- `expire` stops both permanently; recovery is a re-enrolment, which for an
  unattended job means a human at a terminal.

`lock` is the default because it is the only one that both gates and is
reversible, and because a false positive costs an unlock rather than a
re-enrolment. Shops that want detection only set `alert_only`; shops that
treat a suspected leak as terminal set `expire`. Re-open this if operator
experience says false positives dominate.

### Enforcement does not depend on mail

`mail.enabled: false` disables notification, not locking. The gate is an
access-control decision that happens to have a notification attached, and a
deployment with no relay configured should still be able to stop a code
being redeemed from twenty places. This is why the config validation warns
rather than errors: a silent lock is degraded, not invalid.

### A locked code answers `NotFound`

Identical to the expiry answer at `server/service/enrollment.go:104-108`,
and for the reason given there: the caller holds a dead capability either
way, and telling the wire which kind of dead tells an attacker whether their
activity was noticed. The distinction is visible in the web UI, to the
owner and to admins.

### Failed redemptions count

A row exists because a valid code was presented. Whether the signer then
answered is a fact about the signer.

### Alert state self-clears, lock state does not **[judgement]**

An `alerted` code whose window drains is a code that had a burst and
stopped — plausibly a deploy that moved a job between hosts. Latching that
state forever would mean the second real incident on that code produces no
alert. A `locked` code is a code a policy decided was compromised; the
window draining afterwards is what a stolen credential looks like once the
thief notices, not evidence of innocence.

### Addresses are masked before counting

See [Normalization](#normalization). Counting raw IPv6 addresses would make
the feature unusable on any dual-stack network, and that failure mode is a
false lock, not a missed detection.

### The detector runs inline, not on the scheduler **[judgement]**

The repo has a job scheduler (`server/service/scheduler.go:22`) and a sweep
that uses it, so a periodic detector was the obvious alternative. It is
rejected because it cannot gate: a sweep running every minute is a minute of
free redemptions after the threshold is crossed, and the whole second
threshold exists to act without waiting. Inline costs one indexed count on a
path that already does several writes and a broker round trip.

### The observed address list in the mail is bounded

Ten, newest first, plus the total. An unbounded list turns a mail about a
code redeemed from a botnet into a mail nobody's client will render.

## Deliberately rejected

**Counting distinct addresses across all of a user's codes.** Broadest
signal, weakest attribution. Nothing to act on without then asking which
code — which is the per-code query anyway.

**Blocking the first redemption from a new address.** A per-code allowlist
learned from first use would be a stronger control and a support burden of a
different order: every legitimate host migration becomes a ticket. The
threshold model degrades gracefully where an allowlist fails closed on
routine operations.

**Reusing `service_enrollment_redeemed` with a flag.** The existing kind is
per-redemption and chatty, and its default is on; an operator who turned it
off to stop the noise would have turned the anomaly alert off with it. A
security alert must not inherit a routine notification's preference row.

**Rate-limiting redemptions instead.** A different control for a different
problem — it bounds volume from one place, and says nothing about a code
being used from many.

## Sequencing

Each step is independently reviewable, and the tree is working after each.

1. **Config.** `RetrievalAnomaly` struct in
   `server/config/types_certificates.go`, defaults in `defaults.yaml`,
   validation, tests. Nothing reads it yet.
2. **Schema.** The two migration pairs (enrollment columns, composite
   index), `model.Enrollment` fields.
3. **The detector, alert-only.** Window query, normalization, exempt
   networks, the `normal <-> alerted` transitions with CAS. Wired into
   `Retrieve` at step 5 of [Where it runs](#where-it-runs). No enforcement
   yet, no mail yet — a structured log line on each transition.
4. **Address-addressed notifications.** `Event.Address`,
   `NewAddressEvent`, the `handle` branch, `Notifier.NotifyAddress`.
   Independently testable with no detector involved.
5. **The two kinds.** Constants, payloads, `Definition`s, six templates.
   Regenerate the reference: `go test ./server/notify/ -update`.
6. **Enforcement.** `lockdown_threshold`, the three actions, the locked
   check at step 2 of [Where it runs](#where-it-runs), the lockdown
   notification.
7. **Admin unlock.** `PATCH /api/admin/enrollments/{id}/unlock` beside the
   existing expire handler (`server/controller/admin.go:107`), OpenAPI
   regeneration, wire types.
8. **UI.** Lock state on `ServiceCodeRow` / `ServiceCodeDetailModal`, the
   unlock control for admins. Follow `frontend/DESIGN.md`.
9. **Docs.** A section in `email-notifications.md` (the generated reference
   table updates itself), the config keys in `configuration.md`, the flow in
   `flows.md`.

Steps 1-3 deliver detection with logs. Steps 4-5 deliver the alerting the
request opens with. Steps 6-8 deliver the second level. Stopping after any
of those three groups leaves something coherent.

## Testing

Per `.claude/rules/test-go.md` — table-driven, colocated, no mock framework.

- **Counting and normalization**: table over (rows in window, prefixes,
  exempt networks) -> expected distinct count. Edge cases: empty
  `source_ip`, a single address repeated, IPv6 addresses inside one `/64`,
  an address exactly on an exempt boundary, a row one nanosecond outside the
  window.
- **State machine**: table over (current state, count, cooldown elapsed,
  last alerted count) -> expected transition and whether a notification is
  emitted. Both no-op edges (`alerted` under cooldown, `locked` seeing a
  higher count) matter.
- **CAS under concurrency**: two goroutines crossing the threshold on one
  enrollment against a shared sqlite handle; exactly one notification.
- **Enforcement**: each of the three actions, asserting for `lock` and
  `expire` that no certificate is produced. `startTestPipeline`
  (`server/service/enrollment_test.go:37`) runs the *real* signer over the
  in-process transport, so "the job was never published" is observable as
  the absence of a `certificates` row for the pre-allocated serial, and as
  the retrieval row never flipping `succeeded`.
- **Locked gate**: a locked enrollment answers `NotFoundError`.
- **Address notifications**: `handle` with an address event sends to that
  address, reads no `users` row, and ignores a disabled preference for the
  same kind.
- **Config validation**: each rule above, plus the two warning cases.
- **Registry and templates**: automatic. `server/notify` fails on an
  undocumented payload field in either direction; `server/mail` fails on a
  missing or unrenderable template.
- **Controller**: unlock returns 404 for an unknown ID (the
  `RowsAffected == 0` lesson already learned at
  `server/controller/admin.go:125-131`), 403 for a non-admin, and is
  idempotent.

Coverage is reported unfiltered; anything genuinely unreachable carries a
`not covered:` comment at the block. Check `.coverage-floors` after.

## Provenance: what was verified and how

Verified at `5d23809` (2026-08-24) by reading the following in full:
`server/model/enrollment.go`, `server/service/enrollment.go`,
`server/service/notification.go`, `server/notify/{notify,event,payloads}.go`,
`server/config/types_certificates.go` (service and lifetime sections),
`server/config/types_mail.go` (fields), `server/mail/sender.go` (types),
`server/controller/admin.go`, `server/model/user.go`, and the
`enrollment_retrievals` and `notification_preferences` blocks of
`server/resources/migrations/postgres/20260101000000_init.up.sql`.

Claims that will drift, and how to re-check each:

| Claim | Re-check |
| --- | --- |
| `Event` addresses recipients only by `users.id` | `grep -n "type Event struct" -A 8 server/notify/event.go` |
| `Outgoing.To` is a plain string | `grep -n "type Outgoing" -A 5 server/mail/sender.go` |
| Groups are never persisted | `server/CLAUDE.md:23`, `server/model/user.go:5-10` |
| The retrieval row precedes the signing publish | `server/service/enrollment.go:148` vs `:185` |
| Only `enrollment_id` is indexed on retrievals | `grep -rn "idx_enrollment_retrievals" server/resources/migrations/` |
| An expired code answers `NotFound` | `server/service/enrollment.go:104-108` |
| Multi-instance is live | `docs/dev/multi-instance-safety-plan.md` header |

## Open questions

1. **Lock/unlock audit trail.** This design keeps lock state on the
   enrollment row plus structured logs. A dedicated `enrollment_lock_events`
   table would record who unlocked what and when, which for a security
   control is arguably not optional. Deferred rather than decided.
2. **`trusted_proxies` correctness becomes load-bearing.** The detector is
   only as good as `g.ClientIP()` (`server/controller/enrollment.go:60`). A
   misconfigured proxy chain collapses every client to one address (no
   detection) or expands one client to many (false locks). Worth a startup
   warning when the anomaly policy is enabled and `http.trusted_proxies`
   looks permissive, and worth a paragraph in `deployment.md`.
3. **Per-service-account detection**, as an additive second layer reading the
   same rows. Named in [Scope](#scope-is-one-enrollment-code) as not
   foreclosed; not designed.
4. **Should the owner be able to unlock their own code?** This design says
   admin only. The counter-argument is that the owner is the person who
   knows whether the new address is theirs, and admin-only turns every false
   positive into a ticket.
