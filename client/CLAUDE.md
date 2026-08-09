
# What this is
- macOS/Linux/Windows client, invoked from `ssh_config`. Talks to the server
  over HTTPS REST, waits for certificate issuance over SSE.
- Manages ssh keypairs in files or ssh-agent; keypair is regenerated whenever
  a new certificate is needed.
- Full design context (enrollment flow, principal mapping, open questions):
  `docs/ssoossh-context.md`

# Design rules
- Viper config loading (~/.config/ssoossh.yaml | ./ssoossh.yaml), CLI
  overrides
- Never open a listening port — no loopback redirect (see root
  `Hard Constraints`)

## CLI surface

```
ssoossh ssh login | logout | proxycommand | inspect | config
ssoossh host sign | renew | sync | principals
ssoossh service enroll | retrieve
ssoossh ca
```

- `ssh proxycommand` — ensures a valid cert, then relays TCP; requires an
  agent (`ssh` won't re-read changed key files, so `Match exec` + `ssh login`
  is the only mode where key files on disk work without an agent)
- `host principals` — implements sshd's `AuthorizedPrincipalsCommand`. Runs
  as root, called on every login attempt, **never touches the network** —
  answers from whatever `host sync` last wrote. Cache staleness is the host
  admin's call (file mtime / `host sync` exit status), not this command's.
- `host sync` — pulls the principal mapping from the server and writes it
  locally; purely local mapping files remain supported too
- `ca` — prints the CA public key for `TrustedUserCAKeys` / `@cert-authority`
- Service enrollment: keypair is either operator-supplied (server never sees
  the private half) or client-generated; the server returns an enrollment
  code bound to both the public key and the authorized option set, and later
  invocations post only the code — never resubmit the key
