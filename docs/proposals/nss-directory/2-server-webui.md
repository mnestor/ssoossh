# Part 2: server web UI

**Repo:** `mnestor/ssoossh`, `frontend/` (SvelteKit). **Depends on:** Part 1
endpoints. Follow `frontend/DESIGN.md` for the design system. Shared context
in [README.md](README.md).

## Scope

1. Fleet key list, detail, create, rotate, revoke, edit.
2. Rotation report.
3. Shell preference on the user's own settings page (removed 2026-10-18 after the threat review: the shell comes from the directory and host policy only).
4. Hidden entirely when `nss_enabled` in the settings payload is false
   (Part 1 adds it beside `mail_enabled`; README R25).
5. "Pending id changes" and "Collisions" panels on the existing
   `/admin/directory` page (README R38, R39).
6. A read-only viewer of the stored accounts and groups, with what a
   host's lookup is served for each, under `/admin/nss` (added 2026-10-18
   at the owner's request).

No new API surface beyond Part 1. All data comes from `/api/nss/keys*`
and `/api/users/me`.

## Routes

| Route | Who sees it | Notes |
| --- | --- | --- |
| `/nss-keys` | anyone whose `/api/nss/keys` list is non-empty, plus `is_nss_key_admin` from `/api/users/me` | top-level nav entry, **not under `/admin`**: `frontend/src/routes/admin/+layout.svelte` gates on admin roles, and key-group members are not admins (README R16) |
| `/nss-keys/new` | `is_nss_key_admin` (admins inherit it) | |
| `/nss-keys/[id]` | read roles for that key | detail + usage |
| `/nss-keys/[id]/rotation` | read roles | rotation report |
| `/admin/directory` (existing) | admin | gains a "Pending id changes" panel listing name, DN, stored and pending uid or gid, and how long it has been pending, from `GET /api/admin/nss/pending-id-changes`. One **Accept** button per row behind a typed confirmation (enter the name), whose copy says hosts will see the new number on their next refresh and that files owned by the old number are not moved. Empty state: "No pending changes". Beside it a "Collisions" panel from `GET /api/admin/nss/collisions`: one row per uid or name held by two entries, naming both, marking a tombstone, with a **Purge tombstone** button (typed confirmation, the name) that calls the DELETE. Live-versus-live collisions have no button, only the DNs to fix in the directory. Both panels hidden when `nss_enabled` is false |

