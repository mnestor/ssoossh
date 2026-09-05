---
title: Database
description: SQLite or PostgreSQL, connection strings, pooling, migrations, and what is actually stored.
eyebrow: Server operations
sidebar:
  order: 6
---

`ssoosshd` keeps its state in one database: SQLite by default, PostgreSQL when
more than one process shares it. This page covers choosing between them, the
connection settings, and what the rows actually hold -- which matters, because
what is *not* in there is as important as what is.

## SQLite or PostgreSQL

| | SQLite | PostgreSQL |
| --- | --- | --- |
| [`db.provider`](/ssoossh/reference/config/db/#provider) | `sqlite` (the default) | `postgres` |
| Instances | one | any number |
| Setup | none; a file appears | a server you run |
| Good for | a single instance, development, testing | multi-instance, or an existing database estate |

SQLite is the right answer for a single instance and stays the right answer
until you need a second one. Multi-instance requires PostgreSQL: SQLite is
single-connection and cannot be shared between processes.

## Connection strings

For SQLite,
[`db.connection_string`](/ssoossh/reference/config/db/#connection_string) is a
file path. The default is the relative `ssoossh.db`, which is almost never
what you want under systemd -- put it inside the unit's `StateDirectory`:

```yaml
db:
  connection_string: /var/lib/ssoossh/ssoossh.db
```

`:memory:` is accepted and is for tests: the connection pool is forced to a
single connection so every caller shares the same in-memory data, and
everything disappears when the process exits.

For PostgreSQL, either a standard URL or a keyword string:

```yaml
db:
  provider: postgres
  connection_string: "postgres://user:pass@db.example.com/ssoossh"
```

:::caution
The connection string can carry a password, so `ssoosshd.yaml` is sensitive
for a second reason beyond the CA key. It is excluded from the effective
configuration auditors can read.
:::

## Pooling and retries

```yaml
db:
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 0
  retry_attempts: 3
  retry_interval: 3s
```

| Key | Default | Notes |
| --- | --- | --- |
| [`db.max_open_conns`](/ssoossh/reference/config/db/#max_open_conns) | `10` | Zero means Go's default (typically 2). For file-backed SQLite keep it modest: writes serialize on the write lock regardless, so a high count just queues. For PostgreSQL, scale with expected concurrency. Ignored for in-memory SQLite, which is always 1 |
| [`db.max_idle_conns`](/ssoossh/reference/config/db/#max_idle_conns) | `5` | Keep it proportionate to `max_open_conns`; a high open count with a low idle count causes expensive churn |
| [`db.conn_max_lifetime`](/ssoossh/reference/config/db/#conn_max_lifetime) | `0` (unlimited) | Useful for breaking stale connections in a long-running deployment; typically unnecessary against a well-behaved server |
| [`db.retry_attempts`](/ssoossh/reference/config/db/#retry_attempts) | `3` | Connection attempts before giving up. Zero or negative fails on the first error. SQLite usually succeeds immediately; PostgreSQL may retry through a transient network failure |
| [`db.retry_interval`](/ssoossh/reference/config/db/#retry_interval) | `3s` | Only used when `retry_attempts` is above 1 |

Query logging is routed separately from the application log, so it can be
noisier or quieter than everything else:
[`db.logging.level`](/ssoossh/reference/config/db/#logginglevel) (default
`WARN`), plus
[`db.logging.add_source`](/ssoossh/reference/config/db/#loggingadd_source) and
[`db.logging.log_json`](/ssoossh/reference/config/db/#logginglog_json). Give
it a `filename` to split it into its own rotating file; until then its records
go to the general log.

## Migrations

Migrations are embedded in the binary and applied at startup -- there is no
separate migrate command. They are explicit (no automatic schema inference
from the models) and guarded against version skew: if the database's migration
version is *newer* than the running build supports, the server refuses to
start rather than operating against a schema it does not understand.

```text
database version (42) is newer than application version (41), downgrades are
not allowed (set ALLOW_DOWNGRADE=true to enable)
```

That is the message a rolled-back deployment produces. `ALLOW_DOWNGRADE=true`
in the environment turns the refusal into a warning; it is an escape hatch,
not a routine step. In a multi-instance deployment it means the oldest
instance decides what schema version is safe, so upgrade the fleet before you
migrate, not after.

## What is stored

| Rows | What they are |
| --- | --- |
| Certificate requests | one per request: type, requested options, the source address observed at creation, and the status the waiting client polls |
| Decisions | one per approval or denial, including the structured `policy_explanation` JSON that says why a certificate got the lifetime it did |
| Certificates | metadata only: type, owner, request ID, public key fingerprint, serial, key ID, principals, granted extensions and critical options |
| Enrollments | the service-account enrollment: the code, the enrolled public key, and the option set, key ID, principals and certificate duration fixed at approval |
| Retrieval log | one row per redemption of an enrollment code, successful or not |
| Users | one row per person who has logged in, their captured extra claims, and the disable state (`disabled_at`, `disabled_by_user_id`, `disabled_reason`, `disabled_source`) |
| `user_groups` | one row per (user, group, source), where source is `oidc` or `ldap`. Never an authorization input |
| `user_ldap` | the directory anchor for a user: DN, attributes, `last_seen_at`, and the consecutive-miss counter |
| Audit events | the bounded cache behind the web UI's history views |
| Sessions | the web session store |
| `server_secrets` | the generated session cookie key, when `http.cookie_key` is unset |
| CA signer keys | the registry of CA public keys signers have announced, which `GET /api/ca` serves |
| `enrollment_reassignments` | frozen history. Nothing writes to it since ownership stopped being transferable; the rows record transfers that really happened |

### What is not stored

- **Private keys.** The client generates the keypair and sends only the public
  key. This is a hard invariant.
- **Signed certificates.** The server never persists the certificate it
  issued. Delivery to the waiting client is the only copy, so there is no
  certificate store to steal, and a client that misses the delivery window
  re-requests instead of fetching it again. The `certificates` rows above are
  the audit record of an issuance, not the certificate.
- **Group membership as authorization.** `user_groups` exists so a
  notification can be fanned out to a group. Admin, SOC, auditor and the
  certificate policy gates are all evaluated from the session identity.

The enrollment code *is* in the enrollments row -- it has to be, since a
redemption is matched against it. It is never written to a log line, never
placed in an audit payload, and never put in an email.

## Retention

Only the audit table is pruned, and only in the database:

```yaml
audit:
  retention: 1440h        # 60 days; zero disables age pruning
  max_rows: 1000000       # safety valve behind retention; zero disables it
  sweep_interval: 1h
```

That window bounds the web UI's history, not the deployment's, because the
shipped audit log is the archive. See
[Audit log](/ssoossh/operations/audit-log/).

Nothing else has a retention setting. Certificate requests, decisions,
certificate metadata, enrollments and the retrieval log accumulate; a stranded
request is failed by a sweep rather than deleted, so the row stays.

## Availability

One instance losing the database does not affect the others, but that instance
is out of service until it comes back -- and login is fail-closed, so a
transient database error during a disabled-user check denies rather than
admits. In a multi-instance deployment the database is the single thing that
has to be highly available; replication and failover are yours to arrange.
