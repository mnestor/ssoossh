# Audit log

An ordered, append-only record of who did what, to whom, and when.

Implemented in `server/service/audit.go`, with the stored shape in
`server/model/audit_event.go`.

## Two sinks, and they are not equals

Every event goes to both:

- **The shipped log** is the archive. A dedicated `type=audit` slog
  destination emits one JSON line per event, unconditionally, for an
  external log system to ship, retain, search and analyse. Real audit
  retention happens there.
- **The database table** is a bounded cache. It exists only to serve the web
  UI's recent-history views, so it is pruned on a schedule and is
  deliberately not searchable beyond the two indexed subject columns.

The consequence to hold onto: the log emit is unconditional because the
table copy is disposable. A deployment that configures no audit log
destination loses the archive, not the audit trail's correctness — but it
should configure one.

```yaml
audit:
  retention: 1440h        # 60 days in the table; zero disables age pruning
  max_rows: 1000000       # safety valve behind retention; zero disables it
  sweep_interval: 1h      # how often the retention sweep runs
  logging:
    filename: /var/log/ssoossh/audit.log
    max_size: 100         # rotation, same keys as logging.* everywhere else
    max_backups: 30
    log_json: true
```

Leave `logging.filename` unset and audit records still reach the general
log; setting it splits them into their own rotating file.

`retention` bounds the UI's history, not the deployment's. A short window
here loses nothing that matters, because the shipped log is the archive.
`max_rows` backs it up so a burst cannot grow the table without bound inside
the retention window; the default is deliberately high, since it exists to
bound pathology rather than to be the operative limit.

## An audit row references nothing that can change

An entry must read the same in five years as it did the day it was written.
A foreign key breaks that: a rename changes what the entry appears to say,
and a deleted row makes it say nothing. So the payload carries literal
snapshots — the actor's subject, username and groups as they were at event
time, and the target's identity likewise — and the row joins to nothing.

The `actor_user_id` and `target_user_id` columns exist solely so the UI can
ask "everything this account did" and "everything done to this account"
without a JSON scan. They are indexed grouping keys, never foreign keys and
never authoritative. If a user row is later deleted or renamed the timeline
still reads correctly from the payloads; the column merely stops matching.

One row serves both timelines. When a SOC analyst disables alice, the
analyst's history shows the disable and alice's page shows who did it and
why, from the same event.

## Mutations and their audit rows commit together

A state change appends its audit row **in the same transaction**, so a
disable without its audit row is unrepresentable. The in-process event bus
is deliberately not the write path: it is non-persistent, so a crash between
"user disabled" and "event consumed" would lose exactly the record an audit
log exists to keep.

View events have no transaction to join. They are a plain insert that must
never fail the read it describes, so an insert failure is logged loudly and
swallowed — the shipped log line is still emitted, and that is the archive.

## Actions

Namespaced strings inside the payload, so the set grows without a migration.
A client must render an unknown action rather than assume the list is
closed.

| Action | Notes |
| --- | --- |
| `auth.login` | Snapshots the groups the identity carried. Since membership is never persisted, this is the only durable record of what access an identity held on a given day. |
| `auth.login_denied` | A disabled account attempting to log in, which otherwise vanishes entirely. |
| `cert.requested`, `cert.approved`, `cert.denied` | |
| `cert.issued` | **Shipped log only.** The UI already has certificate history from the `certificates` table, so a table copy would be duplication; the archive line is the row an incident reviewer joins against target-host sshd logs. |
| `enrollment.code_created`, `.redeemed`, `.expired` | Never carries the enrollment code: it is a bearer credential, and the never-log-sensitive-data rule covers payloads and log lines alike. |
| `enrollment.notification_email_set` | Carries both the old and the new address. Redirecting where an enrollment's notifications go is also the quiet way to stop an account's holders hearing about their own credential, so "who changed it, and to what" is the question this exists to answer. The address is operator-entered configuration in an admin-only log, not a user secret. |
| `enrollment.reassigned` | **No longer emitted.** Group ownership removed enrollment transfer (see [enrollment-group-ownership.md](../proposals/enrollment-group-ownership.md)); the action stays defined so events recorded before that still read back with a name. |
| `user.disabled`, `user.enabled` | |
| `user.auto_disabled` | A system action, so it carries no actor. |
| `admin.user_viewed`, `admin.enrollment_viewed`, `admin.config_viewed`, `admin.audit_viewed` | |

There is no logout event: sessions mostly end by expiry, so an explicit
logout carries too little signal to keep.

Privileged **detail** views are audited; list views are not. Auditing list
pagination writes a row per page of the user directory and says nothing. If
a list query ever needs auditing, record it as one event carrying the search
parameters. `admin.audit_viewed` is one event per visit to the audit feed,
not one per event displayed, which settles the recursion question.

## Required reasons

A reason is **required and server-validated** (non-empty after trimming,
capped at 1000 characters) on the containment and restorative actions:

- `user.disabled` — the motivating case. The next person deciding whether to
  re-enable the account must be able to see why it was disabled.
- `user.enabled` — "cleared with security, SEC-1234" is as valuable to the
  person after that one.
- `enrollment.expired`.

Optional reason fields do not get filled; required ones cost seconds at
action time and are the whole point later. The API refuses the action rather
than recording one that says nothing, and the web UI keeps the confirm
button disabled until a reason is typed.

System-initiated events generate their own reason text, since no human is
present to supply one. View events carry none.

The denormalized columns on `users` (`disabled_at`, `disabled_by_user_id`,
and `disabled_reason`) stay: they render the directory and the re-enable
flow without touching the audit table, and they survive audit pruning. The
audit trail is the history; those columns are the current state.

## Reading it

Both surfaces sit behind the auditor middleware, which admins and SOC
members reach through the role hierarchy:

- **`GET /api/admin/users/:id/audit`** — one user's timeline, as actor or
  target, newest first. This is where the disable reason meets the person
  deciding whether to re-enable, rendered on the admin user detail page.
- **`GET /api/admin/audit`** — the recent-activity feed, in `created_at`
  order, paged, with no search. Searching happens in the external log
  system.

## Existing special-purpose tables

The enrollment retrieval log already existed and already feeds its own UI
surface. It stays as it is; its code path additionally emits general audit
events. Folding it into `audit_events` is a possible later cleanup, not a
prerequisite.

`enrollment_reassignments` is the same shape but frozen: nothing writes to
it now that ownership cannot be transferred. The rows it holds record
transfers that really happened, so the table is kept and still read.
