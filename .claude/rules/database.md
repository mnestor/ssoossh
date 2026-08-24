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
- Before the first release there is one migration per dialect and a schema
  change edits it in place; nothing is deployed for an incremental migration
  to describe. `test/migration`'s goldens are what make that safe — they pin
  the schema each dialect's migration builds, so a reshape that lands
  somewhere else fails rather than passing unnoticed. Refresh them with
  `go test ./test/migration/ -update` (add `-tags dbparity`, which needs
  docker, for the Postgres golden) only when the schema change is intended.
  Once released, add a numbered migration instead of editing this one.