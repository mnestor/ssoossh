---
title: Installing pam_ssoossh
description: Packages, platforms, CA key types, install paths, and verifying a download.
eyebrow: Host administration
sidebar:
  order: 2
---

`pam_ssoossh` is a PAM module that authenticates a local account by having a
human approve a certificate request. This page covers getting it onto a host.
Wiring it into a service is a separate decision, described on
[sudo and su](/ssoossh/hosts/pam/sudo/),
[console login](/ssoossh/hosts/pam/console/) and
[sshd keyboard-interactive](/ssoossh/hosts/pam/sshd/).

## Which module

Two implementations exist, and they are not interchangeable in every detail.

**The C module**, from
[github.com/mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam), is
what every host page on this site documents. It is around 82 KB stripped and
links only libraries the operating system already ships: `libpam`,
`libcrypto`, `libcurl`. It has both flows -- the browser flow and the console
code-and-QR flow -- and the `mode=` argument that picks between them.

**The Go module** in the ssoossh monorepo (`pam_ssoossh/`, built with
`-buildmode=c-shared`) is the earlier implementation. It carries the Go
runtime into `sudo` and `sshd` at around 12.9 MB, does the browser flow only,
has no console mode, and is being retired. It reads the same `pam.d` lines, so
a stanza written for it keeps working, with these deliberate divergences:

| Behaviour | C module | Go module |
| --- | --- | --- |
| Ctrl-C at the approval prompt | `PAM_IGNORE` | `PAM_AUTH_ERR` |
| `ssh-rsa` (SHA-1) CA in the trusted CA file | Refused by policy | Accepted |
| Console flow and `mode=` | Present | Absent |
| Syslog identity | Every line prefixed `pam_ssoossh:` | Syslog tag `ssoossh` |
| Text sent to the terminal | Filtered to a safe character set | Not filtered |
| Host context sent with the request | Every field, on both paths | None |

:::note
The monorepo's own documents -- `docs/pam.d-sudo.example` and deployment.md
§8 -- describe the Go module. Where they and the C module's manual page
disagree, the manual page is what the module on your host does. The
[reference page](/ssoossh/hosts/pam/reference/) restates it in full.
:::

:::caution[Status]
The C module authenticates end to end through a real PAM stack against a stub
`ssoosshd`, under ASan and UBSan, with fuzzing over every parser that reads
network bytes. What has not happened yet is a run against a production
`ssoosshd`, a build on FreeBSD or macOS, or console mode against the real
server endpoints rather than a stub written against them.
:::

## Supported platforms

| Platform | Crypto | Console mode | Ships an artifact |
| --- | --- | --- | --- |
| Linux glibc (x86-64, arm64) | OpenSSL 1.1.1 or newer | yes | yes |
| Linux musl / Alpine | OpenSSL 1.1.1 or newer | yes | yes |
| FreeBSD | OpenSSL in base | yes | yes |
| macOS 15 Sequoia (arm64) | Security.framework | no | no, developer and CI only |

OpenSSL 1.1.1 is a hard floor, set by RHEL 8: a build against anything older
fails at compile time. OpenSSL 1.0.2 and the releases carrying it -- RHEL 7,
CentOS 7 -- are out of scope permanently.

### CA key types

The crypto backend decides what can be verified, so this differs by platform.
Anything unsupported fails with an error naming the algorithm, never with a
vague signature failure.

| CA key type in `trusted-ca-file` | Linux, FreeBSD | macOS |
| --- | --- | --- |
| `ecdsa-sha2-nistp256` / `384` / `521` | supported | supported |
| `rsa-sha2-256`, `rsa-sha2-512` | supported | supported |
| `ssh-ed25519` | supported | supported (macOS 14 and later) |
| `ssh-rsa` (SHA-1) | refused by policy | refused by policy |

`ssh-rsa` names RSA with SHA-1, which OpenSSH has disabled by default since
8.8. It is refused on every platform. Check what your CA key is before you
deploy: `ssh-keygen -l -f /etc/ssoossh/ca.pub`.

## Which package

