# Bootstrap identity echo for initial setup

**Status:** approved 2026-10-21, issue #221. Being built on `feat/221-bootstrap-echo`.

**Anchors verified at:** `c861dd32` (2026-10-21). `file:line` references drift; re-check before relying on one.

`ssoosshd oidc echo` is a host command, run as the service account, that mints a one-time link to the existing identity echo page. Whoever opens it signs in and sees their own claims, the mapping and suggested YAML, with no admin role and no working mapping needed, and the server stores no claims.

## Problem

Identity echo cannot help at first setup, because it needs a sign-in that already works and an admin that is already detected.

`authentication.fields` maps identity fields to claim names (`server/config/types_oauth.go`, `OAuthFields`). The defaults are `sub`, `preferred_username`, `name`, `groups` and `email`. Operators often do not know what their provider sends: which claim is stable (Entra ID's `sub` is per application; the stable one is `oid`), whether groups arrive at all, and what could feed `extra`.

Identity echo (`server/controller/identityecho.go`) already decodes the ID token and suggests YAML (`claimSuggestions`), but:

1. **Admin depends on the mapping.** `admin.require_group` is empty by default, which disables admin (`server/config/types.go`, `AdminConfig.RequireGroup`). Setting it needs a group name the `groups` claim delivers, which is what the operator does not know yet.
2. **A wrong mapping fails the sign-in.** `HandleCallback` (`server/service/auth.go`) refuses a login whose subject or username claim is missing, so there is no session to reach any page with.
3. **Errors before claims.** A wrong client secret, issuer or redirect URI fails at code exchange or verification, before any claims exist. Today that is only in the server log.

The authority that does exist at setup is access to the server host, the binary and the config. The bootstrap echo uses that.

## The command and its flow

The operator runs one command on the server host, opens the link it prints, and signs in once.

```
sudo -u ssoossh ssoosshd oidc echo [--ttl 15m]
```

1. **Mint.** The command loads the same config as `serve` and opens the server's database, as `ssoosshd ldap sync` does (`server/cmd/ldapsync.go`). It stores a SHA-256 hash of a fresh 128-bit code with `expires_at` and `requested_by` (OS user and hostname), audits `auth.echo_code_minted`, and prints `<public_url>/identity/echo#<code>`. The code rides in the fragment, so it never reaches an access log or a proxy.
2. **Open.** The page reads the code from the fragment, drops it from history, and posts it to a new unauthenticated endpoint. The server checks the hash and expiry, consumes the code (single use, a conditional update, so the first instance wins), and returns the authorization URL with `prompt=login`, the same one the admin echo start returns (`EchoAuthorizationURL`).
3. **Sign in.** The callback takes the existing echo branch: `EchoCallback` (`server/service/auth.go`) verifies the ID token exactly as a login does and returns the claims. No session, no users row, no login event. **No mapping runs**, so a mapping that would fail a login does not matter here.
4. **Show.** The claims, the configured mapping and the suggestions reach the page in the URL fragment, as the admin echo does today (`echoRedirectURL`). The page offers **Copy YAML** and **Download**, built in the browser, so the file you asked for still exists and the server still writes nothing.
5. **Errors before claims.** If code exchange or token verification fails (wrong client secret, unknown redirect URI, wrong issuer), the page shows the error instead of a generic failure, and the server records it on the code row so the waiting command prints it too.
6. **Report.** The command waits until the code is used or expires and prints the outcome: `used at <time> from <observed address>`, the error from step 5, or `expired`. It never prints claims.

## What the page shows

The page is the existing echo result, plus the setup answers the admin echo does not give today; both modes get the additions.

| Section | Contents |
| --- | --- |
| Claims | The decoded ID token, keys sorted; protocol claims (`iss`, `aud`, `exp`, `nonce`, `at_hash` and the rest of `isProtocolClaim`) grouped apart |
| Mapping | Each `authentication.fields` entry: configured claim, resolved value, or `missing` / `wrong shape`; the same resolution code as `HandleCallback`, shared, not copied |
| Would sign in | Yes, or the first error `HandleCallback` would return (for example the username claim missing) |
| Suggestions | Today's `claimSuggestions`, plus candidates for `subject` and `username`, each string-array claim as a `groups` candidate, and the groups claim's values to pick `admin.require_group` from |
| Not mappable | Claims holding nested objects; the mapper reads top-level keys only (`scalarClaim`, `stringSliceClaim`, `extraClaims` all index `claims[key]`) |
| Copy and download | The suggested `authentication` and `admin` YAML, and the whole result as a file, generated in the browser |

Never sent anywhere: the raw ID, access or refresh token, the authorization code or the PKCE verifier.

**Effect on the admin echo.** Its start endpoint, admin check and session handling stay as they are. It does change in two places: its result page gains the rows above (the shared result view), and its callback shows exchange errors on the page instead of a generic failure.

## Why a host-minted code

Host access is the only authority that exists before the mapping works, and a single-use code hands exactly that much of it to one browser.

- **Not a config switch.** An `authentication.bootstrap_echo: true` key would open the echo to anyone until someone removes it, needs a restart, and is easy to forget on a live server.
- **Not over HTTP.** Nothing reachable over HTTP can mint a code; only someone who can read the config and open the database can. The code grants no authority: it shows the person who uses it their own claims and establishes nothing, so the invariant that nothing over HTTP widens authority holds.
- **Shared database as the channel.** The same pattern as `ldap sync`; every `serve api` instance can check a code without NATS or a new socket.
- **Still useful after setup.** An admin debugging one user's claims can mint a code and send it to that user, who sees only their own claims.

## Alternative considered: server-side capture to a file

The first draft armed a capture of the next sign-in and had the command write the claims to a file; it was dropped for the browser echo.

- It captured whoever signed in next unless `--match` named them, so on a live server it could record a stranger's claims.
- The claims sat in plain text in the database until the command read them, so a backup in that window held them.
- It hooked into `HandleCallback`, the real login path, where the echo needs no change.

The file is kept: the page's **Download** builds it in the browser.

## Privacy and security

The code is the whole risk: whoever holds it can run one echo, which shows them their own claims and nothing else.

- **Nothing stored.** Claims never touch the server's disk or database; they reach the page in the fragment, as the admin echo does today.
- **The code.** 128 bits from `crypto/rand`, stored only as a SHA-256 hash, single use, 15 minutes by default, in the URL fragment so no access log or proxy sees it. One live code at a time: minting another replaces the first.
- **Only verified claims.** `EchoCallback` runs the same signature, audience, expiry and nonce checks as a login.
- **The new endpoint.** The code-redeem endpoint is unauthenticated, so it sits behind the existing rate limiter and returns the same error for a wrong, used or expired code.
- **Audit.** Minted, used and expired events carry the code id, the requester and the observed source address. No claim values, as the admin echo records none today.
- **FIPS.** Nothing new: `crypto/rand` and SHA-256.

## Changes and tests when built

The work is one table, one command, one endpoint, one public page and docs; the echo flow itself is reused.

- `server/service`: mint, redeem and expire codes; record pre-claim errors on the row; move claim resolution and `claimSuggestions` out of the controller and extend them (subject, username, groups and admin group candidates, would-sign-in, not-mappable).
- `server/model` and `server/resources`: an `identity_echo_codes` table, migrations in both SQLite and Postgres trees.
- `server/controller`: `POST /api/identity/echo/redeem` beside the admin start; the callback's echo branch sends a bootstrap echo to the public result route and shows exchange errors there. OpenAPI and generated types regenerated.
- `server/cmd`: `oidc echo` (simplecobra), man page and CLI docs regenerated.
- `frontend`: a public `/identity/echo` route reusing the admin echo's result view, with Copy YAML and Download. UI work follows `frontend/DESIGN.md`.
- Audit actions `auth.echo_code_minted`, `auth.echo_code_used`, `auth.echo_code_expired`.
- `user-docs`: a "find out what your provider sends" step in getting started, linked from the `authentication.fields` reference.
- `SSOOSSH.md`: one Server bullet beside identity echo, on your say-so.

Tests:

- should show the claims when a valid code is redeemed and the ID token verifies
- should show the claims when the configured username claim is missing
- should refuse a code that is wrong, used or expired, with the same error
- should consume a code once when two instances redeem it at the same time
- should show and record the exchange error when the client secret is wrong
- should establish no session and write no users row
- should store only the code's hash
- should replace the live code when a second one is minted
- should flag nested claims as not mappable
- should build the downloaded file in the browser without a server request

## Open questions

- [ ] **Command name.** `ssoosshd oidc echo` or `ssoosshd identity echo` (matching the admin page's name)?
- [ ] **Session for the round trip.** The admin echo keeps its state, nonce and PKCE verifier in the admin's session (`SetOIDCEchoState`). A bootstrap echo has no session, so it needs a short-lived pre-auth cookie; check the login flow already sets one before adding another.
- [ ] **Access token and userinfo claims.** ssoossh reads only the ID token, but "groups is in the access token, not the ID token" is a common provider mistake. Proposed: decode a JWT access token and list its claims under Not mappable with that note; skip userinfo (an extra provider call).
- [ ] **Nested claim paths.** `realm_access.roles`-style claims would show but cannot be mapped without dotted-path support in `authentication.fields`. Separate proposal.
- [ ] **Point to it from `config check`.** Once #205 lands, `config check` could suggest `oidc echo` while `authentication.fields` is at its defaults and `admin.require_group` is empty.
