# Phase 4: PAM certificate type, server side

**Status: planned.** Part of [release-plan.md](release-plan.md).

## Goal

Make the server issue PAM certificates: a config section, a creation
endpoint, an approval path, and a signer mapping. At the end of this phase a
PAM certificate can be requested, approved in the browser, signed, and
delivered over SSE, with no client module involved yet.

## What already exists

More than the old plan implies. The type is plumbed through the model layer:

- `model.CertificateTypePAM` is defined at `server/model/enums.go:17`,
  alongside user, host, and service.
- The request lifecycle, SSE delivery, sign queue, and signer are all
  type-agnostic and already carry user certificates end to end
  (`server/service/pipeline_test.go`).
- `keyIDTemplateData` (`server/service/keyid.go:15-22`) already has
  `Username`, `Subject`, `Email`, `ClientIP`, `Hostname`, and `UniqueID`. PAM
  needs no new fields: the holder is the approver, same as user certificates.

What is missing is the type-specific policy at three named places.

## The three rejection points

Each one currently fails closed, which is correct, and each has to be opened
deliberately.

### 1. Config has no PAM section

`server/config/types_certificates.go:8-10`:

```go
User    CertOptionsUser    `mapstructure:"user"`
Service CertOptionsService `mapstructure:"service"`
Host    CertOptions        `mapstructure:"host"`
```

Add a fourth field:

```go
PAM CertOptionsPAM `mapstructure:"pam"`
```

Model it on `CertOptionsUser`, which has `RequireGroup`, `ValidDuration`,
`Extensions`, and `KeyIDTemplate`. PAM's differences:

- **`ValidDuration` should be very short.** A PAM certificate is validated
  once, in-process, and discarded. It never enters an agent and is never
  reused. Seconds, not hours. The client's skew tolerance (phase 5) is what
  makes a short window survivable, so pick these two numbers together.
- **`Extensions` should default to empty.** `permit-pty` and friends are
  meaningless for a certificate that authenticates a `sudo` call and is then
  thrown away. Granting nothing is both correct and the safest default.
- **`RequireGroup` is genuinely useful here** in a way it is not for user
  certificates: "who may become root on this host" is a narrower question
  than "who may log in", and an operator may reasonably want to gate it.
- **`KeyIDTemplate` must not fall back to user's.** Not because the fields
  differ, they do not, but because a `sudo` and a login by the same person
  would otherwise be indistinguishable in an `sshd` or `sudo` audit log.
  Give PAM an explicit default that identifies the type, on the order of
  `pam:{{.Username}}`.

Update `server/config/_defaults.yaml`, `ssoossh.default.yaml`, and
`docs/ssoosshd.yaml.default` together. The end-to-end plan records that a
config sample disagreeing with the code cost real debugging time.

**Implemented**, with one correction to the file list above: the root
`ssoossh.default.yaml` is the *client's* connection config (server URL,
CA pin) — unrelated to `cert_options`, and nothing in the repo reads it
(verified with a repo-wide grep). It is not touched. The two files that
actually carry `cert_options` — `server/config/_defaults.yaml` and
`docs/ssoosshd.yaml.default` — were updated together.

### 2. `Approve` does not handle the type

`server/service/certrequest.go`, in `Approve`'s switch: PAM falls through to
"issuing %s certificates is not supported yet". Add the case.

It follows the user branch, not the service one. A PAM request resolves
through the sign queue to a certificate, the same as a login. The service
branch, which resolves to `enrolled` with a token and produces no
certificate, is the wrong model entirely and nothing in this release uses it.

Policy resolution inherits from the shared path and needs no PAM-specific
handling:

- Extensions narrowed to the intersection of requested and configured.
- `ForceCommand` and `SourceAddresses` dropped unconditionally, as today.
- `NoTouchRequired` granted only for service. Not PAM.
- `RequireGroup` enforced — and mandatory for PAM specifically, see "Open
  question" below.

**Implemented** as `case model.CertificateTypeUser, model.CertificateTypePAM`
sharing `approveForSigning`.

### 3. Principal resolution

`resolvePrincipals` (`server/service/certrequest.go`) returns
`[identity.Username]` for every non-host type. For PAM this is nearly right
and needs stating precisely rather than inheriting by accident.

