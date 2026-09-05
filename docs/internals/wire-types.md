# Wire types: keeping the server, the client, and the web UI in agreement

Three consumers read ssoosshd's JSON: the Go client (`internal/api`), the
`pam_ssoossh` module, and the SvelteKit web UI. Nothing in the language
forces them to agree, so each pairing has an explicit mechanism.

## Go server ↔ Go client

**Shared types.** `internal/apitypes` is imported by both sides, so a renamed
field is a compile error rather than a runtime surprise. On top of that,
`server/controller/contract_test.go` drives the real `internal/api` client
against real controllers over a real listener — it catches the two sides
disagreeing about the envelope, which shared structs alone would not.

Nothing to remember here: the compiler does the work.

## Go server → web UI

The frontend's types are **generated**, not written.

```
server/model/enums.go       ─┐
internal/apitypes/*.go      ─┼─ tygo ─→ frontend/src/lib/api/generated/
server/webtypes/webtypes.go ─┘
```

- `server/webtypes` holds the response shapes and nothing else. It exists as
  its own package because tygo works a package at a time, and pointing it at
  `server/controller` would drag handlers and service interfaces into view.
  The converters that build these shapes stay in
  `server/controller/responses.go`.
- `server/model/enums.go` holds the string enumerations. They are split out of
  the files defining the GORM models so the generator emits the unions without
  also emitting database rows — `CertificateRequest.FailureReason` is not part
  of any API response and should not appear in the frontend's types.
- `frontend/src/lib/api/types.ts` is the only hand-written file left, and it
  declares no shapes. It re-exports the generated ones under the names the app
  uses (`RequestDetailResponse` → `RequestDetail`).

**Workflow.** Change the Go struct, run `make types`, commit the result.
`make types-check` asserts that regenerating changes nothing, and CI runs it
(`.github/workflows/build-test.yaml`).

### What generation buys beyond field names

Go doc comments carry across as TSDoc, so the reasoning next to a field is
visible at the call site in an editor. More usefully, the enums become
TypeScript unions: adding a `CertificateRequestStatus` constant in Go and
regenerating makes `pnpm check` fail in `StatusBadge.svelte`, because its
`Record<RequestStatus, string>` no longer covers every case. A new status
cannot reach production rendering as an unstyled blank.

### The one deliberate exception

`Envelope<T>` is hand-written in `types.ts` even though `apitypes.Envelope`
generates cleanly. The Go struct is the Go client's decode target and
describes success only — `Data T` is non-nullable and `Error` is omitted when
empty. What the server actually writes is looser at both ends: a success is
`{"data": …, "error": null}` and a failure is `{"data": null, "error": "…"}`.
A browser trusting the generated type would be wrong on every error response.
The envelope's real shape is pinned by the assertions in
`server/controller/webapi_test.go` instead.

## The golden test

`server/webtypes/golden_test.go` marshals a fully-populated instance of every
response type and diffs it against a checked-in JSON golden. Two distinct
failures matter:

- **Renames and retypes** show up as a golden diff.
- **Additions** show up through `assertAllFieldsSet`, a reflective walk that
  fails when any field in the fixture is still zero. Without it, a new field
  carrying `omitempty` would serialize to nothing and produce an identical
  golden — passing while the frontend knows nothing about it.

A second set of goldens marshals the zero value of each type, documenting
exactly which fields disappear under `omitempty`. That set is the frontend's
justification for marking a field optional.

Accepting an intended change is three steps, and the failure message says so:

```
go test ./server/webtypes/ -update   # accept the new shape
make types                           # regenerate the TypeScript
make openapi                         # regenerate the spec
```

## Go server → docs/openapi.yaml

