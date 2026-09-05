---
paths:
  - internal/apitypes/**
  - internal/principalsmap/**
  - server/controller/**
---

## The Cross-Repository Wire Contract

`pam_ssoossh` lives in its own project,
[github.com/mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam), and
is written in C. It shares no code with this repository: no imported package,
no generated type, no compiler between the two sides. It is the only consumer
of this API that nothing in CI can typecheck.

Two contracts cross that boundary.

- **The HTTP wire shapes** in `internal/apitypes`, plus the SSE event names,
  which are the terminal statuses in that package.
- **The principals-map file format**, parsed by `internal/principalsmap` here
  and by the module's own parser on the host. Widening what this one accepts
  produces files the other rejects. This contract has no artifact yet; the
  doc comment on the package is its whole specification.

### Changing a shape

Four steps, in this order, in one commit:

```
go test ./internal/apitypes/ -update    # the payload goldens
go test ./server/controller/ -update    # the SSE framing goldens
make openapi                            # docs/openapi.yaml
make wire-contract                      # bumps docs/wire-contract.json
```

`make wire-contract-check` is the merge gate, and `docs/wire-contract.json`'s
`version` is what `ssoossh-pam` pins. The bump is automatic; its job is to put
the change in front of a reviewer, not to prevent it. Land the matching change
in `ssoossh-pam` before releasing either side.

### What the goldens are for

`internal/apitypes/testdata/` and `server/controller/testdata/` are not only
regression tests for the Go side. They ship as a release asset
(`make wire-contract-bundle`) so a C build with no Go toolchain can run its
own decoder over the same files. Adding a wire type without a fixture leaves
the other side guessing.

Two conventions the SSE goldens exist to pin, because `docs/openapi.yaml`
structurally cannot state them and both are guessed wrong by default:

- The status is carried by the SSE **event name**, not the payload.
  `CertificateResult.Status` is `json:"-"`, and `sse_stream_denied.sse` is
  `{"data":{}}` with nothing else in it.
- The framing has **no space after the colon**: `event:approved`, not
  `event: approved`. The space is optional in the SSE grammar and present in
  most examples; gin does not emit it.

### What no longer applies

Older comments in `internal/api` and `internal/principalsmap` justify a
design by the PAM module linking that code into `sudo` — a Go module built
from this repository used to. It does not exist any more. Those designs are
kept on their own merits and the comments say so; do not restore the linkage
claim, and do not treat "PAM needs it" as a live argument for anything except
the two contracts above.
