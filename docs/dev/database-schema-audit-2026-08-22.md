# Database schema audit — 2026-08-22

Scope: the persisted schema only — `server/resources/migrations/{postgres,sqlite}/20260101000000_init.up.sql`,
the `server/model/` structs they back, and the queries in `server/service/`
that depend on their shape. Runs the `db-design` skill's checklist
(normalization, keys, constraints, indexes, types, cascade rules) against
this project's actual scale: a self-hosted, pre-1.0, single-maintainer tool.

Every finding below was verified against current code. Finding 1 was
reproduced empirically against the project's own SQLite driver, not
inferred.

## Schema as it stands

Seven tables, all with a `TEXT` primary key holding a UUID string.

| Table | PK | Foreign keys | Secondary indexes |
|---|---|---|---|
| `users` | `id` | — | `UNIQUE(subject)` |
| `certificates` | `id` | `user_id` → `users.id` (nullable) | `(user_id)` |
| `certificate_requests` | `id` | `user_id` → `users.id` (nullable) | `(status)` |
| `certificate_request_decisions` | `id` | `certificate_request_id` → `certificate_requests.id` | `UNIQUE(certificate_request_id)`, `(subject)`, `(source_ip)` |
| `enrollments` | `id` | `user_id` → `users.id` | `UNIQUE(code)` |
| `host_mappings` | `id` | — | `UNIQUE(hostname)` |
| `server_secrets` | `name` | — | — |

The fundamentals the skill checks for are in good shape and are not
re-litigated below: every table has a primary key, no EAV, no polymorphic
association tables, no dates stored as free-text strings at the model
layer, no raw-SQL concatenation, JSON deliberately kept as portable `TEXT`
rather than dialect-specific `JSON`/`JSONB`, and explicit `golang-migrate`
migrations with a startup version-skew guard rather than `AutoMigrate`.
Column names are also currently in exact parity across the two dialect
files (verified by diff).

## Status

Findings 1, 2, 5, and 7 were fixed on 2026-08-22, in the same pass that
produced this document. Finding 6 is deferred by decision until the first
release. The rest remain open.

| # | Finding | Status |
|---|---|---|
| 1 | SQLite compares timestamps as local-offset strings | **Fixed** — `server/dbtime` |
| 2 | No link from a certificate back to its request | **Fixed** — `certificates.certificate_request_id` |
| 3 | FK actions unspecified; decisions FK vs. pruning | Open |
| 4 | Nothing deletes rows; unbounded list query | Open |
| 5 | No CHECK constraints | **Fixed for the enum columns**; see the note in that section on the pairing constraints, which were deliberately not added |
| 6 | No down migrations | Deferred until the first release |
| 7 | Index coverage does not match query shapes | **Fixed** |
| 8 | Serials not UNIQUE | Open |
| 9 | Nothing enforces dialect parity | Open |
| 10 | Two tables with no readers or writers | Open |

Because the schema is still defined by a single init migration that is
edited in place (the practice this repo already follows for the decisions
table and the `local_*` columns), these changes reach a fresh database only.
**An existing development database must be recreated** — `golang-migrate`
records `20260101000000` as applied and will not re-run it.

## Findings

### 1. High — SQLite compares timestamps as local-offset strings, so every time comparison and sort is lexicographic

**Status: fixed.** `server/dbtime` now normalizes every timestamp GORM
writes to UTC (a plugin for caller-supplied values, `gorm.Config.NowFunc`
for the ones GORM stamps itself), and `ttlCutoff`/`strandedCutoff` return
UTC so the query parameters match. `server/dbtime/dbtime_test.go` carries
the regression tests, including the two scenarios below; both were confirmed
to fail without the fix and pass with it.

This is the only finding here that is a live correctness bug rather than
hardening, and it is a genuine divergence between the two supported
backends.

The SQLite driver (`glebarez/go-sqlite@v1.23.0`) binds `time.Time` by
formatting it to text and calling `bindText`
(`sqlite.go:1135-1138`), using the layout
`"2006-01-02 15:04:05.999999999-07:00"` (`sqlite.go:295-305, 348-355`) —
that is, **wall-clock time with the writer's UTC offset appended**. The
`DATETIME` column declaration gives NUMERIC affinity, which does not
convert a non-numeric string, so the value stays TEXT and every `<`, `>`,
and `ORDER BY` against it is a BINARY-collation string comparison.

