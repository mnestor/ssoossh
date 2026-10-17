# Part 2: server web UI

**Repo:** `mnestor/ssoossh`, `frontend/` (SvelteKit). **Depends on:** Part 1
endpoints. Follow `frontend/DESIGN.md` for the design system. Shared context
in [README.md](README.md).

## Scope

1. Fleet key list, detail, create, rotate, revoke, edit.
2. Rotation report.
3. Shell preference on the user's own settings page.
4. Hidden entirely when `nss_enabled` in the settings payload is false
   (Part 1 adds it beside `mail_enabled`; README R25).
5. "Pending id changes" and "Collisions" panels on the existing
   `/admin/directory` page (README R38, R39).

No new API surface beyond Part 1. All data comes from `/api/nss/keys*`,
`/api/nss/shells` and `/api/users/me`.

## Routes

| Route | Who sees it | Notes |
| --- | --- | --- |
| `/nss-keys` | anyone whose `/api/nss/keys` list is non-empty, plus `is_nss_key_admin` from `/api/users/me` | top-level nav entry, **not under `/admin`**: `frontend/src/routes/admin/+layout.svelte` gates on admin roles, and key-group members are not admins (README R16) |
| `/nss-keys/new` | `is_nss_key_admin` (admins inherit it) | |
| `/nss-keys/[id]` | read roles for that key | detail + usage |
| `/nss-keys/[id]/rotation` | read roles | rotation report |
| `/preferences` (existing) | every user | gains login shell field. `frontend/src/routes/preferences/` holds the notification and detail-level settings; the shell field goes there, not on `/account` |
| `/admin/directory` (existing) | admin | gains a "Pending id changes" panel listing name, DN, stored and pending uid or gid, and how long it has been pending, from `GET /api/admin/nss/pending-id-changes`. One **Accept** button per row behind a typed confirmation (enter the name), whose copy says hosts will see the new number on their next refresh and that files owned by the old number are not moved. Empty state: "No pending changes". Beside it a "Collisions" panel from `GET /api/admin/nss/collisions`: one row per uid or name held by two entries, naming both, marking a tombstone, with a **Purge tombstone** button (typed confirmation, the name) that calls the DELETE. Live-versus-live collisions have no button, only the DNs to fix in the directory. Both panels hidden when `nss_enabled` is false |

Admins also get a link from the admin area to `/nss-keys`.

Server enforcement is authoritative; the UI only hides actions the role
cannot perform, using the per-key `permissions` array from Part 1.

## Key list

Columns: id (monospace), name, description (truncated), group, expires
(relative, amber within 30 days, red when past), last used (from usage
aggregate), status (`active`, `rotating` while a previous secret is live,
`expired`, `revoked`). Filter by status and group; sort by expiry.

## Create

Form: name (required), description, group (free text with suggestions from
known groups if an endpoint exists; otherwise free text), expiry (default
one year, date picker), notification email (default the current user's).

On success, a one-time secret panel:

- shows `key_id` and `key_secret` and the two-line `nss.key` file content
  ready to paste;
- copy buttons; a checkbox "I have stored this secret" must be ticked
  before the panel can be dismissed;
- warns that the secret cannot be shown again.

## Detail

Metadata block, editable inline per `permissions`. Actions:

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

On the user's settings page: a "Login shell" select populated from
`/api/nss/shells`, with a "Directory default" option that sends `null`.
Help text: "Hosts use this shell if installed; otherwise the directory
shell." Saves via `PATCH /api/users/me`; shows server validation errors.
Hidden when `/api/users/me` returns `posix: null` (no linked directory
entry, or NSS disabled).

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
- Shell select: null option, server error display.

## Done when

`npm run check`, lint and vitest pass from `frontend/`; the bundled
`server/frontend/dist` is rebuilt per the repo's usual process.