**The principal must be the local account the module is authenticating**, not
whatever the OIDC identity is called. Those are the same string in the common
case and are not guaranteed to be. `sudo` as `root` from a local account
named `mnestor`, with an OIDC `preferred_username` of `mike.nestor`, has
three candidate values and only one correct answer.

The module sends the authenticating local username; the server issues for it.
The security question, which local users an identity may become, is settled
by `RequireGroup` and by the host's own `sudoers`, not by the principal.
Phase 5's check 3 verifies the returned certificate names the user the module
is authenticating, which is what closes the loop.

**Implemented.** `POST /api/certs/pam` takes a `username` field
(`apitypes.PAMRequestBody`) alongside the public key, distinct from the
approving identity. It is persisted on the request row
(`model.CertificateRequest.Username`, set only for `CertificateTypePAM`) so
`Approve` — which runs against the stored row, not the create-time
params — can read it back regardless of how much later approval happens.
`resolvePrincipals` branches on type: host uses the hostname, PAM uses this
stored username, every other type uses the approver's identity. The key ID
(an audit-log label, not a security boundary) stays keyed on the *approver's*
identity through the `pam:{{.Username}}` default template — that is what
makes a sudo and a login by the same person distinguishable in an audit log,
per the config section above. Verified end-to-end by
`TestPipeline_EndToEnd_PAM` and `TestCertRequestService_Approve_ShouldQueuePAMRequestWithLocalUsernameAsPrincipal`
(`server/service/`), both of which set the local username and the OIDC
username to different values.

### 4. `certTypeFor` rejects everything but user

`server/signer/sign.go:71`:

```go
func certTypeFor(t model.CertificateType) (uint32, error) {
	if t == model.CertificateTypeUser {
		return ssh.UserCert, nil
	}
	return 0, newSignError(certmsg.ErrCodeUnsupportedType, "certificate type %q is not supported yet", t)
}
```

PAM maps to `ssh.UserCert`. It authenticates a person to a service, which is
what a user certificate is; the difference is entirely in lifetime, options,
and who validates it.

`server/signer/sign_test.go` pins PAM as rejected today. Update that test
rather than deleting it: the case that host and service are still rejected is
worth keeping, and it is the test that will catch a future type being enabled
by accident.

The signer stays database-free. Nothing here needs a lookup, which is why
this type is cheap.

**Implemented.** `sign_test.go`'s rejection table now covers only host and
service; `TestSign_ShouldIssueUserCertForPAM` covers the new mapping.

## Work

### 5. The endpoint

`POST /api/certs/pam`, modelled on `createUserRequestHandler`
(`server/controller/certrequests.go:99-116`).

Unauthenticated, like the other creation endpoints: the request ID is the
capability and authorization happens at approval. The module has no session
and cannot have one, which is the whole reason the browser approval flow
exists.

The response carries the approval URL and `events_url`, exactly as the user
path does, because phase 5's module consumes them the same way.

