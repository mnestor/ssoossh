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
7. A fleet-wide list of every key for auditors and above, `/admin/nss/keys`,
   and `/nss-keys` narrowed to the keys the viewer's groups manage (added
   2026-10-19 at the owner's request).

No new API surface beyond Part 1. All data comes from `/api/nss/keys*`,
`/api/admin/nss/*` and `/api/users/me`.

## Routes

| Route | Who sees it | Notes |
| --- | --- | --- |
| `/nss-keys` | anyone whose `/api/nss/keys` list is non-empty, and `is_nss_key_admin` (admins inherit it) whenever `nss_enabled` is true (changed 2026-10-19 by the owner: the list is the keys whose group is one of the viewer's groups for every role, so no role shows the entry on its own; reversed 2026-10-20 by the owner: admins and fleet key admins see the entry whenever `nss_enabled` is on, without a key in their groups, and the rail skips the key list read for them; the list itself is unchanged) | top-level nav entry, **not under `/admin`**: `frontend/src/routes/admin/+layout.svelte` gates on admin roles, and key-group members are not admins (README R16) |
| `/admin/nss/keys` | auditor, SOC, admin, and `is_nss_key_admin` (changed 2026-10-19 by the owner: the only admin page that role opens) | (added 2026-10-19) "NSS keys (all)" in the admin group of the rail (the only entry there for a fleet key admin with no admin area role), beside "NSS directory", only when `nss_enabled` is true. Every key, see "Fleet keys (admin)" below |
| `/nss-keys/new` | `is_nss_key_admin` (admins inherit it) | |
| `/nss-keys/[id]` | read roles for that key | detail + usage |
| `/nss-keys/[id]/rotation` | read roles | rotation report |
| `/admin/directory` (existing) | admin | gains a "Pending id changes" panel listing name, DN, stored and pending uid or gid, and how long it has been pending, from `GET /api/admin/nss/pending-id-changes`. One **Accept** button per row behind a typed confirmation (enter the name), whose copy says hosts will see the new number on their next refresh and that files owned by the old number are not moved. Empty state: "No pending changes". Beside it a "Collisions" panel from `GET /api/admin/nss/collisions`: one row per uid or name held by two entries, naming both, marking a tombstone, with a **Purge tombstone** button (typed confirmation, the name) that calls the DELETE. Live-versus-live collisions have no button, only the DNs to fix in the directory. Both panels hidden when `nss_enabled` is false |

| `/admin/nss` | auditor, SOC, admin | (added 2026-10-18 at the owner's request) "NSS directory" in the admin group of the rail, not admin only, shown only when `nss_enabled` is true. Redirects to `/admin/nss/accounts` |
| `/admin/nss/accounts`, `/admin/nss/groups` | auditor, SOC, admin | (added 2026-10-18 at the owner's request) the stored accounts and groups from `GET /api/admin/nss/users` and `/groups`: search (name, DN, or an exact uid or gid when numeric) and a state filter, both server-side and kept in the address (`?q=`, `?state=`; groups have no `disabled` state and the server answers 400 for it), cursor paging with Load more. An Accounts / Groups switch in the heading. Empty states tell "nothing stored yet" (the sync stores what `nss.user_filter` or `nss.group_filter` selects) from "nothing matches" (a host lookup that misses the stored rows still asks the directory; the Probe shows what the directory says). An info alert naming `nss.enabled` when NSS is off |
| `/admin/nss/accounts/[uid]`, `/admin/nss/groups/[gid]` | auditor, SOC, admin | (added 2026-10-18 at the owner's request) one stored entry from `GET /api/admin/nss/users/{uid}` or `/groups/{gid}`. The identity strip carries the name, the state, the numbers and the served verdict: the server's `served`, or its `not_served` reason in words, never worked out in the browser. Sections: what hosts get (the served record; for a group the member-name count and a note naming `nss.max_group_members` when the list was cut), the stored entry, a state section when the entry is not active (missing, tombstoned, withheld with its reason, a pending uid or gid), the stored memberships (account) or the members paged from `/groups/{gid}/members` (group), each table with an Added column (changed 2026-10-18 by the owner) showing the membership's `added_at`, when ssoosshd first stored it, or "Before first import" for one the first full import stored, then a History section (decided 2026-10-18 by the owner): the row's audit events from `/users/{uid}/audit` or `/groups/{gid}/audit`, newest first, as one line per thing that happened and its date (changed 2026-10-18 by the owner: on an account's page "Removed from ops" for that account only, on a group's page one line per member removed and "and N more removed" for a capped list, lifecycle events one line each; changed again 2026-10-18 by the owner: additions draw no line, since the Added column dates each current membership, so an event that only added draws none; the ambiguous "Added to or removed from G" of an account past both capped lists stays, as it may be a removal), through the same `LoadMoreFooter` as a user's audit timeline, counted in events, with its own loading, "No recorded changes", "None of the events loaded so far is listed here" when the loaded events drew no line, error with Try again, and Load more with the list focus rule. It follows the row through a rename or an accepted id change, so a group's page shows a member removed after the group was stored and the member's page shows the same event. Every related account, group and linked person is a link. A 404 says no stored entry has that number and that a host lookup would still ask the directory; a group 404 also links the account with that uid, since private groups are never stored. Nothing on these pages accepts, purges or edits |
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

## Fleet keys (admin)

(added 2026-10-19 at the owner's request)

`/admin/nss/keys`, `wide`, titled "All NSS keys", from
`GET /api/admin/nss/keys` (Part 1, "Fleet key admin endpoints"). The reader
starts from a clue, usually an address from a log or an alert, else a key's
name, group or creator, and opens the key. The worst case is acting on the
wrong key, or concluding an address uses no key when the search quietly
missed, so:

- **Search**: one field, labelled "Search keys", with the hint "A name,
  group, creator or key id, or an address (10.0.4.7) or range
  (10.0.4.0/24)." Plain text,
  no `inputmode`, autocomplete and spellcheck off. Text matches name, group
  and the creator's username or email. An address or a CIDR range searches
  the usage within `nss.usage_retention`, and a line under the field says
  "Keys used from 10.0.4.0/24 in the last 90 days", the range as the server
  normalised it and the window from its response. A term shaped like an
  address that does not parse gets an inline error naming the problem
  ("10.0.4.0/33 is not a valid range: an IPv4 prefix is 0 to 32.") on Enter
  or leaving the field, and no request; the server's 400 lands in the same
  place. Typing settles into a search as every list's box does.
- **Filters**: a Status group (All, Active, Expired, Revoked) and a "Needs
  attention" group of toggles (Expiring, Unused 30 days, Alerts 7 days);
  Expiring adds a 7 / 30 / 90 days choice beside it, 30 by default. All in the
  address (`?q=`, `?status=`, `?expiring=`, `?unused=1`, `?alerts=1`),
  server-side, unknown values dropped. Clear filters once any is set.
- **Rows**: Name (mono, the link, the group muted under it), Creator
  (username, email under it; "unknown user" and the id when the account is
  gone), Status (the key badge, the expiry as on `/nss-keys`, and a recent
  usage alert), Hosts (distinct addresses in 7 days, right-aligned) and Last
  seen. For an address search the Hosts column is "Used from": up to five
  matching addresses, when each was last seen, "current secret" or "previous
  secret", and "and N more". No action on any row: a row opens
  `/nss-keys/[id]?from=admin`, whose back chip ("All NSS keys") returns with
  the filters.
- **Heading**: New key, a ghost link, for the fleet key admin role only,
  opening the create form with `?from=admin`.
- **States**: skeleton rows; no keys yet (why a host needs one, and who
  creates them); nothing matching with Clear filters; an address no key was
  used from, naming the window and that the address is the one the server
  observed (the forwarded one behind a proxy in `http.trusted_proxies`); a
  load error with Try again; Load more through `LoadMoreFooter`; the NSS
  off notice; an expired session goes to sign in.

`/nss-keys` (changed 2026-10-19): the keys whose group is one of the
viewer's groups, for every role. Its empty state says a key is listed once
its group is one the viewer is in, links auditors to All NSS keys, and keeps
New key for the fleet key admin role. The create form suggests the groups of
every key only to the admin area's roles.

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
  Load more with its focus and its failure; the Added column with a date
  and with "Before first import", and no history line for an addition
  (2026-10-18).

## Done when

`npm run check`, lint and vitest pass from `frontend/`; the bundled
`server/frontend/dist` is rebuilt per the repo's usual process.
