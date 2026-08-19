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