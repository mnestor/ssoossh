## What This Project Is

**ssoossh** (pronounced *sue-sssh*) — SSO for SSH. Users authenticate via OIDC
and receive short-lived SSH certificates instead of managing long-lived SSH
keys. Self-hosted, homelab-friendly. Early development. Reference deployment
uses pocket-id (OIDC) and lldap (LDAP).

- ssoosshd - webserver using gin, SSE for clients, signs ssh keys sent by
  clients after authorization; optionally enriches identity from LDAP
- ssoossh - client (macOS/Linux/Windows) that generates ssh keys, asks the
  server to sign them, and manages the keys and signed cert in ssh-agent or
  files
- pam_ssoossh - linux pam module (`auth` group, `sudo`/`su` only) that
  generates an ephemeral keypair, requests a certificate, validates it, then
  discards everything

Full design context (certificate types, lifetime policy, enrollment flow,
open questions, future plans): `docs/ssoossh-context.md`.

## Hard Constraints

These are cross-cutting security invariants — any change touching auth, cert
issuance, or the client/server transport must preserve them.

- The server never receives private keys. The client sends private keys
  nowhere except the local ssh-agent or a local file.
- The client never opens a listening port. No loopback redirect; the browser
  lands on the server and the client learns the outcome over its SSE stream.
- Server config is the outer bound on every option. Client request asks, web
  UI narrows/adjusts, server config gates. Options the deployment doesn't
  permit are trimmed (not rejected) and shown in the web UI before approval.
- Group membership never appears in a certificate. Groups feed the lifetime
  decision only.
- `verify-required` is not used at all. `no-touch-required` is not offered
  for client-generated keys — only relevant for enrolled `sk-` keys on the
  service path.

## Stack

- Go 1.26+
- Standard library preferred
- Packages: gin, casbin, spf13/cobra spf13/viper, pgxpool, gorm, sqlite

# Monorepo Structure

**This project is being rewritten to a new layout.** Target structure:

| Path | Purpose |
| --- | --- |
| `/cmd` | entrypoints only: `ssoossh` (client), `ssoosshd` (server), `pam_ssoossh` (pam) |
| `/server` | ssoosshd server code |
| `/client` | client code |
| `/pam_ssoossh` | pam code |
| `/internal` | internal code shared between server, client, and pam |
| `/pkg` | source copied in from other modules, when copying (not importing) was needed |
| `/frontend` | npm/SvelteKit — Web UI served by the app module |

`server/` holds bootstrap, controllers, and models for ssoosshd. `internal/`
holds anything shared across `server/`, `client/`, and `pam_ssoossh/` (e.g.
the version module).

## Quality Standards

- Correctness > Maintainability > Performance
- Short and complete comments on all functions or groups of code

## Git Conventions

- Branch: `feat/`, `fix/`, `chore/` prefixes
- Commit format: conventional commits
- PRs require passing CI before merge
- Keep PRs focused — one concern per PR
