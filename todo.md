* hook up acme for http tls

* Coverage exclusions: DONE 2026-08-23. `exclude-from-coverage.txt` and the
  two grep filters in the Makefile are gone; every block that genuinely
  cannot be tested now carries a `not covered:` comment at the code
  (`rtk grep -rn "not covered:" --include='*.go'`). Measured before removal:
  51 of 89 patterns matched no block at all, and the whole mechanism was
  worth half a point (83.1% unfiltered vs 83.6% filtered, 23 statements).

* Test worklist, from the blocks that lost an exclusion or never had one.
  All are reachable; none are annotated, so they show as real gaps.
  - Cheap: `server/config/config.go` four Validate() error paths (79, 85,
    93, 100); `server/config/types_pubsub.go` (9 of 12 statements);
    `server/bootstrap/pipeline.go:50` bad CA key; `server/cmd/sign.go`
    PreRun/Run; `server/pubsub/pubsub.go` New() backend selection (56-72);
    `server/frontend/frontend_included.go:184` redirect with a query
    string; `internal/tools/gendocs/main.go:60` via a bogus
    SOURCE_DATE_EPOCH.
  - `server/service/certrequest.go`: `EvictResolved` and
    `ValidateStartupConfig` are entirely uncovered; so are the invalid
    principal guards (406, 823), the unsupported-flow default (545), both
    `evaluateDuration` error paths (645, 783), `serial.New` (773), the
    enrollment insert error (697), and the SSE fast-path fallbacks
    (1027, 1143, 1162, 1190, 1192).
  - Needs a helper first, then covers several blocks each:
    (1) a `sessions.Store` fake that fails on the Nth Save, which unlocks
    the seven session-write branches in `server/controller/auth.go` that
    `oversizedSessionSeed` cannot reach; (2) per-query DB fault injection
    (a gorm callback keyed on table/op), which unlocks the six
    `certrequest.go` branches whose `not covered:` notes name it.
  - `server/pubsub/pubsub.go` `newNATS` needs a NATS server, so it belongs
    to an integration suite rather than the unit profile.
