---
paths:
  - server/controller/**/*.go
---

## API Design Rules

- All handlers return `{ data, error }` shape
- All controller API methods need swag annotations (`@Summary`, `@Tags`,
  `@Success`, `@Failure`, `@Router`). `docs/openapi.yaml` is generated from
  them by `make openapi` and must never be hand-edited
- Rate limit headers on all routes
- Response shapes for the web UI go in `server/webtypes`, not in
  `server/controller` — tygo generates the frontend's TypeScript from that
  package. After changing one, run `make types`, `make openapi`, and
  `go test ./server/webtypes/ -update`. See
  https://mnestor.github.io/ssoossh/internals/wire-types/.
- Mark a response field `validate:"required"` exactly when it has no
  `omitempty`; that tag is the only thing swag reads to build the spec's
  `required` lists.
