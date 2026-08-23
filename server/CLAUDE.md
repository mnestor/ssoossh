
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
  `docs/dev/ssoossh-context.md`

## Certificate Rules

- Three types: **User** (interactive SSH; principals from OIDC claims + LDAP
  account identifiers), **Host** (server identity — `host sign` for first
  issuance via OIDC approval chain, `host renew` afterward authenticated by
  the existing valid host cert), **Service** (non-interactive, a User-type
  cert)
- Group membership never appears in a certificate — groups feed the lifetime
  decision only (see root `Hard Constraints`)
- `verify-required` is never used; `no-touch-required` only applies to
  enrolled `sk-` keys on the service path, never client-generated keys

## Architecture

- `bootstrap/` - server startup and graceful shutdown
- `cmd/` - entrypoint from `cmd/server/` using spf13/cobra
- `config/` - config structs, defaults, and spf13/viper setup
- `controller/` - gin router methods and structs for each type of
  controller to hold services needed for controller to work. maybe break this up
  into sub folders for the controller types.
- `frontend/` - simple framework for embedding frontend
  html,js,images,css
- `logging/` - everything loging setup for server
- `model/` - database structs to match every table in the database with gorm
  struct tags
- `middleware/` - gin middleswares
- `resources/` - database migrations for both sqlite and postgres
- `service/` - services used by controller, keeps separation of
  controller and model interactions
- `utils/` - utility modules that don't fit any other other locations
  under `server/`

## Security-Critical Code Rules

- Never log sensitive data (passwords, tokens, card numbers)
- Validate all inputs at function boundaries
- Require explicit authorization checks before data access
