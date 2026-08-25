
# What this is
- macOS/Linux/Windows client, invoked from `ssh_config`. Talks to the server
  over HTTPS REST, waits for certificate issuance over SSE.
- Manages ssh keypairs in files or ssh-agent; keypair is regenerated whenever
  a new certificate is needed.
- Full design context (enrollment flow, principal mapping, open questions):
  `docs/internals/design-brief.md`

# Design rules
- Viper config loading (~/.config/ssoossh.yaml | ./ssoossh.yaml), CLI
  overrides
- Never open a listening port — no loopback redirect (see
  docs/internals/invariants.md)

## CLI surface

```
ssoossh ssh login | logout | proxycommand | inspect | config
ssoossh host principals | mapping (list | add | remove)
ssoossh service enroll | retrieve
ssoossh ca
ssoossh version
```

- `ssh proxycommand` — ensures a valid cert, then relays TCP; requires an
  agent (`ssh` won't re-read changed key files, so `Match exec` + `ssh login`
  is the only mode where key files on disk work without an agent)
- `host principals | mapping` — manage the local principal mapping file
  (JSON object: account → list of principals). `host principals` implements
  sshd's `AuthorizedPrincipalsCommand` (runs as root, called on every login
  attempt, never touches the network); `host mapping list|add|remove` edits
  the mapping. Both commands accept `--file PATH` for the mapping file
  (default: `/etc/ssoossh/principals.json`). There are no host
  certificates and no server-side mappings (docs/project/decisions.md); the mapping
  is purely local.
- `ssh config` — the wiring harness: prints the `ssh_config` recipes and
  nothing else, as both its output and its long help. Declared `offline`
  (see `offlineCommander`), so PreRun skips the CA fetch and it answers with
  no server configured or reachable. It deliberately reports no settings;
  `--debug` is the single place those are printed
- `ca` — prints the CA public key for `TrustedUserCAKeys` / `@cert-authority`
- Service enrollment: `ssoossh service enroll --key <path>` (generates a keypair
  or enrolls an existing one at <path>/<path>.pub), obtaining an enrollment code.
  Later, `ssoossh service retrieve --code <code> --key <path>` redeems the code
  once per call, writing the certificate to <path>-cert.pub. The server binds
  the code to both the public key and the authorized option set; later invocations
  post only the code — never resubmit the key. Supports `--retrieve` on enroll
  to get the certificate immediately, `--force` on retrieve to bypass the local
  cache, and `--grace <duration>` to control how long a cached certificate is
  considered fresh before refreshing

## Diagnostics

Two persistent flags, both to stderr, both with an environment equivalent
for invocations whose command line belongs to `ssh` or cron:

| Flag | Environment | Prints |
| --- | --- | --- |
| `-v`, `-vv`, `-vvv` | `SSOOSSH_VERBOSE=1..3` | slog trace: steps, then requests and files, then bodies |
| `--debug` | `SSOOSSH_DEBUG=1` | the config merge chain, resolved settings, and key file paths with existence |

`--debug` implies `-v`, and is hidden from `--help` (a diagnostic aid, not
part of the advertised surface) but documented in
`docs/operations/configuration.md#diagnostics--v-and---debug` and `docs/guide/faq.md`.
Malformed values in either variable read as off — a diagnostic must never
fail a login. `-v` is what a bug report should ask for.

The `--debug` report is the **only** place resolved settings are printed.
Do not add a second command that prints a subset: that is what `ssh config`
used to be, and the two drifted. Run `--debug` on the command being
diagnosed — an offline command leaves key storage and the CA unresolved,
since it never builds either.