| `/admin/nss` | auditor, SOC, admin | (added 2026-10-18 at the owner's request) "NSS directory" in the admin group of the rail, not admin only, shown only when `nss_enabled` is true. Redirects to `/admin/nss/accounts` |
| `/admin/nss/accounts`, `/admin/nss/groups` | auditor, SOC, admin | (added 2026-10-18 at the owner's request) the stored accounts and groups from `GET /api/admin/nss/users` and `/groups`: search (name, DN, or an exact uid or gid when numeric) and a state filter, both server-side and kept in the address (`?q=`, `?state=`; groups have no `disabled` state and the server answers 400 for it), cursor paging with Load more. An Accounts / Groups switch in the heading. Empty states tell "nothing stored yet" (the sync stores what `nss.user_filter` or `nss.group_filter` selects) from "nothing matches" (a host lookup that misses the stored rows still asks the directory; the Probe shows what the directory says). An info alert naming `nss.enabled` when NSS is off |
| `/admin/nss/accounts/[uid]`, `/admin/nss/groups/[gid]` | auditor, SOC, admin | (added 2026-10-18 at the owner's request) one stored entry from `GET /api/admin/nss/users/{uid}` or `/groups/{gid}`. The identity strip carries the name, the state, the numbers and the served verdict: the server's `served`, or its `not_served` reason in words, never worked out in the browser. Sections: what hosts get (the served record; for a group the member-name count and a note naming `nss.max_group_members` when the list was cut), the stored entry, a state section when the entry is not active (missing, tombstoned, withheld with its reason, a pending uid or gid), the stored memberships (account) or the members paged from `/groups/{gid}/members` (group), then a History section (decided 2026-10-18 by the owner): the row's audit events from `/users/{uid}/audit` or `/groups/{gid}/audit`, newest first, through the same `AuditTimeline` and `LoadMoreFooter` as a user's audit timeline, with its own loading, "No recorded changes", error with Try again, and Load more with the list focus rule. It follows the row through a rename or an accepted id change, so a group's page shows a member added after the group was stored and the member's page shows the same event. Every related account, group and linked person is a link. A 404 says no stored entry has that number and that a host lookup would still ask the directory; a group 404 also links the account with that uid, since private groups are never stored. Nothing on these pages accepts, purges or edits |
| `/admin/directory` (existing) | admin | (added 2026-10-18 at the owner's request) gains a "Stored entries" section counting the stored accounts and groups with links to the two lists; it loads and fails on its own. The pending and collision rows link each stored entry to its viewer page |

Admins also get a link from the admin area to `/nss-keys`.

The Directory page is admin only, so the viewer links to its Probe, Sync,
Pending id changes and Collisions sections only for admins; SOC and auditors
read the same sentence naming what an admin can do there.

Server enforcement is authoritative; the UI only hides actions the role
cannot perform, using the per-key `permissions` array from Part 1.

## Key list

Columns: id (monospace), name, description (truncated), group, expires
(relative, amber within 30 days, red when past), last used (from usage
aggregate), status (`active`, `rotating` while a previous secret is live,
`expired`, `revoked`). Filter by status and group; sort by expiry.

## Create

Form: name (required), description, group (free text with suggestions from
known groups if an endpoint exists; otherwise free text), expiry (date
picker bounded by `nss.key_max_lifetime` and starting at its last day;
empty means no expiry when the setting is 0; not editable, and each
rotation renews it),
notification email (default the current user's).

On success, a one-time secret panel:

- shows `key_id` and `key_secret` and the two-line `nss.key` file content
  ready to paste;
- copy buttons; a checkbox "I have stored this secret" must be ticked
  before the panel can be dismissed;
- warns that the secret cannot be shown again.

## Detail

Metadata block, editable inline per `permissions`, except the expiry, which only rotation renews. Actions:

- **Rotate**: confirm dialog stating the grace expiry date; then the same
  one-time secret panel as create.
- **Clear previous secret**: enabled while rotating; confirm dialog showing
  the not-switched count from the rotation report ("12 hosts still on the
  old secret will start failing").
- **Revoke**: typed confirmation (enter the key id); explains both secrets
  die immediately.

Usage table: per (source IP, generation, day), first/last seen, lookups,
misses; paginated; miss-rate column highlighted above the alert threshold.

## Rotation report

The page an operator opens after rotating. Header counts: switched, not
switched, gone, as "12 of 140 hosts still on the old secret". Three grouped
sections, each row: source IP, first seen, last seen, lookups, generation
last presented. Sort and filter; CSV export via
`/api/nss/keys/:id/rotation-report?format=csv`. Days of grace left shown in
the header. Empty state before any rotation: "No rotation yet".

## Shell preference

None (removed 2026-10-18 after the threat review: the shell comes from the directory and host policy only). The settings page has no login shell field, and
`/preferences` keeps only the notification and detail-level settings.

## Tests (vitest, colocated `page.test.ts`)

- Nav entry visibility by role and by `nss.enabled`.
- Pending id changes: rows render from fixture data; Accept requires the
  exact name; a 409 from accept is shown on the row; panel hidden when
  `nss_enabled` is false.
- Collisions: a tombstone row offers Purge, a live pair does not; Purge
  requires the exact name.
- Action buttons follow `permissions`.
- One-time secret panel cannot be dismissed until acknowledged; secret not
  retained in component state after dismissal.
- Revoke requires the exact id.
- Rotation report bucketing and counts render from fixture data.
- Directory viewer (added 2026-10-18): the rail entry for auditor, SOC and
  admin and hidden with NSS off; search and state in the address; each
  empty, error and Load more state; the served verdict for served and each
  `not_served` reason; links between accounts, groups and members; the
  admin-only references as links for admins and plain sentences otherwise;
  the Stored entries section and its failure isolation; on both detail
  pages the History section's loading, empty, error with Try again, and
  Load more with its focus and its failure.

## Done when

`npm run check`, lint and vitest pass from `frontend/`; the bundled
`server/frontend/dist` is rebuilt per the repo's usual process.
