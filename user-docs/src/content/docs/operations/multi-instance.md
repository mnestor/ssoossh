---
title: Multi-instance and NATS
description: Running several ssoosshd processes behind a load balancer, with NATS carrying the certificate pipeline between them -- PKI, nats-server.conf, authorization, and the order to bring it up in.
eyebrow: Server operations
sidebar:
  order: 5
---

Multi-instance means several `ssoosshd serve api` processes behind a load
balancer, one or more `ssoosshd sign` processes, all connected to NATS and
sharing one PostgreSQL database. If you run a single process, skip this page
and stay on `ssoosshd serve`.

```mermaid
flowchart TB
    C["Browsers, ssoossh clients, pam_ssoossh"] --> LB["Load balancer"]
    LB --> A1["ssoosshd serve api #1"]
    LB --> A2["ssoosshd serve api #2"]
    A1 --> DB[("PostgreSQL")]
    A2 --> DB
    A1 <--> N["NATS (mTLS)"]
    A2 <--> N
    N <--> S1["ssoosshd sign #1"]
    N <--> S2["ssoosshd sign #2"]
    S1 --> K["CA key: ssh_key or PKCS#11"]
    S2 --> K
```

## What it requires

- A shared PostgreSQL database. SQLite is single-connection and will not do.
- NATS as the message broker. `ssoosshd` always speaks TLS to it, and always
  presents a client certificate; there is no unauthenticated mode.
