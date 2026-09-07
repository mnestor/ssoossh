
## What This Project Is

- OIDC (+ optional LDAP enrichment) and API endpoint service that runs as a
  systemd service listening on a TCP or file socket
- Decides certificate contents, signs public keys, serves the web UI for
  approval/confirmation/per-user certificate history
- API endpoint provides methods to frontend and client pieces
- CA key sources: inline SSH key (ssh_key), PKCS#11/HSM token (hsm),
  or registry-fetched keys (split mode / multi-instance); signing always
  happens behind a keysource interface, never hardcoded to any single backend
- Full design context (open questions, future plans):
  `https://mnestor.github.io/ssoossh/internals/design-brief/`

## Certificate Rules

- Four types: **User** (interactive SSH; principals from OIDC claims + LDAP
  account identifiers), **Service** (non-interactive, a User-type cert),
  **PAM** (a User-type cert issued for a local PAM authentication),
  **Console** (a User-type cert for an interactive console login, approved
  in the web UI by a typed code;
  https://mnestor.github.io/ssoossh/concepts/console-flow/)
- There is no host certificate type, and no secure host verification to
  justify one (https://mnestor.github.io/ssoossh/project/decisions/). `ssoossh host` is local
  principal-mapping tooling only. Do not add a host type
- Group membership never appears in a certificate — groups feed the lifetime
  decision only (see https://mnestor.github.io/ssoossh/internals/invariants/)
- `verify-required` is never used; `no-touch-required` only applies to
  enrolled `sk-` keys on the service path, never client-generated keys

## Architecture

- `bootstrap/` - server startup and graceful shutdown
- `certmsg/` - the signing-job message types carried between the web tier
  and the signer
- `cmd/` - the `ssoosshd` command tree (`serve`, `serve api`, `sign`,
  `ldap probe`, `version`) using bep/simplecobra; the binary's entrypoint is
  `cmd/ssoosshd/main.go` at the repo root
- `config/` - config structs, defaults, and spf13/viper setup
- `controller/` - gin router methods and structs for each type of
  controller to hold services needed for controller to work. maybe break this up
  into sub folders for the controller types.
- `dbtime/` - time handling that is the same across sqlite and postgres
- `frontend/` - simple framework for embedding frontend
  html,js,images,css
- `job/` - the scheduler and its background jobs
- `logging/` - everything loging setup for server
- `mail/` - SMTP delivery and the notification templates
- `model/` - database structs to match every table in the database with gorm
  struct tags
- `middleware/` - gin middleswares
- `notify/` - notification kinds, per-user preferences, and fan-out
- `openapidoc/` - the swag-generated OpenAPI document
- `pubsub/` - the in-process and NATS message buses
- `resources/` - database migrations for both sqlite and postgres
- `service/` - services used by controller, keeps separation of
  controller and model interactions
- `signer/` - CA signing: config-file key or PKCS#11 token
- `testutil/` - helpers shared by server tests
- `utils/` - utility modules that don't fit any other other locations
  under `server/` (`errorresponses`, `paging`, `tracing`)
- `webtypes/` - the JSON request/response types the frontend's TypeScript is
  generated from

## Security-Critical Code Rules

- Never log sensitive data (passwords, tokens, card numbers)
- Validate all inputs at function boundaries
- Require explicit authorization checks before data access
