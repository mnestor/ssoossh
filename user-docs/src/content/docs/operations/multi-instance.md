---
title: Multi-instance and NATS
description: Running several ssoosshd processes behind a load balancer, with NATS carrying the certificate pipeline between them.
eyebrow: Server operations
sidebar:
  order: 5
---

Multi-instance means several `ssoosshd serve api` processes behind a load
balancer, one or more `ssoosshd sign` processes, all connected to NATS and
sharing one PostgreSQL database. If you run a single process, skip this page
and stay on `ssoosshd serve`.

## What it requires

- A shared PostgreSQL database. SQLite is single-connection and will not do.
- NATS as the message broker, with mutual TLS.
- An explicit session cookie key, the same on every instance.
- [`multi_instance: true`](/ssoossh/reference/config/top-level/#multi_instance).

```yaml
multi_instance: true

http:
  public_url: "https://ssh.example.com"
  # The same value on every instance.
  cookie_key: "your-secret-key-here-32-bytes-minimum"
  trusted_proxies: ["10.0.0.0/24"]   # the load balancer

db:
  provider: postgres
  connection_string: "postgres://user:pass@db.example.com/ssoossh"

pubsub:
  backend: nats
  nats:
    url: "nats://nats.example.com:4222"
    cert_file: "/etc/ssoossh/nats/client-cert.pem"
    key_file: "/etc/ssoossh/nats/client-key.pem"
    ca_file: "/etc/ssoossh/nats/ca-cert.pem"
```

[`multi_instance`](/ssoossh/reference/config/top-level/#multi_instance)
declares the intent. It turns on the checks that only matter with more than
one process -- notably that
[`http.cookie_key`](/ssoossh/reference/config/http/#cookie_key) is set
explicitly -- and adapts behaviour for cross-instance delivery, where a client
waiting on one instance is woken by another.

### cookie_key

Left empty, a key is generated once and persisted in the database's
`server_secrets` table, so sessions survive a restart and instances sharing a
database share the key. Setting it explicitly keys sessions from outside the
database, which is what `multi_instance: true` insists on: with it unset under
`multi_instance`, `ssoosshd` fails at startup. If the value differs between
instances, people get logged out at random as requests land on instances with
different keys.

## Setting up NATS

NATS carries signing jobs, signed certificates, per-request wake-ups, CA
public-key announcements, and notification events across instances. Every
instance must reach the NATS server over the network.

**For development:** `docker run -p 4222:4222 nats:latest`. No
authentication, and not suitable for anything else.

**For production:** mutual TLS. You need a CA certificate, a server
certificate and key for NATS, and a client certificate and key for each
`ssoosshd` process. With a local CA:

```bash
# CA
openssl genrsa -out ca-key.pem 2048
openssl req -new -x509 -days 365 -key ca-key.pem -out ca-cert.pem

# Server (signed by your CA)
openssl genrsa -out server-key.pem 2048
openssl req -new -key server-key.pem -out server.csr
openssl x509 -req -in server.csr -CA ca-cert.pem -CAkey ca-key.pem \
  -CAcreateserial -out server-cert.pem -days 365

# Client (one per ssoosshd instance)
openssl genrsa -out client-key.pem 2048
openssl req -new -key client-key.pem -out client.csr
openssl x509 -req -in client.csr -CA ca-cert.pem -CAkey ca-key.pem \
  -CAcreateserial -out client-cert.pem -days 365
```

Configure `nats-server` to require client certificates (its `tls` and
`authorization` blocks; see the NATS documentation). On the `ssoosshd` side
the three paths are
[`pubsub.nats.cert_file`](/ssoossh/reference/config/pubsub/#natscert_file),
[`pubsub.nats.key_file`](/ssoossh/reference/config/pubsub/#natskey_file), and
[`pubsub.nats.ca_file`](/ssoossh/reference/config/pubsub/#natsca_file). All
three are required once
[`pubsub.nats.url`](/ssoossh/reference/config/pubsub/#natsurl) is set, and an
unset or unreadable one fails the process at startup.

## The subject layout

| Subject | Queue group | Published by | Subscribed by |
| --- | --- | --- | --- |
| `certrequest.sign` | `signer` | API instances | signers |
| `certrequest.signed` | `signed-listeners` | signers | API instances |
| `certrequest.wait.<request-id>` | none (fan-out) | API instances | API instances |
| `ca.key.request` | none | API instances | signers |
| `ca.key.announce` | none | signers | API instances |
| `notification.send` | `notifiers` | API instances | API instances |

The queue groups are what make the pipeline safe with several consumers.
`certrequest.sign` and `certrequest.signed` are competing-consumer topics, so
exactly one process handles each job and each reply. `certrequest.wait.*` has
no queue group, because the message has to reach the one instance holding that
request's event stream. `notification.send` has a queue group for a plainer
reason: without it every instance would deliver the same notification and the
recipient would get one copy per running server.

`ca.key.request` and `ca.key.announce` carry the CA public-key registry. An
API instance asks for an announcement at startup to seed the registry; signers
announce on demand and on reconnection. Omit these two and `GET /api/ca`
answers with nothing to serve, so `ssoossh ca` and the client's CA fetch stop
working even though signing itself still succeeds.

JetStream is not used. The transport is NATS core, at-most-once: a dropped job
costs the waiting client its full `client_timeout` before it retries, which is
acceptable for a flow a human is standing in front of.

## NATS authorization

NATS maps certificate identity to subject permissions, which is what stops an
API instance from acting as a signer:

```text
authorization: {
  users: [
    # API instances: request signatures, deliver results, seed the CA registry
    {
      user: "api-instance-1"
      permissions: {
        publish: ["certrequest.sign", "certrequest.wait.>",
                  "ca.key.request", "notification.send"]
        subscribe: ["certrequest.signed", "certrequest.wait.>",
                    "ca.key.announce", "notification.send"]
      }
    },
    # Signers: consume signing jobs, publish results and the CA public key
    {
      user: "signer-instance"
      permissions: {
        publish: ["certrequest.signed", "ca.key.announce"]
        subscribe: ["certrequest.sign", "ca.key.request"]
      }
    },
  ]
}
```

`notification.send` only matters when
[`mail.enabled`](/ssoossh/reference/config/mail/#enabled) is true; drop it from
the lists otherwise. Signers never touch it -- they have no database and no
notion of a recipient.

## Failover and load balancing

Put the API instances behind haproxy, nginx, or a cloud load balancer, which
may route any request to any instance. No sticky sessions are needed:

- an approval on instance A writes to a database instance B reads;
- a certificate signed for a request created on A reaches a client waiting on
  B, because the wake-up crosses NATS;
- web sessions persist across instances, from the shared database and the
  shared `cookie_key`.

Clients never notice which instance they reached. What the load balancer does
have to do is name itself in
[`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies) and
not buffer the certificate event stream --
[TLS and reverse proxies](/ssoossh/operations/tls-and-proxy/).

## What happens when something breaks

Nothing is lost that matters, because the flow is short and interactive: the
human is the retry mechanism.

**An instance crashes.** Pending approvals are unaffected -- clients keep
waiting on the database status. While a request is still `pending` or
`signing`, the wait loop continues. If it moved to `approved` and the wake
message was lost, the client is answered `410 Gone`, because the certificate
is never persisted, and it re-requests.

**NATS goes down.** Instances still serve session data and historical
certificate records, but no new approval can be delivered to a waiting client.
Both the signer and the listeners have to reach NATS for the certificate
pipeline to work at all.

**A signing job is lost** -- a signer crashes mid-processing, or the transport
drops a message. The client sees the status stay `signing` and keeps waiting.
A stranded-request sweep then fails the request so the client stops waiting
and re-requests; it deliberately errs long, so a request still legitimately in
flight is never cancelled.

**One instance loses the database.** The others are unaffected, but that
instance is out of service. The database itself has to be highly available --
replication and failover are your problem, not `ssoosshd`'s.

**Session cookies** survive an instance restart as long as the shared database
is available and `cookie_key` is consistent.

## What can go wrong in configuration

| Symptom | Cause |
| --- | --- |
| startup fails naming `gochannel` | a split mode with [`pubsub.backend`](/ssoossh/reference/config/pubsub/#backend) left at the in-process default |
| startup fails on mTLS credentials | `pubsub.backend: nats` with `cert_file`, `key_file`, or `ca_file` unset or unreadable |
| startup fails on the cookie key | `multi_instance: true` with no explicit `http.cookie_key` |
| users are logged out at random | `cookie_key` differs between instances |
| `ssoossh ca` returns nothing | no signer has announced, or the `ca.key.*` subjects are not permitted |
| every recipient gets one mail per instance | the `notification.send` queue group is not in effect |
| everyone lands in the most generous lifetime tier | `trusted_proxies` does not name the load balancer, so every request carries its address |

A complete multi-instance `ssoosshd.yaml` is on
[Server configuration examples](/ssoossh/examples/server-configs/).
