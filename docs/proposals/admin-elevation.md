# Admin mode: re-authenticate before admin, SOC or audit work

**Status:** proposal, 2026-10-10. Nothing built. Part of the frontend
redesign (see `frontend/DESIGN.md`, "Character" and "Detail level"); lands
before or with the NSS directory work (`docs/proposals/nss-directory/`).

**Anchors verified at:** `44d48f0` (2026-10-10). `file:line` references
drift; re-check before relying on one.

## What this proposes

A signed-in session is an ordinary user session, whatever groups the person
holds. To use admin, SOC or auditor powers they **enter admin mode**: the
browser goes back through the identity provider with a forced fresh login,
the server checks that the provider really re-authenticated them, and the
session is marked elevated until a deadline. Every admin, SOC and auditor
route and service check requires that mark. Admin mode ends on its idle or
absolute deadline, on "Exit admin mode", or on sign-out.

Three things this buys:

1. **Least privilege by default.** A stolen or replayed session cookie, a
   cross-site bug, or a browser left unlocked reaches only what an ordinary
   user can: their own requests, their own service codes, their own NSS
   keys. Admin power needs a login the attacker does not have.
2. **Simpler everyday screens.** Unelevated, the rail has no Admin group and
   no containment actions. Most sessions are approvals, service codes and
   NSS key work, and they never see the admin surface.
3. **A deliberate step.** Entering admin mode is the "yes, I am sure I want
   to do admin work" moment, recorded in the audit log, and time-boxed.

It is the same pattern as GitHub's sudo mode and Azure PIM's just-in-time
roles, scoped to this product's roles.

## What exists today

| Thing | Where | Bearing |
| --- | --- | --- |
| Roles are a ladder computed from groups on every request | `server/config/types.go:422-460` (`GrantsAdmin`, `GrantsSOC`, `GrantsAuditor`; admin implies SOC implies auditor) | The one place to change what "holds a role" means |
| Route gates | `server/middleware/auth_middleware.go` (`AdminAuthMiddleware`, `SOCAuthMiddleware`, `AuditorAuthMiddleware`), mounted after `sessionAuthMiddleware` on 10 route groups | Enforced at the route |
| Service-layer role checks | 14 `Grants*` calls: `service/enrollment.go` (9), `service/certificate.go` (2), `controller/responses.go` (3) | Enforced below the route too; elevation must reach these, not just the middleware |
| Sessions are server-side | `gormstore` in `server/bootstrap/router.go:225-240`; only the id is in the cookie | Elevation state can live in the session and be cleared server-side |
| Session timestamps | `middleware/session_auth.go:85-89` (`identity_issued_at`, refreshed-at) | Precedent for storing elevation times |
| A forced re-login flow already exists | `service/auth.go:681-699` `EchoAuthorizationURL` adds `prompt=login`; `controller/auth.go:170-240` routes the shared callback by a session flag (`SetOIDCEchoState` / `PopOIDCEcho`) | Admin mode reuses this shape: a third callback purpose |
| `/api/users/me` reports roles | `controller/responses.go:104-106` (`is_admin`, `is_soc`, `is_auditor`) | Needs to say both "holds" and "is elevated" |

## Design

### Effective roles, computed once

Introduce an **effective role set** on the request identity, computed in
the session middleware:

