# Phase 7: Deployment stack, documentation, and the rehearsal

**Status: planned.** Part of [release-plan.md](release-plan.md).

## Goal

A stranger can install ssoossh from the published artifacts, following the
documentation alone, and end up with working SSH login and working `sudo`.

This is the phase that decides whether the release is a product or a set of
binaries.

## The compose stack and the harness are different jobs

Delivery phase 6 flagged the risk of building this twice, and it is worth
restating because both exist by the time this phase starts:

- **The harness** (phase 2) is the PR gate. Fast, hermetic, in-process OIDC
  provider, no Docker. It answers "did this change break issuance?"
- **The compose stack** (this phase) is the deployment rehearsal, with the
  *real* pocket-id. It answers "does a fresh install work against a real
  identity provider?", which the throwaway provider deliberately does not.

The stack does not belong in the merge gate. It is minutes slower, has more
moving parts, and tests somebody else's software. Run it by hand before
cutting a release, and in CI on a schedule if at all.

## Work

### 1. Build the compose stack

None exists. `docker-compose.yml` today is dev-only: two containers that
`sleep`-loop the devcontainer image plus a tinyproxy, with postgres entirely
commented out. pocket-id and lldap appear only in prose across the docs.

Services:

- `ssoosshd` with seeded config and a seeded CA keypair.
- **pocket-id** as the OIDC provider, pre-configured with a client and a test
  user.
- A **target sshd** container trusting the CA, for the login hop.
- A **sudo target** container with the PAM module installed and configured.
  New for this release, and the one that exercises phase 5 against a real PAM
  stack rather than a direct call.
- lldap is not needed, since LDAP enrichment is release 2, but leave a slot.

This is what makes the OIDC flow testable by anyone who clones the
repository, which is currently only possible by pointing at the author's real
deployment.

Docker-outside-of-docker from phase 3 is what lets this run from inside the
devcontainer.

### 2. Deployment documentation, user certificate path

- systemd unit for `ssoosshd`.
- CA key generation and the `ssh_key` config entry.
- sshd configuration: `TrustedUserCAKeys`, with `ssoossh ca` as the way to
  produce it.
- `ssh_config` recipes for both invocation modes: `Match exec` with `ssoossh
  ssh login`, and `ProxyCommand`. Note that `ProxyCommand` requires an agent,
  because `ssh` will not re-read a changed key file.
- OIDC provider setup against pocket-id, matching the compose stack.
- Reverse proxy and TLS, including `http.public_url` and
  `http.trusted_proxies`. The end-to-end work found `public_url` to be the
  single most likely cause of a confusing failure, since the OIDC redirect
  URI and the CSRF origin check are both derived from it.

### 3. Deployment documentation, PAM path

New for this release.

- **PAM stack configuration** for `sudo` and `su`: the `auth` group, where
  the line goes relative to the existing modules, and the control flag.
- **Module arguments**: `server`, `trusted-ca-file`, `debug`,
  `insecure-skip-verify`, and the skew tolerance added in phase 5.
- **The lockout warning, prominently.** Editing `/etc/pam.d/sudo` wrongly
  costs you `sudo` on that machine. The runbook must say: keep a root shell
  open in another terminal while editing, test in that second terminal, and
  know how to revert. This is not optional documentation. It is the most
  dangerous thing this product asks anyone to do.
- **What happens when the server is unreachable**, and how the control flag
  chosen in phase 5 item 4 determines whether `sudo` still works. An operator
  needs to make this choice knowingly.
- Clock synchronization, because phase 4's short lifetime and phase 5's skew
  tolerance are the two numbers that make PAM fail intermittently when NTP is
  not running.

### 4. Configuration reference

`ssoosshd.yaml.default` and `ssoossh.yaml.default` are annotated samples and
are the closest thing to a configuration reference. Confirm they match the
code, including phase 4's new `cert_options.pam` section.

The end-to-end work already found two config documentation bugs by hand, one
of them `authentication:` being documented as a subsection of `http:` when it
is top level. Prose that disagrees with the parser costs more than no prose.

### 5. Getting started

The document a stranger actually reads. One path, shortest to a working
login, with everything optional removed. Link the reference material from it
rather than into it.

If it takes more than one page to get a certificate, the product has a
problem this documentation cannot fix, and finding that out is part of the
point of writing it.

### 6. Release notes

- What the release contains: user certificates and PAM.
- What it deliberately does not: service certificates, host certificates,
  LDAP, admin roles, macOS. Say so plainly. Service and host commands should
  fail closed with a clear error rather than hanging, and the notes should
  set that expectation before someone hits it.
- The revocation position, stated rather than implied. Every certificate in
  this release is short-lived, so "they expire faster than a revocation list
  could be distributed" is straightforwardly true here.
- The glibc floor for the PAM module, from phase 3.

### 7. Run the rehearsal, then fix what it finds

Budget for this. Delivery phase 6 was explicit that the first release is
where process problems surface and that they are cheap to fix now, and that
remains true with PAM in scope.

Expect the documentation walk to send work back into phases 4, 5, and 6. A
rehearsal that finds nothing usually means it was performed by the person who
wrote the instructions.

## Exit criteria

- A fresh machine can be brought up from the documentation alone, reaching
  both a working `ssh login` and a working `sudo`.
- `ssh login` works against the compose stack from a clean checkout.
- The compose stack completes both loops against a real pocket-id.
- The release notes state the scope, the deferrals, and the glibc floor.

## Verification

- **Documentation walked by someone who did not write it**, on a clean
  machine, from the published artifacts rather than a local build. This is
  the verification that matters; the rest are prerequisites for it being
  meaningful.
- Both loops on the compose stack, by hand.
- A deliberate misconfiguration of `/etc/pam.d/sudo` on the sudo target,
  recovered by following the runbook's revert instructions. If the recovery
  path is not tested, it is not documented.
- The service and host client commands fail with a clear message naming the
  release they are coming in, not a stack trace and not a hang.

## Explicitly not in this release

Repeated here because release notes are where people look, and the full
reasoning is in [release-plan.md](release-plan.md):

- Service and host certificates.
- LDAP enrichment and account status.
- Admin and auditor roles.
- Certificate lifetime and source-network policy.
- macOS artifacts.
- Multi-instance safety and the signer process split.
- Client-side TLS pinning.
- Console-login PAM module and QR-code approval.
