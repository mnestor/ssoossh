---
paths:
  - server/resources/**/*.go
  - server/service/**/*.go
  - server/model/**/*.go
---

## Database Access Rules

- Use parameterized queries exclusively
- Never match serialized JSON as text: no `LIKE` against a payload's
  encoded form. It depends on key order and encoding, and it cannot use an
  index. To filter on a field inside a stored document, promote the field
  to its own indexed column, written from the same event and backfilled by
  a migration in both dialect trees, as `audit_events.action` and
  `audit_events.source_ip` are. An occasional query off the hot path may use
  the databases' JSON operators instead (`payload::jsonb ->> 'field'` on
  Postgres, where these columns are `TEXT`; `payload ->> 'field'` on
  SQLite), with an expression index in both trees and a guard for rows
  whose payload does not parse.