- `held`: what the groups grant (today's `Grants*` results).
- `elevated`: true when the session's elevation is live (below).
- `effective = held if elevated else none`.

Every role check reads `effective`, never groups directly:

- the three route middlewares check `identity.Roles.Admin/SOC/Auditor`;
- the 14 service and controller call sites move from
  `c.Admin.GrantsSOC(identity.Groups)` to `identity.Roles.SOC` (or a small
  `access.SOC(identity)` helper);
- `Grants*` stays the single authority for *held*, called in one place.

Groups themselves are not stripped. They also feed certificate policy and
principal mapping, and an unelevated user must still get the certificates
their groups entitle them to; only *administrative* power is gated.

Option rejected: stripping admin groups from `identity.Groups` when
unelevated. It is a one-line change that silently alters certificate policy
for anyone whose admin group also appears in a policy rule.

### Entering admin mode

1. `POST /api/auth/elevate/start` (session + CSRF; 403 if the person holds
   no admin, SOC or auditor role). Server stores state, nonce, PKCE verifier
   and an `oidc_elevate` flag in the session, as `SetOIDCEchoState` does, and
   returns the authorization URL built with `prompt=login` and
   `max_age=0`.
2. The browser goes to the provider; the person logs in again.
3. The shared callback pops the flag and runs `elevateCallback`:
   - verify the ID token exactly as login does (`HandleCallback` path);
   - require `sub` to equal the session's subject (an elevation can never
     switch users);
   - require `auth_time` to be present and no older than
     `admin.elevation.max_auth_age` (default 5m). A provider that ignores
     `prompt=login` returns an old `auth_time` and is refused, fail closed;
   - recompute held roles from the fresh token's groups;
   - set `elevated_at`, `elevated_last_seen` and `elevated_until` in the
     session; audit `admin_elevation_started` with subject, held roles and
     source address.
4. Redirect back to the page that asked, the return URL checked by the
   existing `isSafeReturnURL` (`server/controller/auth.go:256`).

### Staying elevated, and leaving

- **Idle deadline:** `admin.elevation.idle_timeout` (default 15m). Each
  request that *uses* an effective role slides `elevated_last_seen`; plain
  user requests do not, so browsing your own certificates does not keep
  admin mode alive.
- **Absolute deadline:** `admin.elevation.max_duration` (default 1h).
- **Exit:** `POST /api/auth/elevate/end` clears the three fields; audited
  `admin_elevation_ended` with reason `exit`, `idle`, `expired` or
  `signout`.
- Sign-out and session expiry end it with the session.
- An elevated session never outlives `http.cookie_max_age`; config
  validation rejects `max_duration` greater than it.

### What requires admin mode

| Work | Needs admin mode |
| --- | --- |
| Approve or deny your own requests; your own certificates and history | no |
| Your own service codes (create, notify, expire your own) | no |
| NSS keys you manage as a key-group member or NSS key admin (`admin.nss_key_group`) | no |
| Your own preferences | no |
| Anything under `/api/admin/*`, reads included (users, certificates, audit, config, directory, diagnostics, claims echo) | yes |
| SOC containment: expire someone else's service code, disable or enable a user, revoke an NSS key outside your groups | yes |
| Auditor reads of anything that is not yours, NSS keys included | yes |

NSS key admins are deliberately outside admin mode: key creation and
rotation is the everyday NSS work. Admin mode is about acting on other
people's things.

Option considered: leave auditor *reads* unelevated. Rejected because the
point is also to hide the admin surface from everyday sessions, and an
auditor's reads are of other people's data.

### API surface

- `POST /api/auth/elevate/start` → `{ authorization_url }`.
- `POST /api/auth/elevate/end` → 204.
- `GET /api/users/me` gains:
  ```json
  "roles_held": { "admin": true, "soc": true, "auditor": true, "nss_key_admin": false },
  "elevation": { "active": true, "until": "2026-10-10T15:12:00Z", "idle_until": "2026-10-10T14:27:00Z" }
  ```
  `is_admin`, `is_soc`, `is_auditor` become the *effective* values (false
  when unelevated), so existing frontend gates hide admin UI with no other
  change. `is_nss_key_admin` (decided for the NSS work) is *held*, since NSS
  key admin is not elevated.
- A gated route called unelevated by someone who holds the role returns
  `403` with `error_code: "elevation_required"`; without the role it stays
  the plain `403` it is today, so the code does not reveal roles to people
  who lack them.
- `make wire-contract`, swag annotations and `make confdocs` regenerate as
  usual.

### Configuration

```yaml
admin:
  elevation:
    enabled: true          # false keeps today's behaviour: held == effective
    idle_timeout: 15m
    max_duration: 1h
    max_auth_age: 5m       # how fresh auth_time must be on the elevation login
```