The spec is generated too, by [swag](https://github.com/swaggo/swag) v2 in
OpenAPI 3.1 mode. `make openapi`; `make openapi-check` asserts regenerating
changes nothing and runs in CI.

Annotations live on the handlers in `server/controller`, so the description of
an endpoint sits beside the code that serves it and moves when the code moves.
The general API info and the response envelope types live in
`server/openapidoc`.

### Two things worth knowing before editing annotations

**`required` comes from `validate:"required"` tags.** swag emits a schema's
`required` list from `binding:` or `validate:` struct tags and has no other
source for it. Request bodies already carried `binding:"required"`; response
types now carry `validate:"required"`, which nothing reads at runtime — no
validator runs over a response — and exists solely so the spec says which
fields are always sent. The rule to follow when adding a field: mark it
required exactly when it has no `omitempty`, which is the same rule that
decides whether tygo marks the TypeScript field optional.

**The envelope is written out once per response.** The natural spelling is
`apitypes.Envelope[T]`, but swag v2.0.0-rc5 cannot resolve a type parameter,
and its composition syntax (`Envelope{data=T}`) is actively wrong here: it
names every composed body `data` in `components/schemas`, so they overwrite
each other and endpoints end up documented with another endpoint's payload.
Hence the explicit types in `server/openapidoc`. They reference the real
payload types, so field changes still flow through automatically; only the
wrapper is manual. When swag ships generics, all of them collapse.

## Go server → pam_ssoossh

`pam_ssoossh` lives in its own repository,
[github.com/mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam), and
is written in C. It shares no code with this one, so unlike the two pairings
above there is no compiler, no generator and no shared type anywhere in it.
A renamed json tag here regenerates `docs/openapi.yaml` without complaint,
passes every gate in this repository, and breaks a production `sudo`.

Two contracts cross that boundary:

- **The HTTP wire shapes** in `internal/apitypes`, plus the SSE event names,
  which are the terminal statuses in that package.
- **The principals-map file format**, which `internal/principalsmap` parses
  here and the module parses on the host. This one has no artifact yet; it is
  specified only by the doc comment on that package.

Three things carry the first across.

**Golden fixtures.** `internal/apitypes/testdata/` holds one encoded instance
of every wire type — `.full.json` with every field set, `.zero.json` showing
which fields vanish under `omitempty`. `server/controller/testdata/` holds the
raw bytes of each terminal SSE event, captured by driving the real handler.
They are plain files, so the C side reads them with no Go toolchain: it runs
its own decoder over each one and fails its own build when the two disagree.

The SSE goldens exist because that is the part `docs/openapi.yaml` cannot
describe. The spec says so itself — the response is a stream, so there is
nowhere to declare a schema — and the two conventions it therefore cannot
state are both easy to get wrong. The status is in the event name and not in
the payload (`sse_stream_denied.sse` is `{"data":{}}` and nothing else), and
the framing is `event:approved` with no space after the colon.

**A versioned manifest.** `docs/wire-contract.json` records the endpoint set,
the terminal event names, and a SHA-256 per fixture, and bumps its own
`version` whenever any of those move. `make wire-contract-check` is the merge
gate. None of this prevents a breaking change; it makes one show up in review
as a version bump, and gives the other repository a number to pin.

The endpoint set is read from `docs/openapi.yaml`'s paths rather than the spec
being hashed whole, deliberately: editing a handler's `@Description` should
not bump the contract version.

**A release asset.** `make wire-contract-bundle` packs the spec, the manifest
and every fixture into a tarball that goreleaser attaches to the release
(`release.extra_files`), with `wire-contract.json` uploaded loose alongside it
so a consumer can read the version with one request. `ssoossh-pam` pins a
version and tests against the bundle for it.

**Workflow.** A breaking change to either contract is a change in two
repositories:

```
go test ./internal/apitypes/ -update    # the payload shapes
go test ./server/controller/ -update    # the SSE framing
make openapi                            # the spec
make wire-contract                      # bumps docs/wire-contract.json
```

Then open the matching change in `ssoossh-pam` before releasing either side.

## What is still hand-maintained

Nothing describing a shape. The remaining judgement calls are which handler
gets which `@Description`, and keeping that prose true — the same obligation
as any other comment.