Nothing in the write path normalizes to UTC. `time.Now()` is stored
as-is at `server/service/certrequest.go:202,520,616,697,862` and
`server/service/sweep.go:100`; a repo-wide grep finds exactly one `.UTC()`
call in `server/`, in an HTTP header formatter.

Reproduced against the project's own `newTestDB` helper and models:

```
stored: id=t-2h  created_at="2026-08-22T06:00:00-04:00"
stored: id=t-1h  created_at="2026-08-22T07:00:00-04:00"
stored: id=t-0h  created_at="2026-08-22T08:00:00-04:00"

WHERE created_at < <10:30 UTC, bound as UTC>    -> [t-2h t-1h t-0h]   want [t-2h]
WHERE created_at < <same instant, bound as -04> -> [t-2h]             correct
```

The first predicate matched every row, including rows created after the
cutoff, because `"06:00:00-04:00"` sorts before `"10:30:00Z"` on the
leading digit. A second probe with two rows written from different
offsets showed `ORDER BY created_at ASC` returning them in reverse
chronological order.

In steady state this is masked: `CreatedAt` and the cutoffs from
`ttlCutoff()`/`strandedCutoff()` are all `time.Now()` in the same process
zone, so the offset suffixes match and string order happens to equal
chronological order. It stops being masked when the offsets differ, which
happens in three ordinary situations:

- **DST transitions.** On any non-UTC deployment, rows written before the
  shift carry one offset and the sweep's cutoff carries another. In the
  autumn direction a stranded `signing` request is skipped by
  `SweepStrandedRequests` (`server/service/sweep.go:52-56`); in the spring
  direction requests are swept early. Twice a year, every year.
- **A timezone change.** Adding `TZ=UTC` to a container, or moving a host
  between regions, permanently mis-orders every pre-change row against
  every post-change row.
- **Any future `.UTC()` on a bound parameter.** As the probe shows, that
  alone turns the sweep and TTL predicates into "match everything".

`ListForIdentity`'s `ORDER BY issued_at DESC`
(`server/service/certificate.go:54`) is the same string sort, so a user's
certificate history mis-orders across either boundary.

Postgres is unaffected: `TIMESTAMPTZ` stores a true instant and compares
correctly regardless of the offset the value was written with. So the two
backends genuinely disagree about the meaning of `created_at < ?`, and the
SQLite behavior is the wrong one.

**Fix.** Normalize to UTC on write, so every stored string carries a `Z`
suffix and lexicographic order equals chronological order. Verified: with
`.UTC()` applied on write and on the bound parameter, all three probe
cutoffs (expressed in UTC, UTC+4, and UTC-4) return the correct single
row. Prefer a GORM write callback or a serializer over hand-adding `.UTC()`
at each call site, so a new write path cannot silently reintroduce it, and
add a regression test that writes from a `time.FixedZone` and asserts
ordering.

Existing rows need a backfill in the same migration. SQLite can do the
conversion itself — also verified:

```
2026-08-22T09:00:00-04:00        -> 2026-08-22T13:00:00.000Z
2026-08-22T16:00:00+04:00        -> 2026-08-22T12:00:00.000Z
2026-08-22T12:00:00.123456-04:00 -> 2026-08-22T13:00:00.123Z
```

via `strftime('%Y-%m-%dT%H:%M:%fZ', col)`. Note `%f` yields milliseconds,
so sub-millisecond precision is truncated; if that matters, do the backfill
in Go instead.

### 2. Medium — no link from an issued certificate back to the request that authorized it

**Status: fixed.** `certificates.certificate_request_id` now exists in both
dialects, indexed, as a nullable foreign key. `recordCertificate` sets it in
the same switch that resolves the owner, and only when that lookup confirms
the request row exists — pointing a foreign key at a missing row would fail
the insert and lose the audit record, which is the outcome the best-effort
handling there exists to prevent. The owner-less case, which is exactly what
the link is for, does record it.

`certificates` has no `certificate_request_id`. Confirmed: that column and
field exist only on `certificate_request_decisions`.

