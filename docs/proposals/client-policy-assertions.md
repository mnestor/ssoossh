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

## Why this rather than verifying on receipt

The narrower fix — have the client check the returned certificate's
extensions against `ForbiddenCertificateExtensions` and refuse to load it —
was considered and rejected as the primary answer. It closes one hole for
one setting. This generalises: any locked client setting becomes legible to
the server, and the approval UI can say *why* a toggle is unavailable rather
than offering something the requesting machine will refuse to use.

Verifying on receipt is still worth having as a defence-in-depth layer
underneath this, and is the only layer that also covers a server that is
compromised, buggy, or simply not this deployment's — the certificate is
signed by the CA the client pins, and nothing inside it says an extension
was never requested. It is not a substitute for the server knowing.

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
issuance (the server refuses to sign it whatever the approver ticks). The
second is stronger and is what an administrator reading
[client enforcement](https://mnestor.github.io/ssoossh/hosts/client-enforcement/)
would assume, but it means a stale or spoofed assertion can deny a
legitimate certificate.

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
