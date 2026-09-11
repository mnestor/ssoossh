# SELinux checks

Tests that cannot run in CI, because they need a real host.

## Why not CI

SELinux policy belongs to the host kernel, and a container shares it. What
these check is the domain transition a systemd-started daemon makes when it
executes something -- and a daemon launched from an admin's shell inherits
that shell's unconfined domain, so it exercises nothing. There is no
container arrangement that answers the question honestly.

Run them on a throwaway VM: AlmaLinux 9, Rocky 9 or Oracle Linux 9, `@core`
is enough, SELinux left at its default `Enforcing`.

## `authorized-principals-label.sh`

Asks whether the move of the client binary from `/usr/local/bin` to
`/usr/bin` changed anything under SELinux, and whether the compatibility
symlink left at the old path behaves differently from the real one.

The question is real rather than theoretical: the two directories do not
share a default file context on RHEL, so the domain `sshd_t` transitions to
when it runs `AuthorizedPrincipalsCommand` can differ between them -- and
the symlink keeps both paths live for two releases.

```bash
sudo dnf install -y ./ssoossh-client-*.rpm
sudo test/selinux/authorized-principals-label.sh
```

It needs no ssoossh server. `ssoossh host principals` answers from a local
file and never opens a socket, so the script builds a throwaway CA with
`ssh-keygen`, issues a certificate carrying a principal that is deliberately
**not** the account name, and logs in over `localhost`. Because the
principal does not match the account, the only way the login can succeed is
if `sshd` ran the command and used its answer -- so success is evidence the
command ran, not evidence of `sshd`'s own fallback.

What it changes and puts back: one `sshd_config` drop-in, validated with
`sshd -t` before any reload, removed by an `EXIT` trap. It reloads rather
than restarts, so existing sessions survive. It does not touch the system CA
trust, any real user's keys, or an installed principals map.

It refuses to run on a Permissive host. Permissive logs denials without
acting on them, so a clean result there would prove nothing about an
enforcing one.

### Reading the result

- **Both paths clean** -- the path move introduces no SELinux regression and
  the package needs no file-context rules.
- **`sshd -t` refuses the configuration** -- that is a result too. `sshd`
  rejects a command whose path is writable by group or other, which is the
  original reason the binary moved.
- **An AVC** -- capture it whole. The `scontext` and `tcontext` name the
  domain and the file label involved, which is what decides whether the fix
  is a file-context rule (`semanage fcontext` plus `restorecon`) or
  something larger. Note that a policy module, as shipped by
  `pam-ssoossh-selinux`, is a different mechanism from file-context rules
  and not automatically the right shape here.

### Status

**Not yet run.** No enforcing EL host has been available to anyone working
on this, so the answer is unknown rather than assumed. That is the reason
this exists as a script rather than a paragraph asserting the move is fine.

Worth being precise about what "no host" means, because the trap here caught
people already: an `almalinux:9` container on a non-SELinux host is not an
EL9 SELinux environment. Containers share the host kernel's LSM, so a
container on a Debian or Ubuntu machine has no SELinux at all -- `semodule`
will load a policy module and report success, and nothing will ever enforce
it. A result from that arrangement describes packaging mechanics and says
nothing about policy behaviour.

A VM would also settle a narrower question on the ssoossh-pam side. That
project's `selinux/pam_ssoossh.te` records its allow rule as confirmed on a
real enforcing Oracle Linux 9.8 host with the targeted policy, and re-run in
permissive mode to establish that nothing denies after the `name_connect` --
which is what says one rule is the whole answer for a domain rather than
only its first wall. What that confirmation used was `semodule -i` by hand,
which installs at the default priority 400; the rpm installs at priority 200,
the vendor slot, so that a site's own module of the same name still wins.
The rule is confirmed. The packaged delivery of it -- scriptlet, vendor
priority, upgrade path -- is not.

So one disposable AlmaLinux 9 or Oracle Linux 9 VM, SELinux left at its
default `Enforcing`, would answer both. It is the cheapest way to turn two
documented caveats into two facts.

(The ssoossh-pam detail above is reported from that project rather than
verified here; this repository has no visibility into it.)
