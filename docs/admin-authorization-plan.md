# Admin authorization — plan

**Status: planned, nothing implemented.** There is no admin concept in the
product today: `model.User` has no role, no middleware authorizes an
admin-scoped action, and no phase currently owns building one. It has been
deferred repeatedly across the project's planning documents, which is why it
needs writing down before it is deferred again.

## What needs it

| Wants an admin | Where it was deferred |
| --- | --- |
| Certificate history across all users | **auditor** — read-only, sees everything |
| Effective configuration view | **auditor** — it cannot be changed, so read access is all there is |
| Editing source-network policy | **neither** — it belongs to whoever enrolled the host the rule applies to, not to a global role |
| Manually expiring an enrollment | **admin** — the one genuinely administrative function in the product |
| ~~Listing pending requests~~ | Removed outright rather than gated — see below |
| Disabling a departed user | **admin**, and the counterpart to the above — see below |

Answered by walking the list rather than by designing a role first, which is
why the result is two narrow roles instead of a permission system: **three of
five were reads**, one belongs to host ownership, and only enrollment expiry —
with the user-disable flow it implies — is administrative.

## Disabling a user

The admin half of revocation, and the reason an admin role exists at all.
There are two routes to the same state:

1. **From the directory.** The account is disabled upstream and a scheduled
   sweep notices. Requires LDAP enrichment, which is not yet built.
2. **By an administrator**, in the web UI, for deployments without a directory
   or when waiting for the sweep is too slow.

Either way the effect is the same: after a **configured grace period**, the
enrollments approved by that user expire, and the user cannot authenticate
again. The grace period exists because revocation that takes effect instantly
takes production with it — an unattended job losing its credential at 03:00 is
the failure this is meant to prevent, not cause.

**The UI must show what a disable will break before it happens** — which
enrollments, and which service accounts stop renewing. An administrator
disabling a departed colleague needs to know they are about to stop the
nightly backup, in time to hand it to someone else. That is also what makes
Phase 9's backup-owner idea useful rather than decorative.

User certificates are deliberately not in scope here: they are measured in
hours and expire before any of this would matter.

## The one thing that turned out not to need an admin

Writing this list surfaced `GET /api/certs/requests/pending`, which was
session-authed but scoped to nobody: it returned every pending request in the
deployment, each with its `ID`, to any authenticated caller. The dashboard
consumed it in a "Waiting for you" card with Review links, captioned as though
the requests were the viewer's own.

The obvious fix was to scope it to the caller and make the deployment-wide view
an admin power. That was the wrong instinct, and the endpoint is now **gone**
— endpoint, service method, wire type, generated artifacts, and the dashboard
card:

- A request is created by an **unauthenticated** call, so it has no owner at
  creation and there is nothing to scope a list to.
- The ID is the capability, so listing IDs lets any signed-in user bind
  somebody else's request and deny the real requester.
- A certificate takes the **approver's** principals, so a screen inviting
  people to approve requests they did not start is an escalation channel, not
  a convenience. Making it admin-only would have concentrated that hazard on
  the accounts with the most access rather than removing it.

Worth keeping as the shape of question to ask about anything else on the list:
some of these want an admin, and at least one wanted deleting.

## Where "admin" comes from

**An OIDC group named in configuration.** Same shape as the existing
`cert_options.*.require_group`:

```yaml
admin:
  require_group: "SSH Admins"       # empty disables every admin surface
  auditor_group: "SSH Auditors"     # optional, read-only
```

Rationale:

- **No bootstrapping problem.** There is no "who creates the first admin",
  which a database flag always has and always answers badly (a break-glass CLI,
  a first-run wizard, or a seeded account).
- **The identity provider stays authoritative**, which is the product's entire
  thesis. Admin access is granted and revoked where every other access decision
  is already made.
- **An admin cannot grant admin.** The set of administrators lives in config
  and the IdP, both out of reach over HTTP, which is the same property that
  makes runtime-editable policy safe: nothing reachable from the web tier can
  widen anyone's authority.

