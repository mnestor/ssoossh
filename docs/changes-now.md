# Changes: do now

The immediate queue. Every item here is small, independent, and has no
blocker — none of them wait on a design decision or another feature.

Consolidated 2026-08-22 from the security/API audit (2026-08-21), the
feature review (2026-08-22), and the comparative audit (2026-08-22). Items
marked **re-verified** were checked against the working tree on 2026-08-22
rather than trusted from the source review; items marked *carried* were not
re-checked and should be confirmed before starting.

## Security and correctness

| # | Severity | Change | Evidence |
| --- | --- | --- | --- |
| 1 | High | Upgrade Go 1.26.5 → 1.26.6. Seven known stdlib CVEs, fixed in .6. | **Re-verified**: `go.mod` still pins `go 1.26.5`. |
| 2 | High | Approval page accessibility: add a global `:focus-visible` outline rule, and `aria-busy`/`aria-live` on the approve/deny buttons and error alert. | **Re-verified**: grep for `focus-visible`/`aria-live`/`aria-busy` across `frontend/src` returns nothing. |
| 3 | Medium | Add `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` in `server/middleware/`, alongside the existing CSP and HSTS middleware. ~3 lines. | **Re-verified**: absent from `server/middleware/`; CSP and HSTS exist as separate files, these three do not. |
| 4 | Medium | Add `ValidatePrincipal(p string) error` (e.g. `^[a-zA-Z0-9._-]+$`) in `internal/crypto/ssh/`, called before a principal is persisted or signed into a certificate. | **Re-verified**: no such function anywhere in the codebase. SSH principal strings currently reach certificates unvalidated. |
| 5 | Medium | Run a fresh `pnpm audit` and stop relying on the checked-in `pnpm-audit-results.json` snapshot for release sign-off. Process item, not code. | *Carried* from the 2026-08-21 audit: snapshot dated 2026-08-15 and not verifiable in that environment. |
| 6 | High | Normalize `time.Time` to UTC on write, so SQLite stores a `Z` suffix and lexicographic order equals chronological order. Prefer a GORM write callback or serializer over hand-adding `.UTC()` per call site, add a regression test writing from a `time.FixedZone`, and backfill existing rows in the same migration. | See [database-schema-audit-2026-08-22.md](database-schema-audit-2026-08-22.md) finding 1 — reproduced empirically, not inferred. |

Why #6 is High: it is the only **live correctness bug** in the current
backlog, and it is a real divergence between the two supported backends. The
SQLite driver stores `time.Time` as wall-clock text with the writer's UTC
offset appended, and the `DATETIME` column's NUMERIC affinity leaves it as
TEXT — so every `<`, `>`, and `ORDER BY` on a timestamp is a string
comparison. Nothing in the write path normalizes to UTC.

Steady state masks it, because every value and cutoff comes from `time.Now()`
in the same process zone. It stops being masked whenever offsets differ:

- **DST transitions**, twice a year on any non-UTC deployment — stranded
  `signing` requests get skipped by `SweepStrandedRequests` in the autumn
  direction and swept early in the spring direction.
- **A timezone change** — adding `TZ=UTC` to a container permanently
  mis-orders every pre-change row against every post-change row.
- **Any future `.UTC()` on a bound parameter**, which alone turns the sweep
  and TTL predicates into "match everything".

Postgres is unaffected (`TIMESTAMPTZ` stores a true instant). The two
backends genuinely disagree about the meaning of `created_at < ?`, and
SQLite's answer is the wrong one.

Why #2 is High and not cosmetic: the approval page is the actual human
security control in the system — it is where a person decides whether a
certificate gets issued. A keyboard user cannot see which control is focused
(WCAG 2.4.7 failure), and a screen-reader user gets no feedback that an
approve or deny actually happened.

## Frontend wiring (unblocks once the audit-metadata work lands)

| # | Change | Evidence |
| --- | --- | --- |
| 7 | Wire `frontend/src/routes/dashboard/+page.svelte`, `frontend/src/routes/logs/me/+page.svelte`, and `frontend/src/lib/components/DetailRow.svelte` to the `decided_by_*`, `decided_source_ip`, `local_username`, `local_hostname`, and interface-IP fields, replacing the current placeholder (em-dash / marked sample value) rendering. | **Re-verified**: the backend fields exist and are exposed in `server/webtypes/webtypes.go:83-90` and `internal/apitypes/certrequest.go`, but no frontend component references any of them yet. |

## Completed 2026-08-22

Recorded so the next pass does not redo them.

- [x] **Docs folder cleanup.** Stripped dead `release-plan.md` /
      `release-phase*.md` references from all 11 citing documents; deleted
      `project-guidelines.md` (duplicated root `CLAUDE.md` almost verbatim)
      and `docker-setup.md` (four lines of personal devcontainer notes);
      rebuilt `docs/README.md`'s index, which was missing seven documents
      that exist in the folder; removed the dead
      `security-review-2026-08-11.md` link.
- [x] **Consolidated the three review documents** into this file,
      [changes-next.md](changes-next.md), and [deferred.md](deferred.md).
- [x] **Decision actor and source IP persisted.** `Approve`/`Deny` now record
      who decided and from where, in a dedicated append-only
      `certificate_request_decisions` table
      (`server/model/certificate_request_decision.go`, migration
      `20260822000000_certificate_request_decisions`).
- [x] **`Approve`/`Deny` wrapped in DB transactions.** The multi-write
      sequence the 2026-08-21 audit flagged as crash-inconsistent is now
      transactional (`server/service/certrequest.go:533,626,707`).
- [x] **Client-local identity captured.** `local_username` / `local_hostname`
      on the user-cert request path (`client/cmd/ssh_login.go`,
      `internal/apitypes`).
- [x] **Local interface IP collection** (`internal/api/localaddrs.go`).

The last four were listed as still-open in the 2026-08-22 feature review;
they were completed after that review was written and re-verified as done on
2026-08-22. Do not re-add them to a plan.
