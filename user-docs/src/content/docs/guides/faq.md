---
title: User FAQ
description: The questions people connecting with ssoossh ask first.
eyebrow: User guide
sidebar:
  order: 6
---

Questions from the person using ssoossh to connect. Host administrators and
server operators have their own FAQs, at
[Host admin FAQ](/ssoossh/hosts/faq/) and
[Operator FAQ](/ssoossh/operations/faq/). If your question is of the form "why
does it not just...", check [Decisions](/ssoossh/project/decisions/) first --
that page exists for exactly those.

### Do I have to replace ssh?

No. Your existing `ssh` invokes the ssoossh client through a line or two of
`ssh_config`, and from there everything is standard OpenSSH certificate
authentication. `ssoossh ssh config` prints those lines and needs no server, so
it answers before anything is set up. See
[ssh_config integration](/ssoossh/guides/ssh-config/), or the
[illustrated walkthrough](/ssoossh/concepts/walkthrough/) for what a login
looks like.

### Do I get a browser prompt every time I ssh?

No. A valid certificate is reused until it expires, so one browser approval
typically covers a workday. The certificate is checked once at session start;
an established SSH session does not drop when the certificate expires.

`ssoossh ssh login --force` replaces a loaded certificate when you want a new
one anyway.

### Do I need an ssh-agent?

Only for `ProxyCommand` mode. `ssh` reads key files once at startup, so a
certificate refreshed on disk after that goes unseen. With the recommended
`Match exec` mode, key files on disk work fine without an agent -- see
[ssh_config integration](/ssoossh/guides/ssh-config/) and
[`use_agent`](/ssoossh/reference/client-config/#use_agent).

### Can I approve from a different device than the one running ssh?

Yes. The approval URL can be opened in any browser; the client just waits for
the outcome on its own stream. Be aware that the deployment's lifetime policy
may correlate the browser's address with the client's, and weak correlation can
shorten the issued lifetime -- never lengthen it. See
[Options and lifetime resolution](/ssoossh/concepts/options-and-lifetime/).

### Does it work on Windows and macOS?

Yes: macOS, Linux, and Windows, including Pageant and the WSL relay. The macOS
binary is signed and notarized, so Gatekeeper does not block it.
[Getting started](/ssoossh/getting-started/) has the install steps.

On WSL, what decides which build you need is not the operating system but which
ssh-agent your `ssh` reads. With an agent on each side, `ssh` inside a distro
needs the Linux build installed in that distro and `ssh.exe` needs the Windows
build. With one agent bridged across the boundary, a single installation covers
both.

### It is not working. What do I send you?

Re-run with `-v` and attach the stderr. That is the flag to reach for first;
`-vv` adds requests and file operations, `-vvv` adds bodies.

```bash
ssoossh -vv ssh login 2> ssoossh.log
```

If `ssh` is the one invoking the client, its command line is not yours to edit,
so use the environment variable instead:

```bash
SSOOSSH_VERBOSE=2 ssh bastion.example.com 2> ssoossh.log
```

When the problem looks like the wrong configuration rather than the wrong
behavior -- the wrong server, a config file you expected to be picked up and was
not, a key file it cannot find -- add `--debug` (or `SSOOSSH_DEBUG=1`). Both
write to stderr only, so neither disturbs a `ProxyCommand` relay or a
certificate on stdout. Full detail:
[Diagnostics](/ssoossh/guides/diagnostics/).

Read the log before sending it: at `-vvv` it contains request bodies, and it
always names your server, username, and file paths.

### Does the server ever see my private key?

No. The client generates the keypair locally and sends only the public key; the
private key goes nowhere except your local ssh-agent or a local file. This is
one of the project's hard invariants -- see
[Security model](/ssoossh/concepts/security-model/).

### How is the private key protected when it is written to a file?

On macOS and Linux it is written `0600`, readable only by you and root.

Windows has no file mode, so the client sets an access list on the file
instead: the account that generated the key, LocalSystem, and Administrators,
with inherited entries from the containing folder switched off. That is the
same set OpenSSH for Windows requires before it will use a key, and it is the
closest equivalent to `0600`.

One consequence worth knowing for service accounts: the account that runs
`ssoossh service enroll` is the account named on the key. If a service then
runs as a different, non-administrator user, grant that user access to the key
file explicitly, exactly as you would with `chown` on Linux. See
[Service accounts](/ssoossh/guides/service-accounts/).

### Which of my accounts does the certificate name?

The ones you pick on the approval page, from the accounts you hold. For a PAM
or console request the page defaults to the local account being acted as, when
you hold it, because that is what the host matches against. Which of your names
may assume which local account is the host's decision, not the server's -- see
[The principals map](/ssoossh/hosts/pam/principals-map/).

`ssoossh ssh inspect` prints the principals on the certificate you are actually
holding.

### Can I stop the client asking for forwarding I never use?

Yes. The client asks for the full interactive extension set by default, and you
can opt out per extension in config or with a flag:

```bash
ssoossh ssh login --no-x11-forwarding --no-agent-forwarding
```

The equivalent config keys are under
[`certificate_extensions`](/ssoossh/reference/client-config/#certificate_extensions).
The server narrows whatever is requested against its own configuration anyway,
and the approval page shows what survived, so asking for the full set is always
safe.

### How do I get rid of the certificate on this machine?

```bash
ssoossh ssh logout
```

It removes the certificates ssoossh put in your ssh-agent -- those signed by
the configured CA -- and nothing else. Your own keys are left alone. When key
files are used instead of an agent, the files ssoossh wrote are deleted.

### Where does my certificate history live?

In the web UI: the dashboard shows your recent certificates, and **History**
shows all of them, each opening into a detail view. Service certificates link
back to the code they were redeemed from. See
[Approving in the browser](/ssoossh/guides/approving/).

### Why did I get an email about a certificate?

Because you turned that kind on. The three "was this you?" notifications --
user, PAM, and console certificate issued -- are off by default; the four about
service enrollments are on. Choose at `/preferences` in the web UI:
[Notification preferences](/ssoossh/guides/approving/#notification-preferences).

### Can my administrator lock my client settings?

Yes: an `enforce` file on Linux, Group Policy on Windows, or managed
preferences on macOS. `--debug` marks those tiers in the source chain so you
can see which settings you cannot argue with. They are guardrails rather than a
security boundary -- the client runs as you. See
[Client configuration](/ssoossh/guides/client-config/) and
[Client settings enforcement](/ssoossh/hosts/client-enforcement/).
