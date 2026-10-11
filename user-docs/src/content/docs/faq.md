---
title: Adoption FAQ
description: ssoossh adds a way to authenticate. It takes away nothing a host already accepts unless you configure it to.
---

Most of the questions on this page have the same answer: **ssoossh adds to
what you have.** Every piece of it -- the CA line in `sshd_config`, the
principals command, each `pam_ssoossh` line -- is one more way to
authenticate, next to the ones a host already accepts. Existing keys,
passwords, tokens and PAM stacks keep working. Turning any of them off is a
separate decision, made by you, one host and one service at a time.

Questions for a particular role are in the [user FAQ](/ssoossh/guides/faq/),
the [host admin FAQ](/ssoossh/hosts/faq/) and the
[operator FAQ](/ssoossh/operations/faq/).

## SSH logins

### Do existing `authorized_keys` files keep working?

Yes. Adding `TrustedUserCAKeys` changes nothing about the keys `sshd` already
accepts. For each key a client offers, `sshd` checks the `AuthorizedKeysFile`
entries, then `TrustedUserCAKeys`, then `AuthorizedKeysCommand`, and the first
one that accepts the key wins. A plain key in `authorized_keys` is accepted
exactly as before, and an ssoossh certificate is accepted through the CA.
People can move over one at a time.

Installing the client package does not change this either. The `.deb` and
`.rpm` ship `/etc/ssh/sshd_config.d/50-ssoossh.conf` with every line commented
out, so `sshd` behaves the same until you uncomment them.

### Does it work alongside `AuthorizedKeysCommand`?

Yes, for the same reason. `sshd` runs `AuthorizedKeysCommand` for any key the
files and the CA did not accept, so a command that fetches keys from LDAP, a
secrets store or a cloud metadata service keeps answering. ssoossh does not
touch that directive.

The command ssoossh does ship, `ssoossh host principals`, is for a different
directive, `AuthorizedPrincipalsCommand`. It only decides which certificate
principals may log in as which account, and it is optional.

### We already use `AuthorizedPrincipalsFile` or `AuthorizedPrincipalsCommand`

They combine. `sshd` reads `AuthorizedPrincipalsFile` first and runs
`AuthorizedPrincipalsCommand` only when the file found no match, and a match
in either is enough. A host that already maps principals with a file can add
`ssoossh host principals` as the command.

`sshd` takes only one `AuthorizedPrincipalsCommand`. If yours already runs a
command for something else, have that command also print the output of
`ssoossh host principals %u`, or put the ssoossh mappings in the principals
file instead.

One default changes when you set either directive for the first time: a
certificate is no longer accepted just because it names the login account;
only the listed principals are. `ssoossh host principals` always prints the
account's own name as well, so certificates that name the account directly
keep working. See [Trusting the CA in sshd](/ssoossh/hosts/sshd-trust/).

### We already trust another user CA

`TrustedUserCAKeys` takes one key per line, so the ssoossh CA becomes another
line. `sshd` trusts every listed CA equally, and the principals rules above
apply to all of them. A principal therefore means the same thing whichever CA
signed it. If two CAs could issue the same principal name to different people,
trust one of them per account instead, with a `cert-authority` line and its
own `principals=` list in that account's `authorized_keys`.

### Can I turn it on for only some hosts, users or networks?

Yes. A host without a `TrustedUserCAKeys` line naming the ssoossh CA ignores
ssoossh certificates entirely, so you roll out one host at a time. On a host,
`TrustedUserCAKeys` and both principals directives are allowed inside `Match`
blocks, so you can accept certificates for a pilot group only:

```ini
# /etc/ssh/sshd_config
Match Group ssoossh-pilot
    TrustedUserCAKeys /etc/ssh/ca.pub
```

### How do I make certificates the only way in?

Retire the other ways explicitly, when you are ready:

```ini
# /etc/ssh/sshd_config
AuthorizedKeysFile none
PasswordAuthentication no
KbdInteractiveAuthentication no   # keep yes if pam_ssoossh is in /etc/pam.d/sshd
# and remove any AuthorizedKeysCommand line
```

Before you do, test a break-glass path, such as a `Match` block for an
emergency account or console access. After this change, an ssoossh outage
means no new SSH logins, although certificates already issued keep working
until they expire. Scripts and jobs that log in with a key need a
[service certificate](/ssoossh/concepts/service-certificates/) first.

## sudo, su and console login

### Does installing pam_ssoossh change sudo or login?

No. The package installs the module and writes nothing into `/etc/pam.d`. A
service uses the module only after you add a line to that service's own file.
See [Installing pam_ssoossh](/ssoossh/hosts/pam/install/).

### Do passwords, tokens and the existing PAM stack keep working?

Yes, with the documented setup. That is one `auth sufficient` line placed
above the service's existing authentication, with the rest of the file left as
your distribution shipped it:

```ini
# /etc/pam.d/sudo
auth    sufficient  pam_ssoossh.so server=https://ssoossh.example.com \
                    trusted-ca-file=/etc/ssoossh/ca.pub \
                    principals-map=/etc/ssoossh/principals.yaml
@include common-auth
```

