---
title: Getting started
description: The ssoossh components and how the server is configured.
sidebar:
  order: 0
---

ssoossh issues short-lived SSH certificates backed by your identity provider.
It ships as three components:

- **`ssoosshd`** -- the server. Authenticates users via OIDC, decides
  certificate contents, signs public keys, and serves the web UI for
  approval, confirmation, and per-user certificate history.
- **`ssoossh`** -- the client. Requests certificates from the command line.
- **`pam_ssoossh`** -- a PAM module that issues certificates for local PAM
  authentication.

## Configuring the server

`ssoosshd` is configured by a single YAML file layered over built-in
defaults: every key has a working default, and your file only states what
differs.

The config file is located as follows: if the `--config`/`-c` flag is set,
that file is used. Otherwise the first of these that exists wins:

1. `./ssoosshd.yaml` (current directory)
2. `~/.config/ssoosshd.yaml`
3. `~/.config/ssoossh/ssoosshd.yaml`
4. `/etc/ssoosshd.yaml`
5. `/etc/ssoossh/ssoosshd.yaml`

An annotated copy of the defaults ships to `/etc/ssoossh/ssoosshd.yaml`, so
a fresh install starts from a file that documents itself. The same content
is available as the `ssoosshd.yaml(5)` man page, and as the
[configuration reference](../reference/config/) on this site -- all three
are generated from the same source, the server's config structs.

## Where to go next

- The [configuration reference](../reference/config/) documents every key,
  its type, and its default, one page per section.
