---
title: sudo with PAM
description: Authenticate sudo and su against ssoossh using pam_ssoossh.
eyebrow: Guides
sidebar:
  order: 3
---

`pam_ssoossh` authenticates a local PAM operation -- `sudo`, `su` -- by issuing
a short-lived PAM certificate through the same browser approval the SSH flow
uses. The certificate is validated once and discarded.

:::danger[Read this before editing `/etc/pam.d/sudo`]
Getting this file wrong costs you `sudo` on that machine. Not just PAM login --
`sudo` specifically, which is also how you would normally fix a PAM mistake.

- Keep a root shell open in a second terminal (or an active `sudo` session)
  before you touch the file. Do not close it until you have confirmed `sudo`
  still works from a fresh terminal.
- Test in that second terminal, not the one you are editing from.
- Keep a copy of the working file first: `cp /etc/pam.d/sudo /etc/pam.d/sudo.bak`
:::

## The stack entry

One line in the `auth` group of `/etc/pam.d/sudo`, above the existing
`pam_unix.so` line:

```
auth  sufficient  pam_ssoossh.so  server=https://ssoosshd.example.com  trusted-ca-file=/etc/ssoossh/ca.pub
```

`sufficient` means a successful browser approval satisfies the auth stack on its
own. `pam_unix.so` still runs and can satisfy it independently -- a local
emergency password, say -- unless you remove it.

With the optional settings also set:

```
auth  sufficient  pam_ssoossh.so  server=https://ssoosshd.example.com  trusted-ca-file=/etc/ssoossh/ca.pub  principals-map=/etc/ssoossh/principals.yaml  skew-tolerance=5s  timeout=90s
```

:::caution
Only the `auth` management group is implemented. Do not add `pam_ssoossh.so` to
`account`, `password`, or `session` lines.
:::

## Where the module is installed

The package places it at:

| Package | Path |
| --- | --- |
| deb (x86_64) | `/usr/lib/x86_64-linux-gnu/security/pam_ssoossh.so` |
| deb (aarch64) | `/usr/lib/aarch64-linux-gnu/security/pam_ssoossh.so` |
| rpm (any arch) | `/usr/lib64/security/pam_ssoossh.so` |

Installing the package does not edit `/etc/pam.d` -- the stack entry is always
yours to add.

## Module arguments

Arguments are `key=value`, or a bare `key` for a boolean flag. Per `pam.conf(5)`
a value containing spaces must be bracketed (`key=[a value with spaces]`);
libpam strips the brackets before the module sees them.

### `server=URL`

**Required.** Base URL of the ssoosshd server. Missing or empty fails every
login with `PAM_USER_UNKNOWN`.

```
server=https://ssoosshd.example.com
```

### `trusted-ca-file=PATH`

**Required.** An `authorized_keys`-format file listing the CAs trusted to sign
PAM certificates, one per line -- the same key used for `TrustedUserCAKeys`.
Rotate CAs by editing this file; no restart is needed. Missing or empty fails
every login with `PAM_NO_MODULE_DATA`.

```
trusted-ca-file=/etc/ssoossh/ca.pub
```

### `principals-map=PATH`

Optional. A YAML file mapping a local account name to the certificate
principals allowed to assume it:

```yaml
# /etc/ssoossh/principals.yaml
alice:
  - alice
  - admin
```

Omitted -- or a path that fails to load, whether missing or malformed -- means
the certificate must instead carry the exact local account name as one of its
principals.

### `skew-tolerance=DURATION`

Optional, default `2s`. Clock-skew allowance applied symmetrically to both ends
of the certificate's validity window. Go duration syntax; an unparseable value
falls back to the default silently. Choose it together with the server's
`cert_options.pam.valid_duration`.

### `timeout=DURATION`

Optional, default `60s`. How long a login attempt waits for browser approval
before giving up. Same duration syntax and fallback behavior.

### `debug`

Optional, off by default. The bare form, or any value other than `false` or
`stdout`, logs through syslog (facility `authpriv`, tag `ssoossh`).
`debug=stdout` writes debug lines to stdout instead, which is mainly useful with
the `pamtest` harness -- on a real login, stdout belongs to whatever invoked PAM.

Every invocation logs its version at info level through syslog regardless of
this setting.

### `insecure-skip-verify`

Optional, default false. Skips TLS certificate verification when talking to the
server. The bare form or `=true` enables it.

:::danger
Do not use this outside testing.
:::

## Server side

PAM certificates are their own certificate type, configured under
[`cert_options.pam`](/ssoossh/reference/config/cert_options/pam/). Its
`valid_duration` is the one to set alongside the module's `skew-tolerance`.

The key ID template for PAM deliberately never falls back to the `user`
template, so a `sudo` and an SSH login by the same person stay distinguishable
in an audit log.