Releases are built per platform and wrapped as distribution packages by
[nfpm](https://nfpm.goreleaser.com).

| Artifact | For | Package formats |
| --- | --- | --- |
| `linux-{x86_64,aarch64}-glibc-openssl3` | RHEL 9 and rebuilds, Debian 12+, Ubuntu 22.04+, anything with `libcrypto.so.3` | `.deb`, `.rpm` |
| `linux-{x86_64,aarch64}-glibc-openssl1.1` | RHEL 8 and rebuilds, anything with `libcrypto.so.1.1` | `.rpm` |
| `linux-{x86_64,aarch64}-musl` | Alpine | `.apk` |
| `freebsd14-x86_64` | FreeBSD 14 | tarball only |

There is no `.deb` for the OpenSSL 1.1 build: the Debian and Ubuntu releases
that shipped `libssl1.1` are past their support dates.

The module links the host's libraries rather than shipping them, so the
artifact name says where it loads. Package dependencies are sonames wherever
the format allows -- `libcrypto.so.3()(64bit)` for rpm, `so:libcrypto.so.3`
for apk -- which is what `rpmbuild` and `abuild` derive on their own. Debian
has no such provides, so the `.deb` names packages instead, carrying both
spellings of the ones the 64-bit `time_t` transition renamed
(`libssl3t64 | libssl3`).

## Where the package puts things

| Package | Module path |
| --- | --- |
| `.deb` (Debian, Ubuntu) | `/usr/lib/<triplet>/security/pam_ssoossh.so`, for example `/usr/lib/x86_64-linux-gnu/security/pam_ssoossh.so` |
| `.rpm` (RHEL and rebuilds) | `/usr/lib64/security/pam_ssoossh.so` |
| `.apk` (Alpine) | `/lib/security/pam_ssoossh.so` |

Everything else is documentation:

| Path | Contents |
| --- | --- |
| `/usr/share/man/man8/pam_ssoossh.8.gz` | The module manual page |
| `/usr/share/man/man5/pam_ssoossh-ca.5.gz` | The trusted CA file format |
| `/usr/share/man/man5/pam_ssoossh-principals.5.gz` | The principals map format |
| `/usr/share/doc/pam-ssoossh/examples/pam.d/` | A commented fragment for `sudo`, `su`, `sshd` and `login` |
| `/usr/share/doc/pam-ssoossh/examples/principals.yaml` | A principals map to start from |
| `/usr/share/doc/pam-ssoossh/BUILDINFO` | The compiler, the library versions built against, and the sonames needed |

:::note[Nothing is written to /etc/pam.d]
Installing the package does not edit any PAM stack. Wiring the module into a
service is an operator's decision, and no upgrade will make it for you or
undo it.
:::

The paths the examples use for configuration -- `/etc/ssoossh/ca.pub` and
`/etc/ssoossh/principals.yaml` -- are conventions. Nothing in the module fixes
them; both are named on the `pam.d` line.

## Verifying a download

Each release carries a `SHA256SUMS`, build provenance, and, when the
repository holds the signing keys, package signatures. Both public halves are
attached to the release as `pam-ssoossh-release-key.asc` (OpenPGP) and
`pam-ssoossh.rsa.pub` (the RSA key Alpine expects).

```console
$ gpg --import pam-ssoossh-release-key.asc
$ gpg --verify SHA256SUMS.asc SHA256SUMS
$ sha256sum -c SHA256SUMS --ignore-missing
```

Per format:

```console
$ rpm --import pam-ssoossh-release-key.asc && rpm -K pam-ssoossh-*.rpm
$ sudo cp pam-ssoossh.rsa.pub /etc/apk/keys/ && sudo apk add ./pam-ssoossh-*.apk
```

The `.apk` public key filename is what `apk` matches the signature against,
and it must land as exactly `/etc/apk/keys/pam-ssoossh.rsa.pub`.

For the `.deb`, `apt` verifies repositories rather than files, so the
`SHA256SUMS` signature is the check that matters. The embedded `_gpgorigin`
signature can be checked with `dpkg-sig --verify` where that tool exists.

GitHub's own build provenance:

```console
$ gh attestation verify pam-ssoossh_1.2.0_amd64.deb -R <owner>/<repo>
```

A release built without the signing secrets is unsigned, and the release job
log says so. `SHA256SUMS` is still published either way.

A tag with a hyphen (`v1.2.0-rc1`) is a pre-release. Package versions come
from `git describe`, and a pre-release sorts before the release it precedes in
every format, so an untagged or release-candidate build can never outrank a
real one on a host.

## Building from source

Build dependencies are `libpam`, `libcrypto` and `libcurl`:

| Platform | Packages |
| --- | --- |
| Debian, Ubuntu | `build-essential pkg-config libssl-dev libcurl4-openssl-dev libpam0g-dev` |
| RHEL 8+, AlmaLinux, Rocky | `gcc make pkgconf-pkg-config openssl-devel libcurl-devel pam-devel` |
| Alpine | `build-base pkgconf openssl-dev curl-dev linux-pam-dev` |
| FreeBSD | `gmake pkgconf curl` -- libpam and OpenSSL are in base, libcurl is not |
| macOS 15+ | Xcode command line tools only. No OpenSSL, no Homebrew |

```console
$ make            # pam_ssoossh.so, or pam_ssoossh.bundle on macOS
$ make test       # symbol gate, load gate, and the unit suite
$ make dist       # the release tarball
$ make packages   # deb, rpm and apk from that tarball, needs nfpm on PATH
```

Two vendored libraries are compiled in rather than linked -- a JSON tokenizer
and the QR encoder console mode draws with -- so neither appears in `ldd`
output or in the exported symbol table. Every link-time dependency is a
library the operating system already ships.

For a host whose distribution has stopped issuing OpenSSL updates, build
against a self-maintained one without changing any source:

```console
$ make OPENSSL_PREFIX=/opt/openssl-3.5
```

## After installing

1. Put the CA public key where the module will read it --
   [the trusted CA file](/ssoossh/hosts/pam/trusted-ca/).
2. Write a principals map, unless every identity provider username is spelled
   exactly like the local account it logs in as --
   [the principals map](/ssoossh/hosts/pam/principals-map/).
3. Add the module to one service, from a second root session --
   [sudo and su](/ssoossh/hosts/pam/sudo/).
