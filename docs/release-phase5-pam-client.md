# Phase 5: PAM module and the four checks, client side

**Status: planned.** Part of [release-plan.md](release-plan.md).

## Goal

`sudo` on a configured host prints an approval URL, a human approves it in a
browser, and `sudo` succeeds. Nothing is retained on disk or in an agent
afterwards.

This is the security-critical phase of the release. The module is the only
component in the product that **verifies** a certificate rather than
requesting one, and it does so as `root`, inside the authentication stack of
`sudo`. A mistake here is a privilege escalation, not a failed login.

## What exists and what does not

`auth.go` was rewritten since the old plan was written and is now in a good
state to build on. It fails closed deliberately:

```go
var errNotImplemented = errors.New("pam_ssoossh is not implemented yet")
...
return PamAuthInfoUnavail, errNotImplemented
```

It validates configuration before spending a keygen, generates the ephemeral
keypair, marshals the public half, logs it at debug, and stops. The old
pre-restructure body is preserved below it as a comment, annotated with a
written analysis of what it got right and what it missed. **Read that comment
before writing anything**; it is the specification for the checks, and it is
explicit that it is not resumable code.

Solid and reusable: argument parsing including quoted values (`args.go`), the
PAM return-code constants (`return_values.go`), the syslog and stderr logger
(`logger.go`), and the cgo glue for `pam_sm_authenticate` (`pam.go`).

Missing: the certificate request, all four checks, the PAM conversation, and
every test. The package has none.

## The four checks

From `pam_ssoossh/CLAUDE.md` and [ssoossh-context.md](ssoossh-context.md).
All four are required, and the order matters: reject on the cheapest
disqualifying check first, and never let a later check's success paper over
an earlier one's absence.

| # | Check | State today | Notes |
| --- | --- | --- | --- |
| 1 | Signed by the expected CA | Absent | `keypair.SSHKeypair.SignedBy(ca)` already exists and is unused. `trusted-ca-file` is a parsed module arg that nothing reads |
| 2 | **Certificate public key matches the key just sent** | Absent, and never sketched | The omission that matters most |
| 3 | Principals identify the authenticating user | Sketched in the reference comment | Reasonable as written |
| 4 | Inside the validity window, with skew tolerance | Half sketched | The reference checks `ValidBefore` only |

### Check 1 is a signature verification, not a string comparison

The reference implementation compared the marshaled `SignatureKey` against
lines of the trusted-CA file. That asserts "this certificate names a CA I
trust" and relies on the server having actually signed with it. It is not the
same statement.

Worse, as the comment notes, `cas` was never populated in that code, so the
check would always have been false. Use `SignedBy`, which verifies properly.

The trusted-CA file may hold several keys. Parse it as `authorized_keys`
format, one CA per line, and accept a certificate signed by any of them, so
CA rotation does not require simultaneous restarts everywhere.

### Check 2 is the one that would otherwise be missing

**Compare the certificate's public key against the public half of the keypair
generated a few lines earlier.** Without it, checks 1, 3, and 4 passing
together accept *any* CA-signed certificate carrying the right principals,
including one issued to somebody else's keypair.

The concrete attack: an attacker who legitimately holds a valid ssoossh user
certificate for the target user, from their own login session, presents it to
the module and becomes that user via `sudo`. The certificate is genuine, the
CA is right, the principals are right, and the window is valid. Only check 2
notices that it was issued to a different key.

This is why the ephemeral per-attempt keypair exists and why no nonce is
needed: the freshness guarantee is that the key was generated microseconds
ago and never left the process. That guarantee is worth nothing unless the
certificate is bound to it.

Write this check first, and write its failing test first.

### Check 3 is about the local user

The principal must identify the account being authenticated, which is the
local username PAM supplied via `GetUser`, not an OIDC identity the module
never sees. This is the client half of the rule phase 4 sets on the server;
the two have to agree, and the end-to-end test is what proves they do.

### Check 4 needs both bounds and a tolerance

The reference checks `ValidBefore` and ignores `ValidAfter` entirely, so a
not-yet-valid certificate passes.

Check both, and add a **skew-tolerance module setting**. No config field
exists today. Clock skew is the real operational constraint: the certificate
is issued by the server and validated on the host, seconds apart, and the two
machines do not necessarily agree on the time. Phase 4's very short
`valid_duration` makes this sharper, which is why the two numbers get chosen
together.

Apply the tolerance **symmetrically**, and **log the observed skew on
failure**. An operator debugging a `sudo` that fails intermittently at 3am
needs the log line to say "certificate not yet valid, 4.2s of skew, tolerance
2s", not "authentication failed".

## Work

### 1. Rewrite `Authenticate`

Against the current `internal/api` surface. `api.NewClient`, `PostPubKey`,
`GetCertificate`, `kp.ParseCertificate`, and `kp.GetCertficiate` from the
reference comment were all removed when `internal/api` and
`internal/crypto/ssh/keypair` were rewritten.

