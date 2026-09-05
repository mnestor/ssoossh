# Audit log

**Status: implemented.** Built 2026-08-29. The operator-facing reference is
[Audit log](https://mnestor.github.io/ssoossh/operations/audit-log/), which is now the
document to read; this one is kept for the reasoning behind each decision.

**What shipped:** all eight numbered items below, plus the required-reason
set and both UI surfaces. Specifics worth knowing:

- The `audit_events` table, the `type=audit` slog destination, the scheduled
  retention sweep (`audit.retention`, `audit.max_rows`), the taxonomy, and
  the two auditor-scoped read endpoints all landed as designed.
- `cert.issued` goes to the shipped log only, as specified.
- Required reasons are enforced on `user.disabled`, `user.enabled`,
  `enrollment.expired` and `enrollment.reassigned`. The wire types went from
  optional to required, and the web UI gates each confirm button until a
  reason is typed.
- `users.disabled_reason` was added and is surfaced on the admin user detail
  page, which is where it meets the person deciding whether to re-enable.

**Deviations from this document, and why:**

- **`Reassign` gained a reason parameter**, which changed
  `service.EnrollmentProvider`. Unavoidable: the reason is required and the
  service performs the write.
- **Reassignment became transactional.** It previously performed the
  ownership change and its record as two separate statements, so a failure
  between them left an enrollment reassigned with nothing saying by whom.
  Adding a third write that must commit atomically made fixing it the
  smaller change.
- **The disabled-user enrollment sweep now reads before it writes**, so each
  expiry can be audited individually rather than as one bulk UPDATE that
  cannot say which enrollments it touched.
- **`user.auto_disabled` has no producer yet.** It is defined in the
  taxonomy and rendered by the UI, but nothing in the server disables a user
  automatically today; the LDAP directory sync is what will emit it.
- **A pre-existing leak was fixed alongside:** `ldap.logging`'s rotating
  file handle was never released at shutdown, because the LDAP named logger
  had no close function. Audit's addition made the omission visible.

Administrative actions in ssoossh leave almost no durable trace. Disabling a
user records who and when (`users.disabled_at`, `users.disabled_by_user_id`)
but silently discards the reason the API already accepts
(`webtypes.DisableUserRequestBody.Reason` is bound in
`server/controller/admin.go` and never read). Re-enabling records nothing.
Expiring an enrollment records nothing. A denied login by a disabled user
vanishes entirely. The next admin who opens a disabled account has no way to
learn why it was disabled, and an incident reviewer has no ordered record of
who did what.

This proposes a single append-only audit event stream with two sinks: a
small database table that feeds the web UI, and a dedicated slog destination
whose output an external log system ships and archives (we do not care how).

## What this proposes

1. One `audit_events` table: `id`, `created_at`, `actor_user_id`,
   `target_user_id`, and a JSON payload that is the actual record.
2. Every event is a self-contained snapshot. No foreign keys; identity data
   is copied into the payload at event time.
3. A namespaced action taxonomy covering user actions, privileged
   admin/SOC/auditor actions, privileged detail *views*, system actions,
   and authentication events — login and certificate issuance included.
4. A required, server-validated `reason` on the containment and restorative
   actions (disable, enable, expire, reassign).
5. Mutation events are written in the same database transaction as the
   state change they describe.
6. A `type=audit` slog destination (the existing `GenericLogging` pattern)
   emits one JSON line per event as the durable export.
7. The table holds recent events only; a scheduled sweep prunes it. The
   shipped log is the archive.
8. UI surfaces: a per-user timeline on the admin user detail page and a
   recent-activity feed, both auditor-scoped reads.

## Design principles

### An audit row references nothing that can change

An audit entry must read the same in five years as it did the day it was
written. A `user_id` foreign key breaks that: a rename changes what the
entry appears to say, a deleted row makes it say nothing. So the payload
carries literal snapshots — the actor's subject, username, and groups as
they were at event time; the target's identity likewise — and the row joins
to nothing.

This is consistent with how the codebase already treats identity: group
membership is never persisted precisely because claims drift between logins
(`server/model/user.go`, `https://mnestor.github.io/ssoossh/internals/invariants/`). An `auth.login`
event that snapshots groups-at-login is in fact the only durable record of
what access an identity carried on a given day, which is part of why login
belongs in the taxonomy.

### The actor/target columns are grouping keys, not references

The two user-id columns exist solely so the UI can ask "everything this
account did" and "everything done to this account" without a JSON scan
across both sqlite and postgres. They are indexed, nullable, and accepted
as drift-prone conveniences: if the user row is later deleted or renamed,
the timeline still reads correctly from the payloads; the column merely
stops matching. They are never foreign keys and never authoritative — the
payload is.

One row serves both timelines. When a SOC analyst disables alice, the
analyst's history shows "you disabled alice" and alice's page shows
"disabled by soc-bob, reason: …" from the same event.

### The external system is the archive; the table is a cache

Real audit retention, search, and analysis happen in whatever external log
system the deployment ships to. The database copy exists only to serve the
UI's recent-history views, so it is bounded (pruned on a schedule) and
deliberately not searchable beyond the two indexed columns. This removes
any need to design database retention, partitioning, or compliance
semantics here. The consequence to honor: the slog emit is unconditional
and happens for every event, because the table copy is disposable.

### Mutations and their audit rows commit together

The event bus is tempting as a write path, but the in-process gochannel is
non-persistent: a crash between "user disabled" and "event consumed" loses
the audit row, which is the one failure mode an audit log exists to
prevent. So services append the event in the same transaction as the state
change — a disable without its audit row becomes unrepresentable — and emit
the log line best-effort after commit. View events have no transaction;
they are a plain insert that must not fail the read it describes (log
loudly on insert failure instead).

## Schema

```sql
CREATE TABLE audit_events (
    id             TEXT PRIMARY KEY,
    created_at     TIMESTAMP NOT NULL,
    actor_user_id  TEXT NULL,   -- who did it; NULL for system/anonymous
    target_user_id TEXT NULL,   -- who it was done to; NULL when nobody
    payload        TEXT NOT NULL -- JSON; the actual record
);
CREATE INDEX idx_audit_events_actor  ON audit_events (actor_user_id, created_at);
CREATE INDEX idx_audit_events_target ON audit_events (target_user_id, created_at);
```

The payload carries at minimum: `v` (payload schema version, `1` from day
one, so a future shape change never has to guess what it is reading),
`action`, `occurred_at`, `actor` (snapshot object or `"system"` / absent),
`target` (snapshot object, when there is one), `reason` (where
applicable), and per-action detail (certificate
serial, key ID, principals, enrollment ID, source IP, search parameters for
audited queries). Never the enrollment code or any other secret — the
never-log-sensitive-data rule applies to payloads and log lines alike.

Column semantics by example:

| Event | actor_user_id | target_user_id |
| --- | --- | --- |
| SOC disables alice | analyst | alice |
| Alice approves a cert | alice | NULL |
| Scheduler autodisable/sweep hits alice | NULL | alice |
| Anonymous code redemption | NULL | enrollment owner |
| Admin views effective config | admin | NULL |

## Action taxonomy

Namespaced strings inside the payload, so the set grows without
migrations. Initial set:

- `auth.login`, `auth.login_denied` (disabled account attempting login).
  No logout event: sessions mostly end by expiry, so an explicit logout
  carries too little signal to keep.
- `cert.requested`, `cert.approved`, `cert.denied`, `cert.issued`
  (serial, key ID, principals, type, expiry — the row an incident reviewer
  joins against target-host sshd logs). `cert.issued` goes to the shipped
  log only, not the table: the UI already has certificate history from the
  `certificates` table, so a table copy would be pure duplication; the
  archive line is the valuable part.
- `enrollment.code_created`, `enrollment.redeemed`, `enrollment.expired`,
  `enrollment.reassigned`
- `user.disabled`, `user.enabled`, `user.auto_disabled` (system actor)
- `admin.user_viewed`, `admin.enrollment_viewed`, `admin.config_viewed`,
  `admin.audit_viewed`

Privileged **detail** views are audited; list views are not. Auditing list
pagination writes a row per page of the user directory and says nothing;
if a list query ever needs auditing, record it as one event carrying the
search parameters. `admin.audit_viewed` is one event per visit to the
audit feed, not per event displayed — that settles the recursion question.

## Required reasons

`reason` is required and server-validated (non-empty, length-capped) on:

- `user.disabled` — the motivating case: the next person enabling the
  account must see why it was disabled
- `user.enabled` — "cleared with security, SEC-1234" is as valuable to the
  person after that
- `enrollment.expired`, `enrollment.reassigned`

The wire types already carry optional `reason` fields on disable/enable
(`webtypes.DisableUserRequestBody`, `webtypes.ReEnableUserRequestBody`);
this makes them required and actually persisted. Optional reason fields do
not get filled; required ones cost seconds at action time and are the whole
point later. System events generate their own reason text ("owner disabled
168h ago, grace period elapsed"). View events carry none.

The denormalized current-state columns on `users` (`disabled_at`,
`disabled_by_user_id`, plus a new `disabled_reason`) stay: they render the
directory and the enable flow without touching the audit table, and they
survive audit pruning. The audit trail is the history; the `users` columns
are the current state.

## Sinks

**Database:** the table above, written as described. A scheduled sweep
(alongside the existing jobs in `server/bootstrap/scheduler.go`, e.g.
`SweepDisabledUserEnrollments`) deletes rows older than a configurable
window, `audit.retention` defaulting to 60 days — the table is "most
recent events," the shipped log is the archive. A max-row cap
(`audit.max_rows`, default 1,000,000) backs the window up as a safety
valve on chatty deployments: the same sweep deletes oldest-first past the
cap, so a burst of events cannot grow the table without bound inside the
retention window. The default is deliberately high — the cap exists to
bound pathology, not to be the operative limit.

**Shipped log:** a dedicated slog destination using the `GenericLogging`
pattern the LDAP config already uses (`server/config/types.go`), routed by
a `type=audit` attribute, configured under `audit.logging`. GenericLogging
embeds a `DeRuina/timberjack` logger, so the audit file rotates itself
with the same knobs as the LDAP and access logs — no new rotation
machinery. One JSON line per event, same fields as the payload plus the
two subject IDs, so the two sinks cannot drift — both are fed from the
same event struct (with the noted exception that `cert.issued` is emitted
here and skipped in the table). Deployments that do not configure it lose
nothing but the archive.

## UI

Both surfaces sit behind the existing auditor middleware (which admins and
SOC reach through the role hierarchy):

- **User detail page timeline:** events where the user is actor or target,
  newest first, paged. This is where the disable reason meets the person
  deciding whether to enable.
- **Recent-activity feed:** `GET /api/admin/audit`, the table in
  `created_at` order, paged, no search — the external system is where
  searching happens.

## Existing special-purpose audit tables

`enrollment_reassignments` (from/to/by/when) and the enrollment retrieval
log already exist and already feed UI surfaces. They stay as they are;
their code paths additionally emit general audit events. Subsuming them
into `audit_events` is a possible later cleanup, not a prerequisite.

