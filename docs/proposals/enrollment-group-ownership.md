# Group ownership of service enrollments

**Status: implemented** on `feat/enrollment-group-ownership` (2026-08-29).
This document is the design and the reasoning behind it; the code is the
authority on detail, and the `file:line` anchors below were written against
the pre-change tree and will drift.

What shipped differs from the proposal in two places, both noted inline:
notification delivery landed as fan-out only (the per-enrollment address
belongs to [notification-kinds-expansion.md](notification-kinds-expansion.md)
and is not built), and the service codes page gained an
[approved-by line](#the-service-codes-page) on every row, which the
account-first listing made necessary.

Related documents:

- [notification-kinds-expansion.md](notification-kinds-expansion.md) is
  amended alongside this proposal: group ownership removes one of its
  kinds and changes its delivery model. The edits are part of this
  proposal's scope and are described in
  [Notification delivery](#notification-delivery) below.
- The admin-console decisions on enrollment reassignment and the
  disabled-user grace period are superseded by this document. The other
  admin-console decisions (paging, cert deep links, the disable action
  itself) stand.

## What this proposes

A service enrollment stops having a single owning user. It is owned,
collectively and automatically, by every user who holds the service
account it was approved for. Ownership is **derived from service-account
membership, never stored and never moved**, which makes two shipped
features unnecessary:

1. **Reassignment is removed entirely.** The `PATCH
   /api/admin/enrollments/:id/reassign` route, `EnrollmentService.Reassign`,
   the eligibility check, and the reassign UI all go. There is nothing
   left to transfer: whoever holds the account already owns the
   enrollment.
2. **The disabled-user enrollment expiry sweep is removed.** An
   enrollment belongs to the service account, not to the person who
   clicked approve, so disabling that person no longer strands or
   expires it. The other holders keep full visibility and control.

`enrollments.user_id` stays, demoted to pure audit detail: who approved
this enrollment. It no longer grants or implies anything.

## The model today

`enrollments.user_id` is set at approval to the approving user
(`server/model/enrollment.go:30`) and is the sole ownership fact. It
gates the three non-admin read paths:

- `ListForIdentity` (`server/service/enrollment.go:395`): "my service
  codes" is literally `WHERE user_id = ?`.
- `ListRetrievals` (`server/service/enrollment.go:609`): approver or
  auditor.
- `GetEnrollmentDetail` (`server/service/enrollment.go:838`): approver
  or auditor.

It also addresses both shipped notifications (created at
`server/service/certrequest.go:951`, redeemed at
`server/service/enrollment.go:280`) and is the target of the redemption
audit event (`server/service/enrollment.go:680`).

Because a single owner can leave, two escape hatches exist:
reassignment (`Reassign`, `enrollment_reassignments`,
`AuditEnrollmentReassigned`) and the disabled-user sweep
(`registerDisabledUserEnrollmentSweepJob`,
`server/bootstrap/scheduler.go:242`, driven by
`admin.disable_grace_period`, `server/config/types.go:327`). Both exist
only to compensate for ownership being pinned to one person; both are
removed by this proposal.

## The ownership predicate

A service enrollment carries exactly one principal, fixed at approval
time to a service account the approver holds
(`server/service/certrequest.go:814`: `principals :=
[]string{serviceAccount}`, drawn from `identity.ServiceAccounts`). The
new rule is:

> A user owns an enrollment iff the enrollment's service account is in
> that user's service accounts.

This predicate already exists in the codebase as
`isEligibleForReassignment` (`server/service/enrollment.go:904`), where
it decides who may *receive* a transfer. The proposal promotes it to
deciding who owns the enrollment at all times, at which point the
transfer it guarded is meaningless.

Membership has two sources, used for two different jobs:

- **The acting user's session.** `Identity.ServiceAccounts` rides the
  session (the settled session-carries-account-lists decision), so every
  authorization check against the requesting user reads the session
  identity, no extra query. Access reflects the accounts held at login,
  same staleness contract as everything else on the session.
- **The user rows, for fan-out and display.** `users.service_accounts`
  (JSON-encoded `[]string`, `server/model/user.go:21`) is written at
  OIDC login (`server/service/auth.go:327`) and nowhere else; LDAP sync
  does not touch it. Anything that needs "all holders" without a session
  in hand (notification delivery, admin display) resolves against these
  rows and therefore sees each user's accounts as of their last login.
  That staleness is accepted and documented, not worked around.

### A `service_account` column

The enrollment's service account currently lives as the single element
of the `principals` JSON column. Deriving ownership in SQL from inside a
JSON string, portably across sqlite and postgres, is exactly the kind of
query that goes subtly wrong. So the migration adds a plain
`service_account` text column on `enrollments`, backfilled from
`principals[0]`, written at approval alongside it. `principals` is
unchanged and remains what certificates are minted from; the new column
exists for `WHERE service_account IN (?)` and joins.

## Authorization changes, site by site

- **`ListForIdentity`** becomes "enrollments for any service account I
  hold": `WHERE service_account IN (?)` with the session's
  `ServiceAccounts`. An identity holding no service accounts gets an
  empty list. This is a genuine semantic change to the service codes
  page: users will see codes they did not approve. The row already
  renders the approver; that stays, as provenance rather than
  ownership. See [The service codes page](#the-service-codes-page) for
  the presentation.
- **`ListRetrievals` and `GetEnrollmentDetail`**: the `user.ID ==
  enrollment.UserID` check is replaced by `slices.Contains(
  identity.ServiceAccounts, enrollment.ServiceAccount)`. No users-table
  lookup needed for the check itself. Auditor access is unchanged.
- **Admin surfaces** (`ListForAdmin`, admin detail): unchanged in scope
  (auditor-gated), but the "owner" column reads as "approved by".
- **Audit events**: redemption events currently target the owner
  (`AuditSubject{UserID: enrollment.UserID}`). They keep targeting
  `user_id`, now meaning the approver, and gain the service account in
  `Detail` so the account-centric view is queryable. No schema change.

## Removals

**Reassignment.** Delete `Reassign`, `authorizeReassignment`,
`isEligibleForReassignment`, the `PATCH
/api/admin/enrollments/:id/reassign` route and handler
(`server/controller/admin.go:59`, `:1221`), the reassign controls in
`ServiceCodeDetailModal` and `AdminServiceCodeDetailModal`, and the
`service_enrollment_reassigned` kind from the notification proposal.
Stop emitting `AuditEnrollmentReassigned`; the constant stays so
existing audit rows still render.

The `enrollment_reassignments` table is **kept, read-only**. It is an
append-only audit record of transfers that really happened; dropping it
would erase history, and keeping it costs one table. No new rows are
ever written. `AdminEnrollmentDetail` still returns the rows, so a
historical display remains possible; it simply never grows.

**The disabled-user sweep.** Delete
`registerDisabledUserEnrollmentSweepJob`,
`SweepDisabledUserEnrollments`, and the `admin.disable_grace_period`
config key (`server/config/types.go:322`), plus the admin-UI copy that
names the grace-period consequence when disabling a user. Disabling a
user now means what it says and nothing more: they lose access. The
enrollments they approved keep working and remain visible to every
other holder of the account. An admin who wants an enrollment gone
expires it directly, deliberately, as its own action.

The removal is safe because of group ownership itself: the scenario the
grace period existed for — the sole owner is gone, and the credential is
now unattended in the strong sense — cannot arise when every holder of the
account is an owner. The residual case, an account whose holders have *all*
gone, is covered by the per-enrollment notification address rather than by
expiring the code out from under a job that is still running.

**As built.** `EnrollmentProvider` lost its `Reassign` method,
`EnrollmentService` lost the three functions and the two audit helpers only
they used, and the route, handler, API client function, and both modals'
controls are gone. `AuditEnrollmentReassigned` also came off
`reasonRequired`, since nothing can supply a reason for an action nothing
emits. The disable path lost `GracePeriodSeconds` and `ExpireAtTimestamp`
from `DisableUserConsequences`, keeping `ServiceEnrollmentCount` — the
confirmation dialog now uses that number to say what the disable *leaves
alone*, which is the inverse of what it used to say and the thing an admin
disabling a colleague actually needs to know.

## Notification delivery

Group ownership breaks the notification model's core assumption: there
is no longer one owner whose email is the recipient and whose
preference is the gate. The amended rule, folded into
[notification-kinds-expansion.md](notification-kinds-expansion.md):

- **Enrollment-scoped notifications fan out to every current holder** of
  the service account, resolved at delivery time from
  `users.service_accounts`, each copy gated by that holder's own
  per-kind preference, each skipped individually when a holder has no
  email or is disabled. One `NotifyServiceAccount` call publishes one
  event; the delivery consumer resolves it to N sends.
- **When the enrollment has a `notification_email`, that address is the
  sole recipient**, sent ungated. With no single owner there is no
  principled person whose preference could gate it; the address is the
  account's subscription, entered deliberately at approval or edit
  time. *Not built here* — the column belongs to
  [notification-kinds-expansion.md](notification-kinds-expansion.md), and
  fan-out is what group ownership itself required.
- User-scoped kinds (the issued pair) are untouched.

**As built.** `notify.Event` grew a second addressing form
(`ServiceAccount` beside `UserID`, exactly one set) and `Notifier` a second
method, so the choice is explicit at each emit site rather than inferred.
`NotificationService.ServiceAccountRecipients` is the resolver, alongside
the `GroupRecipients` it is modelled on. Matching a name inside a JSON
column has no portable SQL form, so it narrows with a `LIKE` on the quoted
name and confirms by decoding each candidate: exact, and without pulling
every user row into Go on every redemption. It is still an unindexed scan,
and a deployment with a very large user table and a very hot redemption
loop would want what `user_groups` got — rows instead of JSON.

A fan-out that fails partway is retried whole, so a holder already reached
can get a second copy. That is the at-least-once contract the
single-recipient path always had, and the failure it exists for (a relay
that is down) is total rather than per-recipient.

Fan-out reaches only users the server knows, that is, holders who have
logged in at least once, with accounts as of their last login. A
service account held entirely by people who never used ssoossh notifies
nobody; the notification address exists for exactly that deployment.

## The service codes page

Decided: the page is a three-level drill-down, account first.

1. **Service accounts.** The top level lists every service account the
   session identity holds, one entry each, including accounts with no
   enrollments at all: an account you hold with no live code is exactly
   the unattended job about to break silently, and a zero state is the
   page saying so. Each entry summarizes what is behind it (live codes,
   soonest expiry, last retrieval).
2. **Codes for one account.** Selecting an account opens its
   enrollments, keeping today's live/dead split (`+page.svelte` splits
   on expiry now; that logic moves down one level unchanged). Each row
   shows the approver as provenance, expiry, and retrieval count.
3. **One code's detail.** Selecting a code opens the existing
   `ServiceCodeDetailModal` content: fingerprint, options, the
   retrieval log (capped at `RetrievalPageSize` with the total shown),
   and, once the notification proposal lands, the notification address
   editor. The reassignment controls are gone from it; historical
   reassignments render only in the admin detail.

Whether levels one and two are routes or in-page expansion is an
implementation choice; the hierarchy is not. `ListForIdentity` already
returns per-enrollment retrieval counts and last-retrieval times, so
the level-one summaries aggregate from the same response and no new
endpoint is needed.

**As built.** Both levels are query parameters on the one page
(`?account=`, then the pre-existing `?modal=`), shallow-routed like the
certificate history's modal, so every level is linkable. The accounts the
identity holds come from `GET /api/users/me`, unioned with the accounts
actually on the codes — an account that has left the claim keeps its codes
visible rather than stranding a page that can still open them.

Two additions the account-first listing forced:

- `ServiceEnrollmentResponse` carries `service_account` explicitly rather
  than leaving the page to read `principals[0]`, because grouping by it is
  now the page's whole structure.
- Every row names **who approved it** (`approved_by_username`, joined in
  `ListForIdentity`). A list of codes you did not necessarily create is
  unreadable without it, and it is the honest replacement for the ownership
  the column used to imply.

## Migration

One migration, both dialects
(`20260829030000_enrollment_group_ownership`):

1. `ALTER TABLE enrollments ADD COLUMN service_account TEXT NOT NULL
   DEFAULT ''`, then backfill from `principals[0]`. The column is
   authoritative after this point, and it is indexed — the ownership
   query reads it on every service codes page load.
2. No change to `user_id`, no change to `enrollment_reassignments`.
3. Config: `admin.disable_grace_period` removed.

Down migration drops the index and the column. `principals` still holds
the account, so a rollback loses nothing.

**As built — the backfill differs per dialect, deliberately.** Neither
statement may fail on a row whose `principals` never parsed (those exist:
`EnrollmentService.Retrieve` has a branch for them), and the two engines
fail differently:

- SQLite guards with `json_valid(principals) AND
  json_array_length(principals) > 0`, because `json_extract` *raises* on
  malformed input rather than returning NULL.
- Postgres extracts by pattern, `substring(principals from
  '^\["([a-zA-Z0-9._-]+)"')`, because `principals::json` raises on a bad
  row and Postgres has no try-cast to guard it with (`IS JSON` needs 16+,
  below the versions supported). The pattern is exact rather than a
  heuristic: a principal is `[a-zA-Z0-9._-]+`
  (`internal/crypto/ssh.ValidatePrincipal`), so `json.Marshal` always
  produces literally `["name"]` with nothing to escape.

Both leave `''` on a row they cannot read, which matches no account and so
is owned by nobody — visible to auditors, to no one else.
`test/migration/backfill_test.go` pins all of it, malformed rows included.

Rollout is ordinary: the column is added and populated by the migration
before any code reads it, and the reads that replace `user_id` are in the
same release.

## Edge cases

- **Losing an account.** A user removed from a service account at the
  IdP keeps seeing its enrollments until their session identity
  refreshes at next login. Same contract as group-based auditor access
  today; documented, not fixed here.
- **Empty holder set.** Every holder disabled or departed: the
  enrollment keeps working (codes are bearer credentials; that is true
  today too), stays visible to auditors, and notifies only the
  notification address if set. This is the remaining argument for
  setting one, and the approval-page field's help text should say so.
- **The approver loses the account.** They approved it, but no longer
  hold the account: they lose visibility too. `user_id` grants nothing.

## Open questions

- **Rate of fan-out.** A popular service account with dozens of holders
  makes every redemption notification a small mailshot, and
  `service_enrollment_redeemed` fires on *every* redemption by default.
  The per-kind preference mitigates it bluntly (all or nothing), and the
  notification address will mitigate it properly. If that is not enough
  in practice, a per-user "mute this account" is cheap to add later and
  belongs to the preferences surface.
- **Holder resolution cost.** `ServiceAccountRecipients` scans `users`
  with a `LIKE`, once per enrollment-scoped notification. It runs on the
  broker's goroutine, off every request path, so it delays nothing a
  caller waits on — but it is unindexed. The escalation, if a deployment
  ever needs it, is a `user_service_accounts` join table written at
  login, exactly as `user_groups` already does for groups.

## Follow-on work this leaves open

- The **per-enrollment notification address**
  ([notification-kinds-expansion.md](notification-kinds-expansion.md)),
  which is what covers a service account whose holders have all left or
  have never logged in. Until it lands, such an account notifies nobody.
- **`enrollment.reassigned` in the audit UI.** The events and the
  `enrollment_reassignments` rows are still returned; no surface renders
  the history. That is a display gap, not a data loss.
