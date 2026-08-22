# Getting started

The shortest path to a working `ssh login`. Everything optional is left
out and linked instead — see [deployment.md](deployment.md) for the
reference material behind each step (CA key generation, `sshd`
configuration, OIDC provider setup, reverse proxy/TLS, and the PAM/`sudo`
path, which this page does not cover).

This assumes an `ssoosshd` server is already running and reachable, with a
CA key configured and an OIDC provider wired up. If it isn't yet, do that
first — [deployment.md](deployment.md) §1–§6.

## 1. Install the client

**Linux** (`.deb`/`.rpm` from the release):

```sh
# Debian/Ubuntu
sudo dpkg -i ssoossh-client_*.deb
# RHEL/Fedora/etc.
sudo rpm -i ssoossh-client_*.rpm
```

**Windows**: download the `.zip` from the release, extract `ssoossh.exe`
somewhere on `PATH`.

**macOS**: download the `.zip` from the release. The binary inside is
signed and notarized (confirmed working end to end via the `v0.1.0`
release), so Gatekeeper does not block it. Extract and place `ssoossh` on
`PATH`.

## 2. Point the client at the server

```sh
ssoossh --server https://ssh.example.com ssh config
```

prints the resolved configuration — confirms the server address is
picked up before you try a real login. To make it permanent, put it in
`ssoossh.yaml` instead of passing `--server` every time (see
`docs/ssoossh.yaml.default` for the search paths).

## 3. Wire up `ssh_config`

```
Match host bastion.example.com exec "ssoossh ssh login"
    User youruser
```

See [deployment.md §4](deployment.md#4-client-ssh_config-recipes) for the
`ProxyCommand` alternative and when it's needed instead.

## 4. Log in

```sh
ssh bastion.example.com
```

The first connection opens a browser (or prints a URL, if none opened) for
OIDC approval. Approve it, and `ssh` proceeds — a certificate is now
loaded into your agent and reused for subsequent connections until it
expires.

If it takes more than this to get a certificate, something's wrong —
either with this page or with the deployment; check
[deployment.md](deployment.md) for the failure that matches (a redirect
URI the identity provider rejects almost always traces back to
`http.public_url`).

## What's next

- `sudo`/`su` through PAM: [deployment.md §7](deployment.md#7-pam-sudo-and-su).
- What's in this release and what isn't: [release-notes.md](release-notes.md).