The audit chain is therefore `certificate_request` → `decision`, and
separately `certificate` → `user`, with no join between the two halves.
There is no query that can answer "which approval produced this
certificate", or its inverse, without matching on unindexed heuristics
(fingerprint, serial, issue time).

This has a concrete failure mode today. `SignedReplyHandler` resolves the
certificate's owner by reading `user_id` off the request, and deliberately
treats a miss as non-fatal — "a missing owner must not fail issuance for a
certificate that is already signed"
(`server/service/signreply.go:130-149`). When that path is taken, the
`certificates` row is written with `user_id = NULL` and, because there is
no request ID either, it is permanently orphaned: it drops out of
`ListForIdentity` and nothing can reattach it. Adding
`certificate_request_id TEXT REFERENCES certificate_requests(id)` makes
that recoverable and closes the chain in both directions.

### 3. Medium — foreign key actions are unspecified, and the decisions FK conflicts with ever pruning requests

Still open from the 2026-08-21 audit; restating with an addition.

None of the four foreign keys declares `ON DELETE` or `ON UPDATE`, so both
dialects default to `NO ACTION`. Note the earlier audit's claim that
"SQLite silently allows orphans" is **not** accurate for this codebase:
`addSqliteDefaultParameters` unconditionally appends `foreign_keys(1)` to
every connection string and rejects any attempt to override it
(`server/bootstrap/db_pocketid.go:127-128, 140`), so SQLite enforces the
constraints too. The two dialects behave the same. The finding is simply
that the intended behavior on user deletion has never been decided and
written down.

The addition: `certificate_request_decisions.certificate_request_id` is a
`NOT NULL` FK onto `certificate_requests`. The decisions table is
documented as permanent and append-only, while `certificate_requests` is
the table finding 4 argues will eventually need retention. Those two
requirements are in direct conflict — pruning a request will either be
blocked by the FK or, if someone reaches for `ON DELETE CASCADE` to unblock
it, will silently delete the audit record that was meant to outlive it.
Decide now: either the decisions table drops the FK and keeps a plain
copied ID (consistent with how it already treats decider identity — copied
values, deliberately no `user_id` FK), or `certificate_requests` is
declared permanent too.

### 4. Medium — nothing ever deletes a row, and the one list query is unbounded

A repo-wide grep for `Delete(` across `server/` returns three hits, all
`sess.Delete(...)` on the in-memory session object in
`server/middleware/session_auth.go`. There is no `DELETE` against any
application table anywhere in the codebase, and no retention job.

This corrects the 2026-08-21 audit, which records `certificate_requests`
rows as "hard-deleted, no audit trail". They are not deleted; they
accumulate. Every table grows without bound: one `certificate_requests`
row per login attempt and one `certificates` row per issuance, forever.

Compounding it, `ListForIdentity` fetches a user's complete certificate
history with no `LIMIT` and no pagination
(`server/service/certificate.go:51-57`). For a daily SSH user that is a
row per login, unbounded, loaded into memory and serialized on every
dashboard view.

Not urgent at current scale, but it is the kind of thing that is cheap to
design now and expensive to retrofit once there is production data to
migrate. The 2026-08-21 audit already sketches the right pagination shape
(cursor-based on `created_at DESC, id DESC`); pair it with a retention
policy per table, and note that whatever that policy is for
`certificate_requests` must be reconciled with finding 3.

### 5. Medium — no CHECK constraints, on the enums or on the type/nullable-column pairing