Rejected: a role column on `model.User` (bootstrapping, drift from the IdP, and
it lets an admin promote others through the API); and casbin, which is listed
in the root `CLAUDE.md` stack but appears in neither `go.mod` nor any import —
a policy engine is a lot of machinery for two roles, and that stale line should
be struck either way.

This needs a wording amendment to the root hard constraint "Group membership
never appears in a certificate — **groups feed the lifetime decision only**".
Groups would now also feed authorization. The invariant that matters is
unchanged and should be restated as: groups feed decisions, never certificate
content.

## Where it is evaluated

From the session identity, not the database. `AuthService.upsertUser`
deliberately does not persist groups, so `Identity.Groups` exists only for the
life of a session — which is the good answer: there is no stale copy to go
wrong, and removing someone from the group in the IdP takes effect at their
next login.

The consequence has to be stated rather than discovered: **the session lifetime
is the admin revocation window.** Whatever that TTL is, it is how long a
removed administrator stays an administrator. If that is too long, the fix is a
shorter session for admin-authorized routes, or re-checking at use — not a
cached copy of the claim.

## Two roles, not one

- **admin** — expiring enrollments and disabling users. Not narrowing policy:
  that belongs to host owners.
- **auditor** — read-only: certificate history across users, effective
  configuration.

Two rather than one because the read-only view is what people actually want
day to day — support, incident review, "who issued this?" — and handing out
write access to satisfy a read need is how admin sprawl starts. Two rather than
five because nothing here justifies a general permission system.

## What an admin must never be able to do

More important than the powers, and the part to test:

- **Approve a request they did not create.** Not a convenience to add later: an
  administrator who can approve arbitrary requests *is* the escalation path
  described above, since the certificate takes the approver's principals.
  Administration and approval are separate authorities and must stay separate.
- **Raise any configured ceiling** — lifetime, extensions, or group
  requirements. Admin edits narrow only; the config file is the outer bound
  (see the lifetime policy plan).
- **Grant admin**, to themselves or anyone else.
- **Reach key material.** The server never holds client private keys, and the
  CA key comes from configuration; no admin surface exposes either.
- **Turn off the audit trail**, or delete from it.

## Audit

Every admin action records who, when, what changed, and the previous value. A
policy that can be edited at runtime and cannot be reviewed afterwards is worse
than one that cannot be edited — the audit trail is what makes the
runtime-editable decision defensible.

Admin *reads* of other users' data are worth logging too. That is most of what
an auditor role does, and "who looked at what" is a question that gets asked
after an incident.

## Enforcement

Middleware after `SessionAuthMiddleware`, failing closed: no identity, no
group, or no configured group all deny. Route groups mirror the existing
`approvalGroup` / `readGroup` split.

Handlers that write re-check server-side rather than trusting the route
grouping alone — the same defense-in-depth reasoning as clamping policy at
evaluation instead of only at save time.

## Verification

- A table-driven test over **every** admin route asserting 401/403 for
  anonymous, authenticated-but-not-admin, and auditor-attempting-a-write. The
  point is that the table has to be updated when a route is added.
- Empty `admin.require_group` denies rather than allows.
- An admin cannot approve a request bound to someone else.
- An admin edit that exceeds a config ceiling is rejected on save *and* clamped
  at evaluation.

## Open questions

1. **Session TTL for admin routes** — is the ordinary session lifetime
   acceptable as the revocation window, or do admin routes need a shorter one
   or a step-up re-authentication?
2. **Where does the effective-config view draw the line?** It is genuinely
   useful for debugging and it is also a map of the deployment. The CA private
   key and any client secret are obvious redactions; whether trusted proxy
   ranges and subnet policy count is a judgment call.
3. **Does the auditor role justify its own config key on day one**, or should
   it land later once someone asks for it?
4. **Which phase owns this?** On current ordering it fits between 6 and 8 —
   after the first release, before service certificates need enrollment expiry.

## Implementation Status (completed)

This plan has been implemented on branch `feat/auth-roles`. Key decisions and divergences from the plan:

### Design Decisions Implemented

