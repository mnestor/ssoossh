# Client policy assertions

**Status:** idea. Not scheduled. Blocks the user-certificate extensions
picker described below, which was specified and then deliberately paused on
2026-09-10 in favour of doing this first.

## What

The client tells the server which of its settings are locked by
administrative policy — MDM managed preferences on macOS, Group Policy on
Windows, the `enforce` file on Linux — so the server can bound what an
approver is allowed to grant by the requester's own administrative policy,
instead of being blind to it.

Today the server sees only the already-narrowed request. It cannot tell
`--no-port-forwarding` (a user's choice) from
`forbidden_certificate_extensions` (an administrator's rule), because both
arrive as the same thing: an extension that simply is not in
`RequestedOptions.Extensions`.

## What this is not: a boundary against the user

Decided 2026-09-10, and it shapes everything below.

Assertions must be **narrowing only** (see the trust question). But a client
that wants the extension can then simply not assert, and get exactly what it
would have got anyway. Nor does anything stop a user calling the API
directly and skipping this client altogether.

So this cannot bind a determined user, and neither could the narrower fix it
replaced — checking the returned certificate against
`ForbiddenCertificateExtensions` before loading it. That check was considered
and **dropped**: it is bypassed by the same person in the same way, and
"refuse to load" is a control the user owns.

What is left is worth having anyway, but it is a different thing and the
documentation has to say so: this makes an honest client's administrative
policy **legible to the server**, so an approver is not offered a toggle that
would grant something the requesting machine will refuse to honour, and so
the UI can say why. That is correctness, not enforcement.

The consequence for the operator docs is not optional.
`forbidden_certificate_extensions` currently reads as a guarantee. It is not
one, and was not one before this proposal existed — it shapes a request made
by a cooperating client. The page needs to say that plainly whether or not
this ships.

## The trust question, which is the hard part

A client asserting "this setting is locked for me" is client-supplied data.
The server must treat it as **narrowing only, never as authorization**:

- An assertion may remove options from what an approver can grant.
- An assertion may never add one, raise a ceiling, or widen a grant.

Get that backwards and a lying client gains privilege by claiming a policy
it does not have. The safe shape is that the effective set stays
`requested & ceiling & granted - removed` and the assertion enters as one
more subtraction, so a client that lies can only ever harm itself.

Worth deciding explicitly: whether an assertion is advisory to the UI only
(the approver sees a disabled toggle and an explanation) or binding on
issuance (the server refuses to sign it whatever the approver ticks). Given
that this is not a boundary against the user, "binding" only binds honest
clients — it buys little over advisory and costs a denial path when an
assertion is stale.

## Open questions, before implementing

1. **Advisory or binding**, per above. Everything else is mechanical once
   this is settled.
2. **Scope.** Only the forbidden-extension list, or a general locked-settings
   map? Most client settings (`key_filename`, `try_open_browser`) mean
   nothing server-side, so a general mechanism still needs a decision about
   which keys are worth sending.
3. **The client cannot currently tell.** `mergePlatformPolicy` merges the
   policy map into viper and then discards which keys came from it: only
   `policySetsFIPS` and `policyForbiddenExtensions` survive as
   policy-sourced, and every other locked value becomes indistinguishable
   from a config-file one. Retaining that key set is a small prerequisite
   that does not exist today.
4. **Service certificates may be out of scope.** The enrolling machine is not
   necessarily the machine that retrieves or uses the certificate — the code
   is portable and `service retrieve` only posts it — so an assertion made at
   enrollment describes a machine that may be irrelevant. Either the service
   path is excluded, or "whose policy applies" needs an answer.
5. **The approver needs a reason, not a shorter list.** The whole UX payoff is
   a disabled toggle that says why, which means a wire field carrying the
   reason rather than only the narrowed set.
6. **Staleness.** A service enrollment fixes its extensions at approval; the
   requesting machine's policy can change afterwards and nothing revisits
   it.

## What it unblocks: the user-certificate extensions picker

Specified, then paused pending this. Recorded here so it is not re-derived:

- **Selectable:** `cert_options.user.extensions` — the user type's ceiling,
  exactly as the service-certificate picker is bounded by
  `cert_options.service.extensions`.
- **Preselected:** the effective set the pipeline computes today —
  `requested & ceiling & granted - removed`. Read "merged" as intersection,
  not union: it is the set the request would produce if nobody touched it.
  An extension the client opted out of is not preselected (the opt-out is
  already reflected in `requested`); one the tier does not grant is not
  preselected (`o.granted`).
- **Adjustable:** the approver may then change the selection up to the
  ceiling.
- Same principal-style toggles as the service picker, same inert-by-default
  property: an approver who touches nothing approves exactly what the
  request would have produced.

It is paused because it is the change that makes the gap reachable through a
well-behaved server. Letting an approver add extensions to a *user*
certificate, on the interactive login path, is precisely where
`forbidden_certificate_extensions` is documented to apply and currently
does — as a pre-request subtraction in
`effectiveExtensions` (`client/cmd/ssh_login.go`). An approver ticking a box
would put back what an MDM took away, and neither side would know.

## The service-certificate picker ships without this, deliberately

`forbidden_certificate_extensions` has **never** applied to service
certificates, and still does not. Confirmed against the code rather than
assumed: the key is read in exactly two places outside config plumbing —
the pre-request subtraction in `client/cmd/ssh_login.go` and a display line
in `client/cmd/debug.go` — and `client/cmd/service_enroll.go` sends
`api.RequestedOptions{}` with no policy subtraction on the way out.

So the service-certificate extensions picker is not a regression against a
guarantee that existed. It does newly create a route by which an approver's
selection puts an extension onto a service certificate that then lands on a
machine whose administrative policy forbids it. That gap is **known and
accepted**, on the understanding that it is closed by this proposal rather
than left indefinitely.

This asymmetry needs saying plainly in the operator documentation, because
nothing about the key's name suggests it covers one certificate type and not
the other, and an operator will reasonably assume it covers everything.