**Status: fixed for the enum columns.** All four now carry a named CHECK in
both dialects, mirrored as `check:` tags on the model structs so the
AutoMigrate-backed tests build the same constraint (the reasoning already
used for `CertificateRequestDecision`'s `unique` tag). `enums.go` records
that adding an enum value is now a three-place change.

**The type/nullable pairing constraints were deliberately not added.** The
obvious one, `CHECK (type = 'host' OR user_id IS NOT NULL)` on
`certificates`, would break documented behavior rather than protect it:
`recordCertificate` writes `user_id = NULL` on purpose when owner resolution
fails, because the certificate is already signed and delivered by then and
losing the audit row is strictly worse than an owner-less one. The
constraint would convert that tolerated degradation into a hard failure and
lose the record. Finding 2's `certificate_request_id` link is the right fix
for that case, and it is now in place. The equivalent on
`certificate_requests.user_id` is also unsafe — that column is legitimately
null for an unauthenticated initial host-sign ask.

Neither dialect file contains a single `CHECK`.

Three columns are closed enumerations at the Go layer with nothing behind
them at the database layer: `certificates.type` and
`certificate_requests.type` (`user`/`host`/`service`/`pam`),
`certificate_requests.status` (seven values), and
`certificate_request_decisions.outcome` (`approved`/`denied`). The status
column is explicitly relied on as free text — "No migration needed —
status is a free-text TEXT column" (`server/model/enums.go:48`) — which is
a reasonable trade for adding enum values cheaply, but it means a typo in
a status string produces a row that no guarded `WHERE status = ?` update
will ever match again, and it becomes invisible to the sweep.

Separately, several columns are conditionally meaningful by `type`, with
nothing enforcing the pairing: `user_id` is nullable and documented as "nil
for host certs"; `hostname` is set only for host rows; `username` only for
PAM rows; `local_username`/`local_hostname` only for user rows;
`enrollment_token` only for enrolled service rows. A `type = 'user'` row
with `user_id IS NULL` is accepted by the schema, and silently disappears
from that user's history (see finding 2). A partial CHECK — e.g.
`CHECK (type = 'host' OR user_id IS NOT NULL)` on `certificates` — would
turn that into a loud failure at the point of the bad write.

Both dialects support `CHECK`; SQLite has enforced them since 3.3.

### 6. Medium — no down migrations

**Status: deferred by decision** until the project ships its first release,
at which point the schema stops being edited in place and rollback becomes
a real operation rather than a hypothetical one.

`server/resources/migrations/` contains exactly two files, both
`*.up.sql`. No `.down.sql` exists in either tree.

`.claude/rules/database.md` opens with "Always include rollback
instructions". This is a direct, and so far unremarked, violation of the
project's own rule. It has been invisible because there has only ever been
one migration — but the startup path already reasons about downgrades
(`ALLOW_DOWNGRADE`, `server/bootstrap/db.go:117-122`), which is a
rollback story that cannot currently be executed. Write the paired
`.down.sql` files now, while the answer is a trivial set of `DROP TABLE`s,
rather than at the first migration where the rollback is genuinely hard.

### 7. Low — index coverage does not match the actual query shapes

**Status: fixed.** `idx_certificate_requests_status` became
`(status, created_at)` and `idx_certificates_user_id` became
`(user_id, issued_at DESC)`; indexes were added on
`certificate_requests.user_id`, `enrollments.user_id`,
`certificate_request_decisions.decided_at`, and the new
`certificates.certificate_request_id`. Verified against the real migrated
schema, not just the SQL text.

Present but near-useless: `idx_certificate_requests_status` is a
low-cardinality single-column index, and the only query in the codebase
that filters on `status` alone is the sweep's
`status = 'signing' AND created_at < ?` (`server/service/sweep.go:52-55`).
Every other `status` predicate is `id = ? AND status = ?`
(`certrequest.go:535,628,709,864`), a primary-key lookup where the index
is not used. Make it `(status, created_at)` so the sweep's range is
covered too.

Also suboptimal: `idx_certificates_user_id` covers the filter in
`ListForIdentity` but not its `ORDER BY issued_at DESC`, leaving a sort on
every dashboard load. `(user_id, issued_at DESC)` covers both and is the
index a cursor-paginated version (finding 4) will need anyway.

Missing entirely:

- `certificate_requests.user_id` — declared FK, no index. The skill's
  "index every foreign key" rule; also what makes any future
  `ON DELETE`/pre-delete check on `users` a full scan.
- `enrollments.user_id` — same.
- `certificate_request_decisions.decided_at` — "every decision in this
  window" is a first-order audit question and is currently a full scan.
  The table already carries deliberate audit indexes on `subject` and
  `source_ip`; this one belongs with them.

### 8. Low — certificate serials are random and not UNIQUE

`newSerial()` returns 63 bits from `crypto/rand`, masked to stay inside a
signed `BIGINT` (`server/signer/sign.go:50-63`) — so the width is correct
and there is no overflow concern against the `BIGINT`/`INTEGER` column.

But `certificates.serial_number` has no unique constraint, and serials are
the key a KRL revokes by (noted in that function's own comment). A
collision is vanishingly unlikely at 2^63, and that is exactly why a
`UNIQUE` index costs nothing: it converts an astronomically rare event
from "revoking one certificate silently revokes an unrelated second one"
into a failed insert. Cheap insurance on a security-relevant identifier.

### 9. Low — nothing enforces parity between the two dialect files

`.claude/rules/database.md` requires every schema change to land in both
dialect trees, and both migration files carry a header comment saying the
same. They are currently in exact parity — verified by diffing the column
names out of both files.

Nothing keeps them that way. `server/bootstrap/db_test.go` has thorough
coverage of the migration *runner* (22 tests) but none comparing the two
schemas to each other. A test that parses both files and asserts identical
table and column sets would catch the failure mode the rule exists to
prevent, and would have caught it before review rather than after.

### 10. Low — two tables have no readers and no writers

`enrollments` and `host_mappings` are declared, indexed, and foreign-keyed,
and nothing in the codebase touches either. Excluding tests and comments,
`model.Enrollment` has zero references and `model.HostMapping` has one, a
`TODO` at `server/service/host.go:54`.

For `host_mappings` that is straightforwardly "designed, not built", and
matches the host-certificate status recorded in `docs/dev/changes-next.md`.

`enrollments` is more interesting, because the feature it belongs to is
half-built and the two halves disagree. `approveServiceEnrollment` does
**not** create an `Enrollment` row; it writes a UUID into
`certificate_requests.enrollment_token`
(`server/service/certrequest.go:519,536-541`), while
`EnrollmentService.Retrieve` is a stub returning "not implemented"
(`server/service/enrollment.go:39-41`). So the live enrollment state is a
denormalized token on the requests table, carrying none of what
`model.Enrollment` was designed to hold — no `expires_at`, no
`redeemed_at`, no `option_set` snapshot, and no `UNIQUE` on the token.

Worth deciding explicitly when service enrollment resumes: either the
enrollment path starts writing the table it was designed around, or the
table is dropped and `certificate_requests` becomes the documented home
for enrollment state. Leaving both is how the two drift further apart.

## Not flagged / deliberately out of scope

- **`sessions` is outside the migration scheme.** `gormsessions.NewStore`
  `AutoMigrate`s its own table on every startup. Already documented as a
  deliberate, narrow exception at `server/bootstrap/router.go:167-175`,
  with a sound rationale: the table is wholly owned by the library, not by
  `model/`. No action.
- **UUIDs stored as `TEXT` rather than Postgres `uuid`.** Costs ~20 bytes
  per key and gives up the type check. Not worth a migration on its own;
  worth knowing if the ID type is ever revisited.
- **Postgres `TEXT` with no length limits.** The skill flags
  `VARCHAR(MAX)`, but on Postgres `TEXT` and `VARCHAR(n)` are the same
  storage with the same index behavior, so this is not the anti-pattern it
  is on other engines. Input length belongs in validation at the API
  boundary, which is where the 2026-08-21 audit already tracks it
  (`ValidatePrincipal`).
- **Normalization.** The schema is at 3NF. The two intentional
  denormalizations — comma-joined `principals` and the JSON-in-`TEXT`
  columns — are each documented with a rationale and a `TODO` naming the
  join table to introduce if querying by element becomes necessary. That
  is the right call at this scale.
- **`certificate_request_decisions` duplicating identity fields from
  `users`.** Looks like a 3NF violation and is not: it is a deliberate
  point-in-time snapshot so a historical decision cannot be rewritten by a
  later change to the users table, reasoned out in the model's doc comment.
  Correct as designed.

## Remaining work, in order

1. Findings 3 and 4 together — both are "decide the retention story", and
   the FK question cannot be answered without it. This is the next real
   decision; everything else left is small.
2. Finding 8 — one line, additive.
3. Finding 9 — a test, independent of the rest.
4. Finding 6 — when the first release lands, per the decision recorded above.
5. Finding 10 — defer until service enrollment resumes.

No backfill was needed for finding 1: the init migration is still edited in
place, so it only ever runs against a fresh database. That stops being true
at the first release, which is the same boundary finding 6 is waiting on.