All four keys are configurable; the values above are the defaults.

`enabled: false` is for deployments whose identity provider cannot force a
fresh login or omits `auth_time` (the operator does not always control the
provider). Held roles are then effective, as today, and the startup log says
admin powers are always on. It defaults to true. Existing installs get a
release note, because admins will be asked to re-authenticate on first
admin use.

Two checks help an operator find out which setting they need:

- **At startup,** when `enabled` is true, read the provider's discovery
  document (already fetched for login). If `claims_supported` is present and
  does not list `auth_time`, log a warning naming
  `admin.elevation.enabled`. A missing `claims_supported` is not a warning;
  many providers omit it.
- **At elevation,** a token with no `auth_time`, or a stale one, is refused
  with `error_code: "elevation_unsupported_idp"` and a message telling an
  admin that the provider did not re-authenticate and that
  `admin.elevation.enabled: false` turns the feature off. The refusal is
  audited with its reason.

Pocket ID advertises `prompt=login` support; it is the provider to verify
the flow against in e2e and by hand.

### Frontend

- Account menu: "Enter admin mode", shown only when `roles_held` has any
  admin, SOC or auditor role and `elevation.active` is false.
- While elevated, a bar at the top of the content area (not in the rail):
  "Admin mode · 12 min left · Exit admin mode". `role="status"`; the time is
  announced at 5 minutes and 1 minute left, not continuously. Its tint is
  `trimmed-surface`, the caution plane, so an elevated screen never looks
  like an everyday one; cobalt stays the primary action.
- The rail's Admin group and every containment action render only when
  elevated (`is_*` are effective, so the existing `nav.ts` gates hold, plus
  the per-item `minRole` the redesign adds).
- A page that receives `elevation_required` (a deep link, or elevation that
  lapsed mid-task) shows one panel: "This needs admin mode. You'll sign in
  again with your identity provider." with "Enter admin mode", returning to
  the same URL afterwards. Unsaved input on that page is not kept; the
  panel says so when a form is dirty.
- Detail level and remembered principals are unaffected; they are
  per-browser display state, not roles.

## Threat model notes

- Elevation does not protect against a fully compromised browser that can
  also complete an IdP login (malware with the user present). It does
  protect against cookie theft and replay, CSRF, and an unattended session.
- The elevation callback shares the login callback's CSRF state and nonce
  checks. The `sub` match prevents a login as someone else from elevating
  this session.
- `auth_time` freshness is the control that makes `prompt=login`
  meaningful; without it a provider that silently reuses its session would
  make admin mode cosmetic. Hence fail closed when it is absent.
- Audit events (`admin_elevation_started`, `_ended`, and each refused
  attempt with its reason) give SOC a record of who held admin power when.

## Tests

- Table-driven role resolution: held × elevated × idle/absolute expiry →
  effective, across all three roles and the ladder.
- Each route group returns `elevation_required` for a holder who is
  unelevated, the plain 403 for a non-holder, and passes when elevated.
- Each of the 14 service and controller call sites, through their existing
  tests, with an unelevated holder.
- Elevation callback: wrong `sub`, missing `auth_time`, stale `auth_time`,
  replayed state, provider error, success; and the audit events for each.
- Idle sliding only on role-using requests; absolute cap; exit; sign-out.
- Config validation: `max_duration` must not exceed `http.cookie_max_age`;
  `enabled: false` makes held equal effective.
- Frontend: menu item visibility, the bar and its countdown announcements,
  the `elevation_required` panel and return URL, rail gating unelevated and
  elevated.
- e2e (`test/e2e`): admin and SOC flows elevate first; the mock IdP returns
  a fresh `auth_time` on `prompt=login`.

## Decisions

Taken 2026-10-10:

- Timeouts: 15 minutes idle, 1 hour absolute, 5 minutes auth age, all
  configurable; no separate SOC timeout.
- No email on entering admin mode; the audit log records it.
- The feature is switchable (`admin.elevation.enabled`), default on, for
  deployments whose identity provider does not support a forced fresh
  login. Pocket ID is the reference provider.
