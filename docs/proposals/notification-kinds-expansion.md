# Expanding the notification catalogue

**Status: implemented.** Built 2026-08-29. The operator-facing reference is
[operations/email-notifications.md](../operations/email-notifications.md),
which is now the document to read; this one is kept for the reasoning behind
each decision.

**What shipped:** all four kinds, the `notification_email` column and both
send-once claims (migration `20260829040000_notification_expansion`), the
sweep job, and the three surfaces that set the address — the browser
approval page, the service codes page, and the admin enrollment view.
Specifics worth knowing, each noted inline below where it differs from the
proposal:

- The address travels as an **enrollment-addressed event**, not as a branch
  on the account-addressed one. `notify.Event` gained an `EnrollmentID`, and
  `Notifier.NotifyEnrollment` replaced `NotifyServiceAccount` at every
  enrollment-scoped emit site — including the two that already existed — so
  setting an address redirects *all* of an enrollment's notifications rather
  than only the new ones.
- `notification_email` is `TEXT NOT NULL DEFAULT ''` rather than nullable,
  matching `service_account` beside it. An email address is never
  legitimately empty, so `''` is an unambiguous "unset" that needs no
  three-valued logic in the delivery branch.
- The expired-attempt window is a config key
  (`mail.expired_attempt_window`, default 24h) rather than the hardcoded
  suggestion below, on the same reasoning as `mail.expiry_reminder_lead`:
  the right value depends on how the deployment's jobs retry.
- The reminder sweep excludes already-expired codes and bounds each pass to
  500 claims, so the first sweep after an upgrade does not try to remind
  every enrollment inside the window at once.
- The issued pair share one `CertificateIssued` payload struct across two
  registered kinds, since they describe the same object; the kinds stay
  separate so the preferences can be.

Amended by
[enrollment-group-ownership.md](enrollment-group-ownership.md), which
removed one kind originally proposed here
(`service_enrollment_reassigned`) and replaced the single-recipient
delivery model.

The four kinds, in the order they earn their keep:

| Kind | Fires when | Default |
|---|---|---|
| `service_enrollment_expiring` | An enrollment code is within the reminder window of `ExpiresAt` (since revised from send-once to weekly, then daily over the final week) | on |
| `service_enrollment_expired_attempt` | `service retrieve` presents a code that has expired | on |
| `user_certificate_issued` | An interactive user certificate is signed | off |
| `pam_certificate_issued` | A PAM certificate is signed for a `sudo`/`su` | off |
| `console_certificate_issued` | A console certificate is signed for a code-approved login (added with the console type, after this proposal) | off |

Everything here follows the existing four-step "adding a notification
kind" recipe (docs/email-notifications.md): a `Kind` constant, a payload
struct, a registry `Definition`, and templates. The sections below cover
only what each kind needs *beyond* that recipe, plus the one change to
delivery itself.

## A per-enrollment notification address

Under group ownership, an enrollment-scoped notification with no
per-enrollment address fans out to every current holder of the service
account. That is the right default, but it still leaves two gaps the
address exists to close:

- A service account whose holders have never logged in (a deployment
  run entirely by people outside ssoossh) has an empty holder set at
  delivery time, so fan-out reaches nobody.
- An identity provider that releases no email claim silences every
  holder's copy, including the ones about unattended jobs that will
  break. And a large holder set turns every redemption into a
  mailshot; a team alias is the quieter, more deliberate channel.

The fix is an optional notification address on the enrollment itself:

- A nullable `notification_email` column on `enrollments`. `NULL` means
  fan-out to all holders; a value makes that address the sole
  recipient. No backfill needed for existing rows.
- **Set at approval time.** The browser approval page (flow 4b) gains an
  optional field, defaulting to empty. This is the moment the approver
  is already deciding what the enrollment is for; a team alias entered
  here means every subsequent notification about the enrollment — the
  created message, redemptions, the expiry reminder — reaches the people
  who run the job, not just the person who clicked approve.
- **Editable after the fact.** Any holder of the service account edits
  it from the service codes page; an admin edits it from the admin
  console's enrollment view.

### Delivery semantics

Group ownership means there is no single owner to address or to gate,
so recipient resolution moves entirely to the delivery consumer. One
`Notify` call still publishes one event, carrying the enrollment ID;
the consumer resolves it to its sends:

- **Address set:** that address is the sole recipient, sent ungated.
  With no single owner there is no principled person whose preference
  could gate it; the address is the account's subscription, entered
  deliberately at approval or edit time.
- **Address unset:** one copy per current holder of the enrollment's
  service account, resolved at delivery time from
  `users.service_accounts`, each copy gated by that holder's own
  per-kind preference, each skipped individually when a holder has no
  email. Holders are known as of their last login; a holder the server
  has never seen is not reached, which is the address's job to cover.
- User-scoped kinds (the issued pair below) are untouched: recipient is
  the identity email, preference is that user's own.

### What does not change

The enrollment code never appears in any message. The existing pair of
tests — code absent from every rendered message, code absent from every
payload — extends to each new kind below, so the invariant stays a
decision rather than an accident.

An address typed into a form is user-supplied input: validate the
format, and store it as-is otherwise. Whether to restrict the domain
(e.g. a `mail.notification_address_domains` allowlist, so an
authenticated user cannot point server mail at arbitrary third parties)
is an open question below; the proposal's default is no restriction,
documented.

## `service_enrollment_expiring`

The "created" message already tells the recipient to re-enroll before
`CodeExpiresAt` to keep an unattended job running. Nothing follows up,
and the follow-up is the entire value: by the time the date matters, the
terminal that displayed the code is long gone and the cron job is the
only thing that remembers the enrollment exists — by failing.