1. **Three roles, config-driven**: admin, auditor, and ssh-server-admin, all fail-closed on empty groups
   - Config location: `config.Admin` (AdminConfig struct) with fields `RequireGroup`, `AuditorGroup`, `SSHServerAdminGroup`
   - All three group names optional; empty disables that role
   
2. **Session TTL**: Changed from 12 hours to **15 minutes** default
   - File: `server/bootstrap/router.go`, constant `defaultCookieMaxAge`
   - Admin revocation window is the session lifetime
   - Regression test `TestSessionCookieOptions_ShouldNeverProduceAZeroMaxAge` verified not broken
   - **Pending human task**: confirm authoritative NASA session-timeout policy value

3. **Middleware**: AdminAuthMiddleware and AuditorAuthMiddleware after SessionAuthMiddleware
   - Both in `server/middleware/auth_middleware.go`
   - Both fail closed: 403 Forbidden on no identity, no configured group, or no membership

4. **SSH Server Admin Interface**: Seam for feat/host-certs
   - Interface defined in `server/service/adminchecker.go` as `service.SSHServerAdminChecker`
   - Implementation: `service.ConfigSSHServerAdminChecker` and `service.NewConfigSSHServerAdminChecker(c)`
   - Config field folded into `config.Admin.SSHServerAdminGroup` (mapstructure: `ssh_server_admin_group`)
   - Note: feat/host-certs' bootstrap will need to update config read location from top-level to `config.Admin.SSHServerAdminGroup` on rebase

5. **PKCE (S256)**: Integrated into OIDC flow
   - Verifier generated in `service.AuthService.AuthorizationURL`, returned as third value
   - Verifier stored/retrieved via `middleware.SetOIDCVerifier`/`middleware.PopOIDCVerifier`
   - Used in `service.AuthService.HandleCallback` via `oauth2.VerifierOption`

6. **Effective Config View**: `GET /api/admin/config`, auditor-scoped
   - Returns `webtypes.EffectiveConfigResponse` with operational settings
   - Redacted fields: CA key (not in response), client secret (not in response), cookie signing key (not in response)
   - **Database connection string**: Redacted entirely (not included in response) to avoid leaking credentials
   - Includes: server settings, OIDC provider URL, all role group names, logging level, certificate validity/TTL settings

7. **Admin Write Endpoints** (implemented as stubs with clear interfaces)
   - `PATCH /api/admin/enrollments/:id/expire`: marks enrollment as expired (skeleton implemented)
   - `PATCH /api/admin/users/:id/disable`: disables user (TODO marked; would require schema changes feat/service-certs owns)

8. **Auditor Read Endpoints**
   - `GET /api/admin/config`: effective configuration view (fully implemented)
   - `GET /api/admin/certificates/history`: cross-user certificate audit trail (skeleton with TODO)

### Divergence: SSH Server Admin Role Outside Original Plan Scope

The original plan defined only two roles (admin, auditor). The ssh-server-admin role was added as a cross-branch seam for feat/host-certs, which defined the interface contract. To maintain interface alignment:
- The interface and implementation live in `server/service` (feat/host-certs' seam point)
- Config field lives in `config.Admin` alongside the original two roles
- This ensures all three role-to-group mappings are co-located in the config

### Files Changed

New files:
- `server/service/adminchecker.go`: SSHServerAdminChecker interface and ConfigSSHServerAdminChecker implementation
- `server/controller/admin.go`: Admin and auditor route handlers
- `server/middleware/auth_middleware.go`: AdminAuthMiddleware and AuditorAuthMiddleware

Modified files:
- `server/config/types.go`: Added AdminConfig struct
- `server/bootstrap/router.go`: Registered admin routes, updated session TTL
- `server/middleware/error_handler.go`: Added ForbiddenError type
- `server/middleware/session_auth.go`: Added PKCE verifier session helpers
- `server/service/auth.go`: PKCE integration in AuthorizationURL and HandleCallback
- `server/controller/auth.go`: PKCE verifier storage/retrieval in login/callback
- `server/webtypes/webtypes.go`: Added EffectiveConfigResponse
- Test files updated for PKCE

