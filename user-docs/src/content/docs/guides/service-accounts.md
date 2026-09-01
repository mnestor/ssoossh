---
title: Service accounts
description: Enroll a non-interactive account and retrieve its certificate.
eyebrow: Guides
sidebar:
  order: 2
---

A service account is a non-interactive identity -- a CI runner, a backup job --
that needs an SSH certificate without a person at a browser every time. It is
wired up once from an enrollment code instead of an interactive login.

The certificate it receives is a User-type certificate; there is no separate
service certificate type on the wire.

## 1. Enroll

```sh
ssoossh service enroll --key /etc/myservice/id
```

`--key` names the keypair. The client generates one if it is not there, or
enrolls the existing pair at that path (`/etc/myservice/id` and
`/etc/myservice/id.pub`).

The command prints an enrollment code. A human approves the enrollment in the
web UI, and the approver -- not the requesting machine -- chooses which service
account it mints for. The server binds the code to both the public key and the
option set fixed at approval.

Add `--retrieve` to enroll and fetch the first certificate in one step.

## 2. Retrieve

Later, and on a schedule, the service redeems its code:

```sh
ssoossh service retrieve --code <code> --key /etc/myservice/id
```

The certificate is written to `/etc/myservice/id-cert.pub`.

Each call redeems the code once. Later invocations post only the code -- the
public key is never resubmitted, because the server already holds the one the
code is bound to.

| Flag | Default | What it does |
| --- | --- | --- |
| `--code` | | The enrollment code to redeem |
| `--key` | | The keypair path; the certificate lands at `<path>-cert.pub` |
| `--grace` | `1m` | How long a cached certificate is still considered fresh |
| `--force` | off | Bypass the local cache and fetch regardless |

`--grace` is what keeps a job that runs every minute from asking the server for
a new certificate every minute. `--force` is the escape hatch when you need to
know the fetch actually happened.

## What the approver controls

Everything durable about the enrollment is fixed at approval, not requested by
the client:

- the account the code mints certificates for
- the certificate options granted
- how long the enrollment itself lives, via
  [`cert_options.service.enrollment_duration`](/ssoossh/reference/config/cert_options/service/#enrollment_duration)
- how long each issued certificate is valid, via
  [`cert_options.service.valid_duration`](/ssoossh/reference/config/cert_options/service/#valid_duration)

:::note
The web UI never shows an enrollment code after approval, and the server has no
endpoint that returns one. The code exists only in the output of the `enroll`
command that created it -- if it is lost, enroll again.
:::

## Containment

Enrollments are visible and revocable to the admin and SOC roles, which is what
[`admin.require_group` and `admin.soc_group`](/ssoossh/reference/config/admin/)
gate. Expiring an enrollment stops it minting new certificates; certificates it
already issued remain valid until their own `valid_duration` runs out, which is
what keeps that duration short.
