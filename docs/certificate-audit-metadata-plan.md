# Certificate audit metadata: backend follow-ups

During the frontend redesign (dashboard/history/certificate-detail views),
three real gaps turned up in what's captured and persisted about a
certificate request and its decision. Each was verified by reading the
actual code, not assumed. None exist today. The new frontend views already
have UI for all three — they render honest placeholders (an em-dash, or a
clearly marked sample value) until the backend work below lands; no
fabricated data ships.

## 1. Decision actor + source IP

`Approve`/`Deny` never persist who decided or from where.

- `server/service/certrequest.go:299` — `Approve(ctx, requestID, identity)`.
  `identity` is used for principal resolution only (see
  `resolvePrincipals`), never persisted as the decision's actor.
- `server/service/certrequest.go:559` — `Deny(ctx, requestID)` doesn't even
  take an identity.
- `certificate_requests` (both migrations) has only `resolved_at`, a bare
  timestamp — no actor column, no IP column.
- Request-time IP capture already has a working pattern to mirror:
  `server/controller/certrequests.go:107` —
  `params.SourceIP = g.ClientIP()`.

Needed:

- Migration adding `decided_by` (identity/user id) and
  `decided_source_ip` columns to `certificate_requests`.
- `Approve`/`Deny` signature changes to accept and persist both.
- Controller wiring to capture `g.ClientIP()` at decision time, the same
  way request creation already does.
- Expose both through whatever response type backs the new
  dashboard/history/detail views (extend `webtypes.RequestDetailResponse`
  or equivalent).

## 2. User-type requests: local user + hostname

`model.CertificateRequest.Hostname` is host-type only; `.Username` is
PAM-type only and already correctly captures the local account being
authenticated (its doc comment at `server/model/certificate_request.go:30-35`
confirms this — no change needed there). Nothing captures the client's
local OS user or hostname for **user-type** requests.

- `NewCertRequestParams` (`server/service/certrequest.go:31-38`): `Type`,
  `PublicKey`, `Hostname` (host-only), `Username` (PAM-only), `SourceIP`,
  `RequestedOptions` — no field for user-type client identity.
- Confirmed nothing in `client/cmd` builds or sends this today.

For a user-type cert there is no way to request one except via the local
client — local_user@host **is** the requester identity, not optional
extra context.

Needed:

- Client-side capture (`os/user.Current()`, `os.Hostname()`) at
  user-type request time.
- A wire/API field to carry local username + hostname on the
  create-request payload.
- A new `NewCertRequestParams` field + DB column (mirroring the shape
  `Hostname`/`Username` already use for the other types).
- Surface through `webtypes.RequestDetailResponse`.

## 3. Registered IPs (all local interface addresses)

The client should send every IP it has registered on its own network
interfaces, not just rely on the single source IP the server happens to
observe. Matters most for service certs — a service host may be reachable
on several interfaces/addresses, and future source-address policy or
routing decisions need the full set, not whichever one the request
happened to arrive from.

- Grepped `client/` and `server/` for any existing enumeration of local
  interfaces (`net.Interfaces`, `InterfaceAddrs`, any IP-list concept) —
  none found.

Needed:

- Client-side collection via `net.Interfaces()` / `net.InterfaceAddrs()`
  at request time.
- A new wire field, likely `[]string`.
- A new `NewCertRequestParams` field + DB column — probably JSON-encoded,
  mirroring how `requested_options` is already stored as a JSON-encoded
  `TEXT` column.
- Surface through `webtypes.RequestDetailResponse`.

## 4. No detail endpoint for an already-issued certificate

Discovered while building the frontend's certificate-detail view (clicking
a row in the dashboard or history list). `GET /api/certs`
(`listCertificates` → `CertificateResponse`) — what both the dashboard and
`logs/me` call — returns a thin audit-trail record: `id`, `type`,
`serial_number`, `key_id`, `principals` (a single string, not a list),
`public_key_fingerprint`, `hostname?`, `issued_at`, `expires_at`. No
`source_ip`, no extensions/critical-options, no decision actor or IP (see
items 1 and 3 above for those). There is no `GET /api/certs/:id`.

The only per-item detail endpoint that exists,
`GET /certs/requests/:id` → `RequestDetailResponse`
(`getRequestDetail` in `frontend/src/lib/api/endpoints.ts`), is for a
still-**pending** request, not a resolved one — and it has claiming side
effects that make it unsafe to repurpose: its own doc comment says *"This
is also the call that binds the request to the caller server-side — the
first authenticated view claims it... A second person loading the same
page gets 403 here rather than after clicking approve."* Reusing it for
browsing an already-resolved historical certificate would be wrong.

The frontend's certificate-detail view ships now using only the real
`CertificateResponse` fields above. Showing the richer request-time detail
(extensions/critical-options granted vs. requested, source IP, decision
actor + IP) for an *already-issued* certificate needs a real design
decision, not just an endpoint: does that detail get retained past
resolution at all (worth checking against `docs/signing-pipeline.md`'s
"certificates are ephemeral" note — that's about the signed cert bytes,
not necessarily this metadata, but the retention policy for the
originating `certificate_requests` row past resolution isn't otherwise
documented), and if so, a non-claiming `GET /api/certs/:id` (or an
expanded `/certs` response) needs to expose it.

## 5. Runtime branding config endpoint

Discovered while wiring up deployment branding (org name, logo, login
consent notice) in the frontend. These need to be operator-configurable
without rebuilding the frontend from source: this app is prerendered
(`adapter-static`) and the compiled static files are baked directly into
the `ssoosshd` server binary/distribution, so a deployment operator
installing a release never touches the frontend build — a build-time env
var (`VITE_*`) can never reach them. Config has to be server-side and
fetched by the frontend at runtime.

There's already an exact precedent for this shape: `server/controller/ca.go`
registers `GET /api/ca` on the `apiGroup` without the `sessionAuth`
middleware other controllers use (`server/bootstrap/router.go:308-313`),
explicitly commented "Public by design — it is a public key." A branding
endpoint needs the same treatment — public by design, since the login
notice specifically has to render *before* authentication.

Needed:

- A new config section (new `server/config/types_branding.go`, following
  the existing per-domain `types_*.go` + `_defaults.yaml` pattern already
  in `server/config/`) holding org name, logo URL (or a logo asset
  reference — decide how the image itself gets served), and login notice
  text — all optional, empty by default.
- A new unauthenticated controller mirroring `ca.go`'s shape, serving
  those values as JSON.
- Wiring into `router.go`'s `apiGroup` alongside the existing
  `NewCaController` registration.

The frontend already fetches this at app load (same pattern as the
existing `session.load()`) and fails closed — treats a 404/error as "no
branding configured" — so it works correctly standalone right now and
will pick up real values automatically once this endpoint ships.

## Out of scope here

Also raised and deliberately deferred during the design pass, not covered
by this plan: an admin host-enrollment flow, per-host certificate policy
settings, and "ownership changeability" pages for service/host certs
(reassigning a key or enrollment when the owning user leaves or is
disabled, so automation keeps working). None of these are designed yet.
