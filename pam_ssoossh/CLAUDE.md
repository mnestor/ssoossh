
# What this is
- linux pam module, `auth` group, `sudo`/`su` only
- generates an ephemeral ssh keypair, requests a certificate (or gets an
  error), then validates it — then discards everything (nothing retained,
  no nonce needed: the per-attempt keypair provides the freshness)
- Full design context: `docs/ssoossh-context.md`

# Design rules

## The four checks (all required, in order)
1. signed by the expected CA
2. public key in the certificate matches the key just sent
3. principals identify the authenticating user
4. inside the validity window — tolerance is a module setting; clock skew is
   the real constraint, so log observed skew on failure

## Notes
- `AuthorizedPrincipalsCommand` is irrelevant here — that's an sshd
  directive, not something pam_ssoossh implements or calls
- `pam_ssh_agent_auth` is rejected as an approach because it requires agent
  forwarding
