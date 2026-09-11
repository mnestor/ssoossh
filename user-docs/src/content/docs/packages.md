---
title: Installing from the package repository
description: Signed apt and yum repositories for the client, the server and the PAM module, and how to verify them.
---

Every Linux package ssoossh ships is served from a signed repository at
`packages.mikenestor.org`. Adding it once gets you the client, the server,
the PAM module and its SELinux policy, and gets upgrades through the package
manager you already run.

This is the recommended way to install on Linux. Direct downloads from the
[releases page](https://github.com/mnestor/ssoossh/releases) still work and
are the only option for Alpine, Windows and macOS, but see
[What verification actually happens](#what-verification-actually-happens)
before choosing them on Debian or Ubuntu.

## What is in it

| Package | What it is |
| --- | --- |
| `ssoossh-client` | the `ssoossh` command users run |
| `ssoosshd` | the server |
| `ssoosshd-pkcs11` | the server built to load a PKCS#11 module |
| `pam-ssoossh` | the PAM module, for `sudo`, console login and `sshd` (EL 8, 9 and 10, Debian, Ubuntu) |
| `pam-ssoossh-selinux` | SELinux policy for the PAM module (EL 8 and 9) |
| `ssoossh-release` | the repository definition and signing key |

`ssoosshd-pkcs11` conflicts with `ssoosshd` on purpose -- both install the
same binary path -- so the solver will not let you install both. Most HSM
deployments want plain `ssoosshd`; see
[HSM and PKCS#11](/ssoossh/operations/hsm/).

`pam-ssoossh` is built against OpenSSL 1.1 for EL 8 and OpenSSL 3 for EL 9
and 10, and carries an `.el8`, `.el9` or `.el10` release tag accordingly. You
do not choose: each EL major has its own metadata, and the one your host reads
offers only the build for it.

`pam-ssoossh-selinux` is EL 8 and 9 only. On an enforcing EL host the module
needs SELinux policy to reach `ssoosshd` for `sshd` and console login --
`sudo` is unaffected -- and on EL 10, where no policy package is published,
`setsebool -P authlogin_yubikey on` grants the same access from policy the
distribution already ships. See
[SELinux on EL hosts](/ssoossh/hosts/pam/install/#selinux-on-el-hosts).

## RHEL, Alma, Rocky, Oracle

The repository serves EL 8, 9 and 10. Install the release package, which
writes the `.repo` file and installs the signing key:

```bash
sudo rpm -i https://packages.mikenestor.org/ssoossh/yum/pool/ssoossh-release_1.4.0_noarch.rpm
sudo dnf install ssoossh-client
```

Or write `/etc/yum.repos.d/ssoossh.repo` yourself:

```ini
[ssoossh]
name=ssoossh
baseurl=https://packages.mikenestor.org/ssoossh/yum/el/$releasever/
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://packages.mikenestor.org/ssoossh/gpg-public.asc
```

Leave `$releasever` literal -- dnf expands it, and that is what points your
host at the tree built for its release.

Set both `gpgcheck` and `repo_gpgcheck`. The first verifies each package's
signature, the second verifies the repository metadata's own signature.
Without the second, a correctly signed package can still be advertised to you
by metadata nobody signed.

## Debian and Ubuntu

```bash
curl -fsSLO https://packages.mikenestor.org/ssoossh/apt/pool/main/all/ssoossh-release_1.4.0_all.deb
sudo dpkg -i ssoossh-release_1.4.0_all.deb
sudo apt update && sudo apt install ssoossh-client
```

The release package installs `/usr/share/keyrings/ssoossh-archive-keyring.gpg`
and `/etc/apt/sources.list.d/ssoossh.sources`:

```
Types: deb
URIs: https://packages.mikenestor.org/ssoossh/apt
Suites: stable
Components: main
Architectures: amd64 arm64
Signed-By: /usr/share/keyrings/ssoossh-archive-keyring.gpg
```

`Signed-By` is the part that matters, and it is why this is a deb822
`.sources` file rather than a one-line entry. It binds this repository to
this one key. Without it, apt accepts anything signed by any key in its
trusted set, so a key you added for an unrelated third-party repository could
sign for this one.

## What verification actually happens

It is not the same on both sides, and the difference decides whether the
repository is a convenience or the only thing protecting you.

On **rpm hosts**, packages are signed and `rpm` checks that signature at
install time once the key is imported. A package downloaded straight from the
release page is verifiable on its own.

On **deb hosts**, it is not. Debian ships `no-debsig` uncommented in
`/etc/dpkg/dpkg.cfg` and does not install `debsig-verify`, so the signature
inside a `.deb` is ignored by policy rather than by oversight. Nothing checks
it. What protects an apt install is the signature over the repository
metadata, which lists each package's SHA-256 -- and that exists only when you
install through the repository.

:::caution[On Debian or Ubuntu, prefer the repository]
A `.deb` downloaded from a release page and installed with `dpkg -i` is
verified by nothing at all. If you must install that way, check it yourself
against the release's `SHA256SUMS`, whose detached signature is made with the
same key as the repository.
:::

## The signing key

One key signs the repository metadata, every `.deb` and `.rpm`, and the
`SHA256SUMS` manifests on both projects' release pages:

```
F70D 6841 9D01 9880 1047  8849 026E 0E37 977F 58E1
github.com/mnestor (Signing key for packages) <me@mikenestor.org>
```

It is published at
[`packages.mikenestor.org/ssoossh/gpg-public.asc`](https://packages.mikenestor.org/ssoossh/gpg-public.asc).
Check the fingerprint against the one above before trusting a copy you
fetched over the network:

```bash
curl -fsSL https://packages.mikenestor.org/ssoossh/gpg-public.asc |
  gpg --show-keys --fingerprint
```

## Alpine

Alpine is not served from the repository. Indexing an apk tree needs Alpine's
own tooling, which the release builder does not carry, so `.apk` packages stay
direct downloads. Only the server ships this way; there is no client `.apk`.

They are signed, with a bare RSA key rather than an OpenPGP one, and apk
verifies them against a public half you install first:

```bash
sudo cp ssoossh.rsa.pub /etc/apk/keys/
sudo apk add ./ssoossh-server_1.4.0_linux_amd64.apk
```

`ssoossh.rsa.pub` is published with each release. The file name matters: apk
looks for `/etc/apk/keys/<name>.rsa.pub` matching the name the package was
signed under.

## Upgrades and pinning

Packages are immutable at a given version and cached for a year; repository
metadata is never cached. An upgrade is whatever your package manager does
normally.

Nothing is ever removed from the repository, so an older version stays
installable:

```bash
sudo dnf install ssoossh-client-1.4.0
sudo apt install ssoossh-client=1.4.0
```