`sufficient` means an approval ends the stack, and anything else moves on to
the next line, normally the password prompt. That includes a denial, a
timeout, Ctrl-C and an unreachable server.

Hosts where people at the keyboard already use Touch ID or a smartcard keep
it: add [`ssh-only`](/ssoossh/hosts/pam/reference/#ssh-only) and only sessions
that arrived over SSH get the browser approval. Everyone else goes straight to
the next module.

### Can sudo and console login both use ssoossh, next to the existing configuration?

Yes. Each PAM service has its own file -- `/etc/pam.d/sudo`, `su`, `login`,
`sshd` -- and you add the line to each one separately. The services do not
affect each other, and a service you leave alone is unchanged. One host can
run browser approval for `sudo` and the console code for `login` at the same
time, each with its password prompt still underneath.

Two rules keep this true:

- **Never put the line in a shared include** (`common-auth`, `system-auth`,
  `base-auth`). Screen lockers, display managers and `polkit` read those too,
  and an approval prompt nobody can see is a lockout. One line per service is
  how ssoossh stays out of everything else.
- **Leave your existing gates above it.** `pam_securetty`, `pam_wheel`,
  `pam_nologin` and `pam_succeed_if` group tests still apply, and an approval
  cannot bypass a gate that runs before it.

See [sudo and su](/ssoossh/hosts/pam/sudo/) and
[console login](/ssoossh/hosts/pam/console/) for each service's stanza.

### What happens when the ssoossh server is down?

Only ssoossh stops working. A `sufficient` PAM line returns
`PAM_AUTHINFO_UNAVAIL` and the stack goes on to the local credential, so an
outage means no browser approvals, not no `sudo`. For SSH, keys and passwords
are unaffected, and certificates people already hold keep working until they
expire. Nobody can get a new certificate until the server is back.

### How do I make the approval mandatory?

Change `sufficient` to `required`, or remove the fallback below it. That makes
the ssoossh server a hard dependency of `sudo` on every host where you do it,
so read [Why `sufficient`](/ssoossh/hosts/pam/sudo/#why-sufficient) first. Do
not do it for console login: the console is the break-glass path for when the
network or ssoossh is down.

## Attribution

### Does ssoossh record who did what on a shared account?

It records who **authenticated**, not what they did afterwards. That is less
than full attribution, and on shared accounts the difference matters.

What gets recorded:

- **An SSH login with a user certificate.** `sshd` logs the certificate's key
  ID, which names the person. A login to a shared `deploy` account therefore
  still names who logged in. The server's approval record keeps who approved,
  from where, when and what was granted.
- **`sudo`, `su` or a console login through `pam_ssoossh`.** The module logs
  each successful authentication to the host's syslog with the local account
  and the certificate's key ID, so a `sudo` to `root` from a shared account
  names who approved it in the host's own log, with no lookup on the server:

  ```text
  pam_ssoossh: successful authentication: root, certificate ID "pam:alice" (serial 42) CA ecdsa-sha2-nistp384 SHA256:E6/Otz...
  ```

  The server's approval record keeps the approver too, beside what the host
  reported: hostname, PAM service, tty, the account that ran `sudo`, and the
  command line (`sudo systemctl restart nginx`) on Linux and FreeBSD. See the
  [module's logging](/ssoossh/hosts/pam/reference/#the-success-line) and the
  [audit log](/ssoossh/operations/audit-log/).

Your existing logging is unchanged: `sudo` still logs the account that ran it
and the command, as it always has.

:::caution[The key ID template decides whether the host log names anyone]
Both host log lines name a person only because the key ID does, and the key
ID is whatever [`key_id_template`](/ssoossh/operations/key-id-templates/)
renders. The built-in defaults start from the approver's username
(`{{.Username}}`, `pam:{{.Username}}`, `console:{{.Username}}`). A custom
template without an identifying field is accepted at startup, and then every
host log line names nobody and the server's record is the only link back to a
person. Add `{{.UniqueID}}` too, to join a host log line to the exact server
record.
:::

What does not get recorded:

- **Anything inside a shell.** `sudo -i`, `sudo -s` or `su -` is approved once,
  and the record shows that command line. Nothing typed in the shell it opens
  reaches ssoossh. The same goes for everything done in an SSH session after
  the login.
- **Cached `sudo`.** `sudo` does not ask for authentication again while its
  credential cache is fresh (`timestamp_timeout`, 15 minutes by default), so
  the next `sudo` commands in that window never reach PAM and leave no
  ssoossh record.
- **`NOPASSWD` rules.** `sudo` skips authentication for those commands
  entirely.
- **Who was at the keyboard.** The record names who approved in the browser.
  ssoossh cannot see the terminal; it knows the approver, who is normally the
  same person.

If you need per-command attribution on shared accounts:

- Set `Defaults timestamp_timeout=0` in sudoers so every `sudo` asks. Each
  command then needs its own approval.
- Avoid `NOPASSWD` and shell-opening rules for those accounts.
- Record the sessions with a tool built for it: `sudo`'s I/O logging
  (`log_output`), `auditd`, or a session recorder.

With all three in place, ssoossh says who authorized each escalation, and the
recording says what happened inside it.