The shape: create the request against phase 4's `POST /api/certs/pam`, print
the approval URL through the PAM conversation, block on SSE for the
certificate, run the four checks, return.

Reuse `internal/api`'s create-then-await split rather than adding a PAM-only
client path. The module and the `ssh login` client are making the same
request against the same endpoint shape.

### 2. PAM conversation support

Needed to display the approval URL at the terminal. The cgo glue handles
`pam_sm_authenticate` and retrieves the username; the conversation callback
for `PAM_TEXT_INFO` is not wired.

**Do not print to stdout or stderr.** Under PAM those are the conversation
channel with `sudo`, and writing to them directly is what phase 1 removes
from `pam_ssoossh.go:34`. The URL goes through the conversation; everything
else goes through the logger.

### 3. Fix the nil-error success logging

`pam_ssoossh.go:52-63`:

```go
success, err := Authenticate(&w, username, cfg)
if err != nil {
	w.Errorf(err.Error())
	return C.int(success)
}
w.Infof("successful authentication: %s", username)
return C.int(success)
```

A non-success code returned with a nil error is logged as a successful
authentication. The old plan called this unreachable; it is not. It is
reachable the moment `Authenticate` gains a path that returns a failure code
without an error, which is exactly what this phase writes.

Two fixes, and do both: every failure path returns a non-nil error, **and**
the caller decides what to log from the return code rather than from the
error being nil. The second is what makes it structurally safe rather than
safe by convention.

### 4. Timeouts and cancellation

A human has to open a browser and approve. That takes as long as it takes,
and the module is holding up a `sudo` prompt the whole time.

Decide and document:

- **The wait timeout**, and what the user sees when it expires. A `sudo` that
  hangs indefinitely on a laptop with no browser is a worse failure than one
  that gives up after 60 seconds with a clear message.
- **Interrupt handling.** Ctrl-C at the `sudo` prompt should abandon the
  request, not leave the module blocked on SSE.
- **The server unreachable case.** Fail fast and let the PAM stack fall
  through to whatever comes next in `/etc/pam.d`, rather than making an
  outage of the ssoossh server an outage of `sudo` on every host.

That last point deserves care and is not purely a module decision: the
`sufficient` versus `required` control flag in the PAM stack determines
whether a fallback exists at all. Phase 7 documents the recommended stack;
this phase decides what behavior that recommendation depends on.

### 5. First tests

The package has none. Cover:

- Argument parsing, which is already non-trivial and handles quoted values.
- Each of the four checks in isolation, each with a failing case.
- The full authenticate path against a fake server.

`make test-pam` stops being a stub in phase 3, so these run in CI from the
moment they are written.

### 6. Extend the end-to-end suite

Add a PAM tier to phase 2's harness: request through the module's code path,
approve in the browser, certificate validated, success returned.

The harness IdP was written to be reusable for this. Whether the tier drives
a real `sudo` or calls `Authenticate` directly is a judgment call: a real
`sudo` in CI needs a configured `/etc/pam.d` entry and root, which the runner
has, but it is the most environment-dependent thing in the suite. Calling
`Authenticate` directly covers all four checks and the wire; only the cgo
glue and the conversation go untested.

Recommendation: **direct `Authenticate` in the PR gate, real `sudo` in the
phase 7 rehearsal.** The cgo glue is the part least likely to change and the
most expensive to test.

## Exit criteria

`sudo` on a configured host triggers OIDC approval in a browser and succeeds.
Denying in the browser denies the `sudo`. Nothing is retained afterwards.

## Verification

- Unit tests for all four checks, each with a failing case.
- **A test that a certificate whose public key differs from the one sent is
  rejected.** This is check 2, and it is the single most important test in
  the release. Construct it from a genuinely CA-signed certificate for the
  right principal and the wrong key, so it fails for the right reason.
- A test that `ValidAfter` in the future is rejected, that tolerance is
  applied symmetrically, and that observed skew appears in the log.
- A test that a certificate signed by an untrusted CA is rejected, including
  the case where the trusted-CA file holds several keys and the signer is not
  among them.
- A test that a certificate whose principals omit the authenticating user is
  rejected.
- A test that every failure path returns a non-nil error, and that the
  success log line does not appear for any non-success return code.
- An end-to-end test mirroring `server/service/pipeline_test.go` for the PAM
  type, from the module's side.
- Manual: `sudo` on a host with the module in the `auth` stack, plus a
  negative test that denying in the browser denies the `sudo`, plus the
  server-unreachable case.

## Constraints

- `pam_ssoossh/` must not import `server/` or `client/`, only `internal/`.
- The module runs in the `auth` group, `sudo` and `su` only.
- `pam_ssh_agent_auth` is rejected as an approach because it requires agent
  forwarding.
- `AuthorizedPrincipalsCommand` is irrelevant here. It is an `sshd` directive,
  not something this module implements or calls.
- The console-code flow, where the module displays a code typed into the web
  UI, stays deferred. See the "Future" section of
  [ssoossh-context.md](ssoossh-context.md).