**Implemented** as `createPAMRequestHandler` (`server/controller/certrequests.go`),
registered at `POST /certs/pam`, taking `apitypes.PAMRequestBody{PublicKey,
Username, RequestedOptions}`. `internal/api` (the Go client used by `ssh
login`/`host sign`/`service enroll`) is deliberately untouched — phase 5's
client module is what will call this endpoint, per the goal above ("no client
module involved yet").

### 6. Regenerate the wire artifacts

Not follow-up tidying. A new endpoint means `make openapi`, `make types`, and
`go test ./server/webtypes/ -update`, all in the same change. CI runs
`openapi-check` and `types-check` and will fail the PR otherwise. See
[wire-types.md](wire-types.md).

**Implemented.** `make types` regenerated `apitypes.ts` (new `PAMRequestBody`);
`make openapi` regenerated `docs/openapi.yaml` (new `/api/certs/pam` path).
`webtypes` itself didn't change — `RequestDetailResponse.Type`/`.Principals`
already carry everything a PAM row needs — so `go test ./server/webtypes/
-update` produced no diff; `openapi-check`/`types-check` both pass clean.

### 7. The approval page

Confirm a PAM request renders correctly. It should need no new UI: same
requester binding, same granted-versus-requested rendering, same approve and
deny.

What to check rather than assume is the **wording**. A page that says "a
client is requesting an SSH certificate" is misleading when the actual
question is "do you want to become root on `web01`". The type is in the
request; use it. This is a copy change, not a feature.

If tier 2 of the end-to-end suite matches on that copy, this is why phase 2
adds `data-testid` attributes.

**Implemented.** No new UI, as expected — `ApprovalView.svelte` already
showed `detail.type` and rendered granted-vs-requested generically. Only the
card heading/description changed: PAM gets "Approve a PAM authentication" /
wording that names a local `sudo`-style operation instead of "an SSH
certificate", switched on `detail.type === 'pam'`. Covered by two new cases
in `ApprovalView.test.ts`. Left the "become root on `web01`" specificity out
of the copy itself: PAM requests don't carry a hostname (unlike host
requests — see `model.CertificateRequest.Hostname`'s doc comment), so the
wording stays generic to "a local operation" rather than naming a machine it
doesn't know.

### 8. Migrations

None for the type itself: `certificate_requests` stores it as a string and
the option set as JSON. Verified — no check constraint or enum type
enumerates certificate types anywhere in `server/resources`.

One column was needed for a different reason: item 3's local username has to
survive from request creation to (possibly much later) approval, and nothing
existing carries it — `Hostname` is documented as host-only. Added a
`username` column to `certificate_requests` (`model.CertificateRequest.Username`,
set only for `CertificateTypePAM`) in both dialects' `20260101000000_init`
migration directly, matching that file's own header comment ("a column added
here must also be added to postgres/... and model/") rather than creating a
second migration file — there is still only ever the one, and nothing has
shipped yet.

## Exit criteria

- `POST /api/certs/pam` creates a request that appears on the approval page,
  is approved by a human, reaches the signer, and is delivered over SSE.
- The issued certificate is an `ssh.UserCert` with the configured short
  lifetime, no extensions, and a key ID identifying it as PAM.
- Host and service certificates are still rejected.

## Verification

- An end-to-end test mirroring `server/service/pipeline_test.go` for the PAM
  type: create, approve, sign queue, signer, listener, SSE delivery.
- A test that the issued certificate's principal is the local username the
  request named, not the approver's OIDC username, with those two set to
  different values in the fixture. This is the assertion that would catch the
  wrong reading of item 3.
- A test that `cert_options.pam.extensions` being empty yields a certificate
  with no extensions, and that a requested extension is dropped rather than
  granted.
- A test that an unset `cert_options.pam.key_id_template` does not inherit
  the user template.
- `server/signer/sign_test.go` still pins host and service as rejected.
- `server/signer/zerodb_test.go` still green.
- The phase 2 end-to-end suite still green. This phase edits the user path's
  own code.

## Open question — decided

Whether `require_group` for PAM should default to empty (anyone who can log
in can `sudo`, subject to `sudoers`) or be mandatory (no group configured
means no PAM certificates are ever issued).

Failing closed argues for the second, and it matches how
`admin.require_group` is specified to behave in
[admin-authorization-plan.md](admin-authorization-plan.md), where empty
denies. Against it: the host's `sudoers` is already an authorization
decision, and requiring two lists that must agree is the pattern phase 8 of
the delivery plan explicitly rejected for service accounts.

**Decided: mandatory.** Empty `cert_options.pam.require_group` means no PAM
certificates are ever issued — `CertRequestService.Approve` rejects the
request outright rather than treating an unconfigured group as "anyone."
This is the fail-closed match to `admin.require_group`'s precedent, and the
counter-argument is weaker here than it was for phase 8's service accounts:
`sudoers` is host-local and narrow, not a second broad-access list that has
to be kept in sync with a server-side one — `require_group` is answering "may
this identity request PAM certificates on any host at all," a coarser
question than what `sudoers` decides per host. Implemented in
`CertOptionsPAM.RequireGroup`'s doc comment, `server/config/_defaults.yaml`,
`docs/ssoosshd.yaml.default`, and enforced in `CertRequestService.Approve`
(see `TestCertRequestService_Approve_ShouldDenyPAMWhenRequireGroupUnconfigured`).
Decided before the endpoint shipped, per the instruction above — changing it
later is a breaking config change that would silently grant access.
