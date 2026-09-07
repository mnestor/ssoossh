---
paths: 
  - server/resources/migrations/**/*.sql
---
 
# Migration Safety Rules
 
- Always include rollback instructions
- Test migrations on a copy of production data first
- Never delete columns in the same migration that removes code using them
- Add columns as nullable first, populate, then add constraints
- Migrations live in two dialect trees, `server/resources/migrations/postgres/`
  and `server/resources/migrations/sqlite/` (golang-migrate). A schema change
  needs a matching migration in both — write and number them together, don't
  patch one dialect and forget the other.
- The project is released, so a schema change is a new, timestamp-numbered
  migration pair (`YYYYMMDDHHMMSS_name.{up,down}.sql`) in both dialect trees.
  Never edit an existing migration, `20260101000000_init` included: deployed
  databases have already applied it and golang-migrate will not re-run it.
  `test/migration`'s goldens pin the schema each dialect's chain builds, so a
  change that lands somewhere else fails rather than passing unnoticed.
  Refresh them with `go test ./test/migration/ -update` (add `-tags
  dbparity`, which needs docker, for the Postgres golden) only when the
  schema change is intended.