This is the one kind that cannot be emitted from an event path, because
the event is the *absence* of one. It rides the existing scheduler:

- A sweep job registered in `bootstrap/scheduler.go` alongside the
  stranded-request and CA-key sweeps, selecting enrollments with
  `expires_at` inside the reminder window and no reminder claimed.
- The window comes from config (`mail.expiry_reminder_lead`, suggested
  default 7 days; `0` disables the job). Read at startup like the other
  sweep intervals.

### Send-once across instances

Every instance runs the sweep, and the delivery queue group only
deduplicates *consumption*, not *publication* — two instances sweeping
the same row would queue two events and the group would faithfully send
both. The claim therefore lives in the database: an
`expiry_reminder_sent_at` column on `enrollments`, taken with a guarded
update —

```
UPDATE enrollments SET expiry_reminder_sent_at = now
WHERE id = ? AND expiry_reminder_sent_at IS NULL
```

— and the event published only when the update reports one row.
Claim-then-publish means a crash between the two loses the reminder
rather than duplicating it; for a reminder, at-most-once is the right
side to fail on.

### Interaction with expiry changes

The disabled-user grace period is gone
([enrollment-group-ownership.md](enrollment-group-ownership.md)):
disabling a user no longer moves any enrollment's `expires_at`. But the
rule it motivated is worth keeping as an invariant, because an admin
expiring an enrollment directly still moves the date: **any path that
moves `expires_at` earlier must clear `expiry_reminder_sent_at`**, or a
reminder already sent for the old horizon suppresses the one that
matters for the new, closer one.

## `service_enrollment_expired_attempt`

`EnrollmentService.Retrieve` answers an expired code exactly like an
unknown one — the caller holds a dead capability either way, and the
distinction belongs to the approver, not the wire. That stays. But at
the point the server gives that answer, it has already loaded the
enrollment row: the attempt is fully attributable, and it is the single
most informative signal this system can send. Either a forgotten job is
now failing on schedule, or someone is replaying a credential that
should no longer exist. Both are things the account's holders want to
hear about once — and today the server says nothing to anyone.

- Emitted from the expired branch of `Retrieve`, before returning the
  unchanged not-found answer. Attempts with genuinely unknown codes stay
  silent: there is no row, so there is no one to tell.
- Payload: the enrollment identity (service account, enrollment ID, key
  fingerprint), the attempt's source IP and time, and when the code
  expired.

A broken cron job retries forever, so this needs the same DB-claimed
dedupe as the reminder, with a window instead of a one-shot: a
`last_expired_attempt_notified_at` column, claimed with a guarded update
(`... WHERE last_expired_attempt_notified_at IS NULL OR
last_expired_attempt_notified_at < ?`) so each enrollment produces at
most one message per window (suggested: 24h) no matter how hot the retry
loop or how many instances field it.

Default on: it fires only when something is already wrong, so it is
quiet for everyone whose jobs work.

## `user_certificate_issued` and `pam_certificate_issued`

The "was this you?" pair. The requester was present for both flows —
approving in a browser, or typing a password at a PAM prompt — so on the
happy path these messages confirm what the user already knows. Their
value is the unhappy path: a certificate minted by a session the user
does not recognize, from an address they were never at.

- **Default off, both of them.** Interactive certificates are issued per
  login and PAM certificates per `sudo`; either kind defaulting on would
  make every existing deployment noisy the day it upgrades, which is
  precisely the outcome the registry's `DefaultEnabled` comment exists
  to prevent. Users who want the signal opt in at `/preferences`;
  security-sensitive deployments can tell their users to.
- Two kinds, not one with a type field, so the tolerances can differ: a
  user who runs `sudo` forty times a day and logs in twice can keep the
  login signal without drowning in the other.
- Emit point: `SignedReplyHandler.resolveSuccess`, which already handles
  exactly the non-service types and has the request row (user, source
  IP, requested options) and the reply (serial, key ID, principals,
  expiry) in hand. Emitting where the certificate becomes real — rather
  than at approval — means one emit site covers both types and the
  message never describes a certificate that failed to sign.
- Payload: certificate type, key ID, principals, serial, source IP of
  the request, issued/expires timestamps, and `ServerURL` for the link
  to the certificate's detail page.

These are user-scoped, not enrollment-scoped: the recipient is the
identity email, no override applies.

## Sequencing

All of it landed in one change, so the ordering below is now history rather
than a plan.

1. **Done:** the notification address column,
   `service_enrollment_expiring`, and
   `service_enrollment_expired_attempt`. The three dedupe and address
   columns did share one migration, as expected. The address turned out
   *not* to be a branch in `NotificationHandler.recipients` alone — the
   consumer had no way to know which enrollment an account-addressed event
   was about, so `notify.Event` gained an `EnrollmentID` and the emit sites
   moved to `NotifyEnrollment`.
2. **Done, with the group-ownership work
   ([enrollment-group-ownership.md](enrollment-group-ownership.md)):** the
   fan-out delivery model.
3. **Done:** the issued pair, emitted from
   `SignedReplyHandler.resolveSuccess` as proposed.

## Open questions

- **Domain restriction on the notification address.** Shipped
  unrestricted, documented in
  [operations/email-notifications.md](../operations/email-notifications.md).
  An allowlist config stays cheap to add later without a migration.
- **Reminder cardinality.** Shipped as one reminder per enrollment. A
  second, closer-in reminder (say 7 days and 24 hours) doubles the
  column bookkeeping for unclear gain; revisit if operators ask.
- ~~**Whose preference gates an overridden send.**~~ Resolved by group
  ownership: with no single owner there is no preference that could
  gate the address, so a set address sends ungated (the subscription
  semantic), and fan-out sends are gated per holder. See "Delivery
  semantics".