- An explicit session cookie key, the same on every instance.
- [`multi_instance: true`](/ssoossh/reference/config/top-level/#multi_instance).

[`multi_instance`](/ssoossh/reference/config/top-level/#multi_instance)
declares the intent. It turns on the checks that only matter with more than
one process -- notably that
[`http.cookie_key`](/ssoossh/reference/config/http/#cookie_key) is set
explicitly -- and adapts behaviour for cross-instance delivery, where a client
waiting on one instance is woken by another.

The rest of this page is the build order:

1. [PostgreSQL](#1-postgresql), shared by every API instance.
2. [Issue the NATS certificates](#2-issue-the-nats-certificates).
3. [Write `nats-server.conf`](#3-write-nats-serverconf) and start the broker.
4. [Verify NATS before ssoosshd touches it](#4-verify-nats-on-its-own).
5. [Configure the signers](#5-configure-the-signers).
6. [Configure the API instances](#6-configure-the-api-instances).
7. [Start everything in order](#7-start-in-order) and point the load balancer
   at it.

## The subject layout

The NATS authorization you are about to write is derived from this table, so
it comes first.

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
announce at startup, every five minutes, and on demand. Omit these two and
`GET /api/ca` answers with nothing to serve, so `ssoossh ca` and the client's
CA fetch stop working even though signing itself still succeeds.

Nothing uses request/reply, so no client needs `_INBOX.>` permissions.

JetStream is not used. The transport is NATS core, at-most-once: a dropped job
costs the waiting client its full `client_timeout` before it retries, which is
acceptable for a flow a human is standing in front of.

## 1. PostgreSQL

One database, reachable from every API instance. Signers never touch it.
Schema migrations run at startup on every instance and are serialised by a
PostgreSQL advisory lock, so instances starting together do not race: the
second one waits and then finds nothing to do. See
[Database](/ssoossh/operations/database/) for provisioning and the connection
string.

```yaml
db:
  provider: postgres
  connection_string: "postgres://ssoossh:secret@db.example.com:5432/ssoossh?sslmode=verify-full"
```

## 2. Issue the NATS certificates

`ssoosshd` connects with
[`nats.ClientCert`](/ssoossh/reference/config/pubsub/#natscert_file) and
`nats.RootCAs` set, which turns TLS on unconditionally: a broker with TLS
disabled is rejected client-side with
`nats: secure connection not available`, whatever the URL scheme says. So you
need a CA, one server certificate, and one client certificate per `ssoosshd`
process, before anything else.

Two properties matter beyond "it is signed by the CA":

- **The server certificate needs a SAN matching the host in
  [`pubsub.nats.url`](/ssoossh/reference/config/pubsub/#natsurl).** The Go
  client sets `ServerName` from that host and verifies it. A certificate with
  the name only in the Common Name fails with
  `x509: certificate relies on legacy Common Name field`.
- **The client certificates carry no SANs at all**, so that NATS falls back to
  the subject when mapping a certificate to a user. `verify_and_map` tries
  email SANs, then DNS SANs, then URI SANs, and only then the subject
  distinguished name in RFC 2253 form. A stray DNS SAN silently becomes the
  user name and nothing matches.

```bash
umask 077

# The CA. Keep ca-key.pem offline; only ca-cert.pem is deployed.
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -keyout ca-key.pem -out ca-cert.pem \
  -subj "/O=Example Corp/CN=ssoossh-nats-ca"

# The broker's server certificate. List every name and address an ssoosshd
# process might use to reach it, including the cluster route addresses.
cat > server-ext.cnf <<'EOF'
basicConstraints = CA:FALSE
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:nats.example.com, DNS:nats1.example.com, IP:10.0.0.10
EOF

openssl req -newkey rsa:2048 -nodes -keyout server-key.pem -out server.csr \
  -subj "/O=Example Corp/CN=nats.example.com"
openssl x509 -req -in server.csr -CA ca-cert.pem -CAkey ca-key.pem \
  -CAcreateserial -out server-cert.pem -days 825 -sha256 \
  -extfile server-ext.cnf

# One client certificate per ssoosshd process. No subjectAltName: the
# subject DN is what NATS maps to a user.
cat > client-ext.cnf <<'EOF'
basicConstraints = CA:FALSE
keyUsage = critical, digitalSignature
extendedKeyUsage = clientAuth
EOF

for name in ssoossh-api-1 ssoossh-api-2 ssoossh-signer-1; do
  openssl req -newkey rsa:2048 -nodes \
    -keyout "$name-key.pem" -out "$name.csr" \
    -subj "/O=Example Corp/CN=$name"
  openssl x509 -req -in "$name.csr" -CA ca-cert.pem -CAkey ca-key.pem \
    -CAcreateserial -out "$name-cert.pem" -days 825 -sha256 \
    -extfile client-ext.cnf
  # Print the exact string to paste into nats-server.conf.
  openssl x509 -in "$name-cert.pem" -noout -subject -nameopt RFC2253
done
```

That last line prints the user names the broker will see:

```text
subject=CN=ssoossh-api-1,O=Example Corp
subject=CN=ssoossh-api-2,O=Example Corp
subject=CN=ssoossh-signer-1,O=Example Corp
```

Copy them without the `subject=` prefix. Do not retype them by hand; RFC 2253
orders the attributes from most to least specific, which is the reverse of the
`-subj` argument you wrote.

:::caution[Give each process its own certificate]
Not for accounting -- the permissions differ. An API instance holding the
signer's certificate can join the `signer` queue group and take signing jobs
off the queue that it has no CA key to complete, and every job it steals is a
certificate request that hangs until the stranded-request sweep fails it.
:::

## 3. Write nats-server.conf

```text
# /etc/nats/nats-server.conf
listen: 0.0.0.0:4222

# Monitoring endpoints: /healthz for probes, /connz to see which
# certificates actually connected. Bind it to a private interface.
http_port: 8222

tls {
  cert_file: "/etc/nats/certs/server-cert.pem"
  key_file:  "/etc/nats/certs/server-key.pem"
  ca_file:   "/etc/nats/certs/ca-cert.pem"

  # Require a client certificate AND use the identity in it as the NATS
  # user name. Without the "_and_map" half the authorization block below
  # never matches, and every client with a CA-signed certificate gets
  # unrestricted access to every subject.
  verify_and_map: true
}

authorization {
  # Named permission sets, referenced as $API and $SIGNER below. The
  # config parser resolves these within the enclosing block.
  API = {
    publish:   ["certrequest.sign", "certrequest.wait.>",
                "ca.key.request", "notification.send"]
    subscribe: ["certrequest.signed", "certrequest.wait.>",
                "ca.key.announce", "notification.send"]
  }
  SIGNER = {
    publish:   ["certrequest.signed", "ca.key.announce"]
    subscribe: ["certrequest.sign", "ca.key.request"]
  }

  users: [
    { user: "CN=ssoossh-api-1,O=Example Corp",    permissions: $API },
    { user: "CN=ssoossh-api-2,O=Example Corp",    permissions: $API },
    { user: "CN=ssoossh-signer-1,O=Example Corp", permissions: $SIGNER },
  ]
}
```

The two permission sets are the two halves of
[the subject layout](#the-subject-layout): API instances request signatures and
consume results, signers consume jobs and publish results. Neither can do the
other's work, which is the point of mapping certificates to users at all.

`notification.send` only matters when
[`mail.enabled`](/ssoossh/reference/config/mail/#enabled) is true; drop it from
`$API` otherwise. Signers never touch it -- they have no database and no
notion of a recipient.

Adding an instance means adding one line to `users` and reloading
(`nats-server --signal reload`, or `SIGHUP`). Adding a signer is the same.

### Running the broker

```bash
nats-server -c /etc/nats/nats-server.conf
```

```bash
docker run -d --name nats \
  -p 4222:4222 -p 127.0.0.1:8222:8222 \
  -v /etc/nats:/etc/nats:ro \
  nats:2-alpine -c /etc/nats/nats-server.conf
```

The broker needs no persistent volume. JetStream is off, so it holds nothing
across a restart, and nothing in the deployment expects it to.

### A local development broker

`docker run nats:latest` on its own will not work: with no TLS configured the
`ssoosshd` connect fails before it sends a single subject. The smallest thing
that does work is TLS with client verification and no user mapping, which
leaves every client unrestricted -- fine on a laptop, wrong anywhere else:

```text
# nats-dev.conf -- development only, no per-user permissions
listen: 127.0.0.1:4222
tls {
  cert_file: "certs/server-cert.pem"
  key_file:  "certs/server-key.pem"
  ca_file:   "certs/ca-cert.pem"
  verify: true
}
```

Generate the PKI from [step 2](#2-issue-the-nats-certificates) with
`subjectAltName = IP:127.0.0.1, DNS:localhost` on the server certificate, and
point every process at `nats://127.0.0.1:4222`.

### Making NATS redundant

A single broker is a single point of failure for new certificate issuance (see
[what happens when something breaks](#what-happens-when-something-breaks)).
Run three, clustered:

```text
server_name: nats1
listen: 0.0.0.0:4222

cluster {
  name: "ssoossh"
  listen: 0.0.0.0:6222
  routes: [
    "nats://nats1.example.com:6222",
    "nats://nats2.example.com:6222",
    "nats://nats3.example.com:6222"
  ]
  tls {
    cert_file: "/etc/nats/certs/server-cert.pem"
    key_file:  "/etc/nats/certs/server-key.pem"
    ca_file:   "/etc/nats/certs/ca-cert.pem"
    verify: true
  }
}
```

Give each node a distinct `server_name`, the same `cluster.name`, and a server
certificate whose SANs cover both its client address and its route address.
Then list all three in
[`pubsub.nats.url`](/ssoossh/reference/config/pubsub/#natsurl), comma
separated:

```yaml
pubsub:
  nats:
    url: "nats://nats1.example.com:4222,nats://nats2.example.com:4222,nats://nats3.example.com:4222"
```

The client picks one and fails over to another if it drops. Queue groups span
the cluster, so a signer connected to `nats2` still takes jobs published on
`nats1`.

## 4. Verify NATS on its own

Do this before `ssoosshd` is involved, so a certificate problem cannot be
mistaken for a configuration problem.

```bash
curl -sf http://127.0.0.1:8222/healthz && echo ok
```

Then check that a real client certificate authenticates and lands on the right
permissions. With the [`nats` CLI](https://github.com/nats-io/natscli):

```bash
nats -s nats://nats.example.com:4222 \
  --tlscert=ssoossh-api-1-cert.pem \
  --tlskey=ssoossh-api-1-key.pem \
  --tlsca=ca-cert.pem \
  sub 'ca.key.announce'
```

That subscription is one an API instance is allowed to hold, so it proves the
handshake, the mapping, and the permissions in one go. A subscription the
certificate is not permitted to hold is refused with
`Permissions Violation for Subscription to "..."`, which is the same error
`ssoosshd` would log.

Once a signer is running, the same subscription prints its announcement within
five minutes. To ask for one immediately, publish a request in the wire format
the pipeline uses -- a JSON-encoded Watermill envelope, not a bare payload:

```bash
nats -s nats://nats.example.com:4222 \
  --tlscert=ssoossh-api-1-cert.pem \
  --tlskey=ssoossh-api-1-key.pem \
  --tlsca=ca-cert.pem \
  pub ca.key.request '{"UUID":"probe","Metadata":{},"Payload":""}'
```

`/connz?auth=1` on the monitoring port lists what is currently connected and,
usefully, the identity each certificate mapped to:

```bash
curl -s 'http://127.0.0.1:8222/connz?auth=1' \
  | jq -r '.connections[] | "\(.authorized_user)  subs=\(.subscriptions)"'
```

```text
CN=ssoossh-signer-1,O=Example Corp  subs=0
CN=ssoossh-signer-1,O=Example Corp  subs=2
```

If `authorized_user` is not the DN you put in `nats-server.conf`, the mapping
is what is broken, not the permissions. Each `ssoosshd` process opens **two**
connections -- one publishing, one subscribing, deliberately separate so
closing the subscriber cannot drain the publisher -- so expect twice as many
rows as processes.

## 5. Configure the signers

A signer's configuration surface is small, because it has no database, no HTTP
listener, and no identity provider. It is the CA key and the broker:

```yaml
# /etc/ssoossh/signer.yaml
ssh_key: |
  -----BEGIN OPENSSH PRIVATE KEY-----
  ...
  -----END OPENSSH PRIVATE KEY-----

pubsub:
  backend: nats
  nats:
    url: "nats://nats.example.com:4222"
    cert_file: "/etc/ssoossh/nats/ssoossh-signer-1-cert.pem"
    key_file:  "/etc/ssoossh/nats/ssoossh-signer-1-key.pem"
    ca_file:   "/etc/ssoossh/nats/ca-cert.pem"

logging:
  level: info
  format: json
```

Replace `ssh_key` with an [`hsm`](/ssoossh/operations/hsm/) block to keep the
CA key in a PKCS#11 token. Exactly one of the two must be set.

Run it with `ssoosshd sign --config /etc/ssoossh/signer.yaml`. Several signers
may run at once; they compete for jobs through the `signer` queue group, and
they need not share a CA key -- every announced key is accepted, which is what
makes rotation work.

:::note[What a signer prints, and when]
[`logging.level`](/ssoossh/reference/config/logging/#level) defaults to
`WARN`. One line is exempt, so that a healthy process is never completely
silent:

```text
INF ssoosshd is starting type=startup mode=sign
```

That line says the process came up and which mode it is in. It says nothing
about whether it reached NATS -- the broker connection happens after it. For
that, and for the subscription that actually matters, set `logging.level:
info` as the example above does and compare against
[step 7](#7-start-in-order).

A failure before or during the broker connection goes to stderr and exits
non-zero at any level:

```text
ssoosshd: failed to initialize pub/sub: failed to create NATS publisher:
cannot connect to nats: nats: no servers available for connection
```
:::

## 6. Configure the API instances

```yaml
# /etc/ssoossh/api.yaml -- identical on every API instance
multi_instance: true

http:
  public_url: "https://ssh.example.com"
  # The same value on every instance.
  cookie_key: "your-secret-key-here-32-bytes-minimum"
  trusted_proxies: ["10.0.0.0/24"]   # the load balancer

db:
  provider: postgres
  connection_string: "postgres://ssoossh:secret@db.example.com:5432/ssoossh?sslmode=verify-full"

pubsub:
  backend: nats
  nats:
    url: "nats://nats.example.com:4222"
    cert_file: "/etc/ssoossh/nats/ssoossh-api-1-cert.pem"
    key_file:  "/etc/ssoossh/nats/ssoossh-api-1-key.pem"
    ca_file:   "/etc/ssoossh/nats/ca-cert.pem"
```

Only the three NATS paths differ per instance, and only because each process
holds its own client certificate. Everything else is byte-identical, which is
worth enforcing with whatever ships the file.

There is deliberately no `ssh_key` and no `hsm` block here. API mode neither
needs nor wants the CA key: it learns the CA public key from the signers'
announcements. See
[Startup modes](/ssoossh/operations/startup-modes/#the-three-modes).

Run it with `ssoosshd serve api --config /etc/ssoossh/api.yaml`. A complete
worked example is on
[Server configuration examples](/ssoossh/examples/server-configs/).

### cookie_key

Left empty, a key is generated once and persisted in the database's
`server_secrets` table, so sessions survive a restart and instances sharing a
database share the key. Setting it explicitly keys sessions from outside the
database, which is what `multi_instance: true` insists on: with it unset under
`multi_instance`, `ssoosshd` fails at startup. If the value differs between
instances, people get logged out at random as requests land on instances with
different keys.

Any 32 bytes or more of secret will do:

```bash
openssl rand -base64 48
```

## 7. Start in order

Core NATS delivers nothing to a subscriber that is not yet listening, so
bring-up order matters on a cold start:

1. **PostgreSQL**, then **NATS**. Both are dependencies of everything else.
2. **The signers.** Wait for each to log its subscription to the sign queue.
   An API instance that starts first and receives an approval before any
   signer is listening publishes a job into an empty queue group, and the
   client waits out the stranded-request sweep for nothing.
3. **The API instances.** Each logs `NATS transport initialized` with the URL
   it reached, then asks for a CA key announcement to seed its registry.
4. **The load balancer**, once `GET /api/ca` on an instance returns a key.

A signer that reached the broker and subscribed logs exactly this, at
`logging.level: info`. Only the first line survives the default `WARN`:

```text
INF ssoosshd is starting type=startup mode=sign
INF NATS transport initialized type=queue url=nats://nats.example.com:4222
INF Adding handler type=queue handler_name=certrequest-signer topic=certrequest.sign
INF Adding handler type=queue handler_name=ca-key-announcer topic=ca.key.request
INF Running router handlers type=queue count=2
INF Starting handler type=queue subscriber_name=certrequest-signer topic=certrequest.sign
INF Starting handler type=queue subscriber_name=ca-key-announcer topic=ca.key.request
```

`Starting handler` on `certrequest.sign` is the line that matters: before it,
the queue group has no member and any job published is discarded.

In systemd terms that is `After=` and `Wants=` on the broker unit where the
processes are co-located, and start-up ordering in whatever orchestrates them
where they are not. Nothing depends on this ordering after the fact: a signer
restarted later re-announces at startup, and API instances re-request on
theirs.

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

## The CA public-key registry has a clock

Two intervals are worth knowing, because together they decide how long
`GET /api/ca` keeps working after the signers stop:

- Each signer announces its CA public key at startup and every **5 minutes**
  thereafter.
- An API instance holds an announced key for **15 minutes** past the last
  announcement, then sweeps it.

So a signer can miss two announcements without anything visible happening, and
losing every signer empties the registry about 15 minutes later. That shows up
first as `ssoossh ca` and `pam_ssoossh`'s trusted-CA fetch returning nothing,
not as signing failures.

## Scheduled jobs run on every instance

Scheduled jobs are not leader-elected: each instance registers and runs every
job on its own timer. That is deliberate for the sweeps -- they are idempotent
and running one twice costs nothing -- but it is what makes the
[directory sync's](/ssoossh/operations/ldap/) auto-disable threshold a
duration rather than a count of passes. Three replicas observe an absence
three times per interval, so a pass count would mean something different in
every deployment.

Two consequences worth knowing:

- Each `ldap_sync_runs` row names the instance that produced it, since that is
  the log to go and read.
- A manual sync runs on whichever instance answered the request. There is no
  queue across replicas, and the single-flight guard is per instance.

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

:::caution[A long outage needs a restart]
The NATS client reconnects on its own with the library defaults: 60 attempts
per broker, two to three seconds apart. An outage that outlasts that -- two to
three minutes against a single broker, proportionally longer against a cluster
-- exhausts the attempts, and the client closes the connection for good rather
than continuing to retry. Restart the `ssoosshd` processes after any broker
outage longer than a few minutes, and treat `NATS transport initialized` in
the log as the confirmation they came back.
:::

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
| a process logs `ssoosshd is starting` and nothing further | working as configured: [`logging.level`](/ssoossh/reference/config/logging/#level) defaults to `WARN`, which exempts that one line and drops the rest of the startup sequence. Set `logging.level: info` to see the broker connection and subscriptions |
| startup fails naming `gochannel` | a split mode with [`pubsub.backend`](/ssoossh/reference/config/pubsub/#backend) left at the in-process default |
| startup fails on mTLS credentials | `pubsub.backend: nats` with `cert_file`, `key_file`, or `ca_file` unset or unreadable |
| startup fails on the cookie key | `multi_instance: true` with no explicit `http.cookie_key` |
| `nats: secure connection not available` | the broker has no `tls` block; `ssoosshd` always negotiates TLS |
| `x509: certificate signed by unknown authority` | `ca_file` is not the CA that signed the broker's server certificate |
| `x509: certificate is valid for X, not Y` | the server certificate's SANs do not cover the host in `pubsub.nats.url` |
| `x509: certificate relies on legacy Common Name field` | the server certificate has no SAN at all |
| `nats: Authorization Violation` | the client certificate's mapped identity is not in the broker's `users` list, or `verify_and_map` is not set |
| `Permissions Violation for Publish to "certrequest.sign"` | the API user's permission set is missing a subject from [the subject layout](#the-subject-layout) |
| a signer takes jobs and nothing is signed | two processes sharing one client certificate, so an API instance joined the `signer` queue group |
| users are logged out at random | `cookie_key` differs between instances |
| `ssoossh ca` returns nothing | no signer has announced in the last 15 minutes, or the `ca.key.*` subjects are not permitted |
| every recipient gets one mail per instance | the `notification.send` queue group is not in effect |
| everyone lands in the most generous lifetime tier | `trusted_proxies` does not name the load balancer, so every request carries its address |

A complete multi-instance `ssoosshd.yaml` is on
[Server configuration examples](/ssoossh/examples/server-configs/).
