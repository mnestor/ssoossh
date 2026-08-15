# Manual PAM testing

A recipe for exercising `pam_ssoossh.so` against a real PAM stack, outside
the Go test suite. Not automated; `pamtest.c` in this directory is the
harness. See [release-phase3-pam-build-env.md](../../docs/release-phase3-pam-build-env.md)
for the build environment this assumes, and
[release-phase5-pam-client.md](../../docs/release-phase5-pam-client.md) for
where this fits in verification.

## Build environment

An old-glibc container, matching the release build target. `amazonlinux:2`
is what this recipe was last run against:

```console
$ yum install -y sudo gcc glibc-devel make curl pam-devel
```

Go, if not already present in the image:

```console
$ curl -fsSL https://go.dev/dl/go1.26.5.linux-arm64.tar.gz -o /tmp/go.tar.gz
$ rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz
$ export PATH=/usr/local/go/bin:$PATH
```

Swap the archive for the host architecture as needed.

## Build and install the module

From the repository root, with `CGO_ENABLED=1` and the `pam` build tag (see
[release-phase3-pam-build-env.md](../../docs/release-phase3-pam-build-env.md)
for why both are required):

```console
$ CGO_ENABLED=1 go build -tags=pam -buildmode=c-shared -o pam_ssoossh.so ./pam_ssoossh/
$ cp pam_ssoossh.so /lib64/security/
$ chmod 644 /lib64/security/pam_ssoossh.so
```

`/lib64/security/` is the RHEL-family path. Debian and Ubuntu use
`/lib/x86_64-linux-gnu/security/` or the arm64 equivalent.

## Configure a test PAM service

A dedicated service name, so this never touches the real `sudo` or `su`
stack while iterating:

```
# /etc/pam.d/ssoossh-test
auth    requisite   pam_ssoossh.so server=https://ssoossh.example.com trusted-ca-file=/etc/ssoossh/ca.pub insecure-skip-verify=false debug
account required    pam_unix.so
```

`server` and `trusted-ca-file` are both required — Authenticate fails closed
(`PamUserUnknown`/`PamNoModuleData`) before any network call if either is
missing. `trusted-ca-file` is `authorized_keys` format, one CA per line (see
"Check 1" in
[release-phase5-pam-client.md](../../docs/release-phase5-pam-client.md)).
Two more module arguments tune the checks themselves, both accepting
anything `time.ParseDuration` understands: `skew-tolerance` (default `2s`),
the symmetric clock-skew allowance on the certificate's validity window, and
`timeout` (default `60s`), how long to wait for browser approval before
giving up.

### Argument values containing spaces

`module-arguments` in a PAM config line are whitespace-separated (see
`man pam.conf`). To pass a value that itself contains spaces — for example
a `trusted-ca-file` path — bracket it, per pam.conf(5):

```
auth requisite pam_ssoossh.so trusted-ca-file=[/etc/ssoossh/ca keys/prod.pub]
```

libpam strips the brackets and merges the bracketed text into a single
argument before the module ever sees it, so `parseArgs` in
[args.go](../args.go) needs no special handling — the space is already
part of the one argument it receives. A literal `]` inside the value needs
`\]`; see `man pam.conf` for the exact rule. There is no other way to
include spaces in a module argument: PAM does not understand shell-style
quoting (`key="value with spaces"`) at all, so writing it that way just
gets the line split apart on whitespace like any other unbracketed text.

## Run it

```console
$ gcc -o pamtest pamtest.c -lpam -lpam_misc
$ ./pamtest ssoossh-test
```

`misc_conv` prints any `PAM_TEXT_INFO` message to the terminal, which is how
the approval URL surfaces during a manual run. Approve or deny in the
browser and confirm `pamtest` reports the matching result.

Only once this is solid against the test service name should the same
stanza be added to the real `auth` group for `sudo`/`su`, per
[release-phase7-deploy-docs.md](../../docs/release-phase7-deploy-docs.md)'s
lockout warning: keep a second root shell open while editing
`/etc/pam.d/sudo`.
