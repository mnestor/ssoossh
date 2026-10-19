## Approach

- No emojis or em-dashes.
- Do not guess APIs, versions, flags, commit SHAs, or package names. Verify by reading code or docs before asserting.

## Stack

- Go 1.27+
- Standard library preferred
- Packages: gin, spf13/viper, spf13/cobra (client through bep/simplecobra), gorm with gorm.io/driver/postgres and glebarez/sqlite (pure Go, no cgo)

## Monorepo Structure

| Path | Purpose |
| --- | --- |
| `/cmd` | entrypoints only: `ssoossh` (client), `ssoosshd` (server), `ssoossh-nss` (NSS daemon, Linux only) |
| `/server` | ssoosshd server code |
| `/client` | client code |
| `/nssd` | ssoossh-nss, the host-side systemd userdb daemon; imports only `/internal`, never `/server` or `/client` |
| `/internal` | internal code shared between server and client |
| `/frontend` | pnpm/SvelteKit web UI, embedded in ssoosshd by `server/frontend`; see `frontend/DESIGN.md` for the design system |
| `/test` | e2e, load, migration, packaging and Postgres suites, plus shared test helpers |
| `/docs` | developer docs, proposals (`docs/proposals/`), OpenAPI spec, man pages, wire contract |
| `/user-docs` | user documentation site (Astro Starlight) |
| `/deploy` | systemd units, docker-compose, HSM simulator, macOS deployment files |
| `/packaging` | packaging that goreleaser has no pipe for |
| `/scripts` | build, check and release helpers |

## Quality Standards

- Correctness > Maintainability > Performance
- Short and complete comments on all functions or groups of code

## UI work

Read frontend/DESIGN.md before creating or changing any UI, and take every colour, size, spacing and radius from its tokens.

@SSOOSSH.md
