# PAM certificates should carry the approver, not the requester's claim

**Status: implemented in the working tree, not yet committed.** Anchors
verified against `a009511` (2026-09-04); the code they point at has since
changed, which is the point. Kept as the record of what was wrong and why
the fix is shaped the way it is. What landed differs from the draft below in
two places, both noted inline: the `pamUsername` parameter was removed from
the `principals` signature entirely rather than ignored, and the approval
page gained a `target_account` field so the approver can still see which
local account is being attempted.

This is a change to shipped behaviour, not a new feature. It is written
separately from
[console-login-pam.md](console-login-pam.md) because it stands on its own
and should land first; that design depends on it and is simplified by it.

## The defect

A `pam` certificate's `ValidPrincipals` is the local account name the PAM
module sent, verbatim. It is not derived from the approver's identity in any
way.

```go
// server/service/certtypepolicy.go:140-149
model.CertificateTypePAM: {
    // ...
    principals: func(pamUsername string, _ *Identity, _ []string) []string {
        return []string{pamUsername}
    },
},
```

Both call sites pass `req.Username` as `pamUsername`
(`server/service/certrequest.go:525` for the approval-page preview,
`:1192` for the value that becomes `certmsg.SigningJob.Principals`), and
`req.Username` arrives in the body of `POST /api/certs/pam`, from an
unauthenticated caller.

The behaviour is deliberate and pinned by a test. `TestPipeline_EndToEnd_PAM`
(`server/service/pipeline_test.go:203`) approves as `mike.nestor` a request
naming `mnestor`, and asserts:

```go
if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != "mnestor" {
    t.Errorf(`got ValidPrincipals %v, want ["mnestor"] (the local account, not the approver's OIDC username)`, cert.ValidPrincipals)
}
```

### The part that makes it a defect rather than a choice

The certificate records the approver in the field nothing enforces, and the
requester's own claim in the field that decides access:

| Certificate field | Value today | Derived from |
| --- | --- | --- |
| Key ID | `pam:mike.nestor` (`server/service/keyid.go:67`, filled from `identity.Username` at `:40`) | the **approver's** OIDC login |
| `ValidPrincipals` | `["mnestor"]` | the **requester's** unauthenticated request body |

Key IDs are for audit logs. Principals are what `sshd`, `sudo` policy, and
`pam_ssoossh`'s own check 3 act on. So the authenticated half of the
transaction lands in the advisory field and the unauthenticated half lands
in the load-bearing one.

This contradicts the project's own stated model
(`docs/internals/design-brief.md:83`):

> The certificate asserts identity; the host decides which local accounts
> that maps to. This is what keeps "userX may become root here" from being
> a statement about every host trusting the CA.

Under the current PAM path the certificate asserts no identity at all. It
echoes back the account the caller asked for. Every `user` certificate obeys
the model (`userPrincipals` returns the approver's selection, and
`checkUserPrincipalLinkage`, `server/service/certrequest.go:1035`, refuses a
selection the approver does not hold). `pam` is the one type that does not.

### What it permits

1. An attacker reaches `POST /api/certs/pam` and creates a request with
   `username: root`. Nothing authenticates that call; it is open by design,
   because approval is where authorization happens.
2. They get a human who satisfies `cert_options.pam.require` to approve it.
   `bindRequester`'s doc comment (`server/service/certrequest.go:721-725`)
   already records that nothing defends this consent-phishing step.
3. The signer mints a certificate with `ValidPrincipals: ["root"]` and key
   ID `pam:<the approver>`.
4. On any host trusting the CA, `pam_ssoossh` authenticating local account
   `root` runs check 3 (`pam_ssoossh/checks.go:117`), finds `root` among the
   principals, and succeeds.

The approver's identity constrained nothing but the audit string. The only
thing standing between step 2 and a root shell is whether the deployment
happens to have `principals-map` configured on that host, which is optional
and off by default.

## The fix

`pam` principals come from the approver, like every other type:

```go
principals: func(_ string, identity *Identity, _ []string) []string {
    out := []string{identity.Username}
    return append(out, identity.OtherAccounts...)
},
```

`req.Username` stays on the row and stays in the audit record and on the
approval page. It stops feeding the certificate. It becomes what
`LocalUsername` already is for user certificates: context the client
reported, useful to a human, never evidence.

After the change the flow reads the way the design brief says it should.
The certificate says "the identity provider vouched for `mike.nestor`, who
also holds `mnestor`". The host decides whether that identity may act as the
local account being authenticated, using `checkPrincipal` and its
`principals-map`. The attack above ends at step 4: a certificate naming
`mike.nestor` and `mnestor` does not authorize `root` unless the host's own
root-owned map says that identity may become root.

### Consequences that follow for free

- **`linkage` stays nil for `pam`, correctly this time.** The principals are
  by construction accounts the approver holds, so there is nothing left to
  cross-check. The linkage machinery exists for `user`, where the approver
  *selects* principals and the selection has to be validated.
- **The key ID and the principals stop disagreeing.** Both derive from the
  approver. `pam:{{.Username}}` keeps working unchanged.
- **The console design gets simpler.** Its whole
  "who may approve a login for which account" question collapses into the
  host-side principal map, which is where it belonged.

## The breaking change, and the migration

This is the reason the fix is not a one-line commit.

Today, a deployment where the OIDC username happens to equal the local
account name works with no `principals-map` at all: the cert says `alice`,
the module is authenticating `alice`, check 3 passes on the exact match.

After the change, that deployment still works **only if** the approver's
`identity.Username` or one of their `OtherAccounts` equals the local account
name. Wherever they differ (`mike.nestor` approving for `mnestor`, an email
address as the OIDC username, a service-shaped local account), **`sudo`
stops working on every host until `principals-map` is configured**.

`checkPrincipal` already supports exactly this and needs no change
(`pam_ssoossh/checks.go:117`):

```yaml
# /etc/ssoossh/principals.yaml
mnestor:
  - mike.nestor
  - mnestor
```

Two sharp edges to document with it:

- **A configured map is all-or-nothing per account.** `PrincipalsMap.Allowed`
  (`internal/principalsmap/principalsmap.go:294-305`) returns false for an
  account with no entry. Turning the map on denies every account not listed
  in it.
- **A map that fails to load falls back to exact match**, logged at warning
  (`checkPrincipal`'s doc comment). Before this change that fallback
  degraded to today's behaviour. After it, the fallback is a stricter
  check than the map was, so a typo'd path turns into denied logins rather
  than silently looser ones. That is the right direction, but the warning
  needs to be in the runbook.

Because the failure mode is "sudo stops working fleet-wide after an
upgrade", the change needs, at minimum:

1. A release note that names it as breaking, at the top.
2. A pre-flight the operator can run before upgrading: for each user, does
   `identity.Username` or an `OtherAccounts` entry match the local accounts
   they `sudo` as? The data is already in the users table.
3. `LDAP enrichment`, which is the mechanism that fills `OtherAccounts`
   (`docs/operations/ldap.md`), is parsed but not yet consumed. Until it is,
   `OtherAccounts` is empty for most deployments and `principals-map` is
   the only migration path. Worth confirming before picking a release.

## Sub-decision: every held account, or one?

The fix above puts `identity.Username` plus every entry of
`identity.OtherAccounts` into the certificate. The alternative is to emit
only the one that matters.

**Emit all of them** (recommended). The host's `checkPrincipal` picks the
one it needs, no selection UI is required, and the certificate lives 30
seconds, is validated once in-process, and is never written to disk
(`cert_options.pam.valid_duration`, and the module discards it). The
information disclosure is a list of account names shown to a module that
already knows one of them.

**Emit only the account being authenticated**, by intersecting the held
accounts with `req.Username` (directly or through a server-side map), is
tighter but needs the server to know the host's mapping, which it
deliberately does not: principal mapping is local to each host and nothing
syncs it down (`docs/internals/design-brief.md`, "Principal mapping"). It
would also fail closed in a way that is hard to diagnose, because the
server would have to guess what the host's map says.

**[judgement]** Emitting all held accounts is the smaller change and does
not require the server to model host-local state.

## Sequencing

1. **The policy change and its tests.** One function in
   `certtypepolicy.go`, and `TestPipeline_EndToEnd_PAM` inverted: the same
   deliberately-different usernames, now asserting the certificate names the
   approver and *not* the local account.
2. **The model comment on `CertificateRequest.Username`**
   (`server/model/certificate_request.go:34`) currently says the opposite of
   what will be true and must move with the code.
3. **The approval page** shows the local account as reported context rather
   than as the principal being granted, alongside the principals that
   actually will be.
4. **Docs**: `docs/pam.d-sudo.example`'s `principals-map` entry stops being
   an optional convenience and becomes the normal case;
   `docs/operations/deployment.md`'s PAM section gains the migration;
   `docs/guide/features.md` gains the corrected description.
5. **Release note**, breaking, with the pre-flight.

## Testing

- `TestPipeline_EndToEnd_PAM` inverted, and kept as the end-to-end
  assertion: approver `mike.nestor` (holding `mnestor`), request naming
  `mnestor`, certificate must carry `["mike.nestor", "mnestor"]` and the
  key ID must still be `pam:mike.nestor`.
- A case with `OtherAccounts` empty: the certificate carries exactly
  `[identity.Username]`.
- The escalation case, as a regression test: a request naming `root`,
  approved by an identity that does not hold `root`, produces a certificate
  that does not contain `root`.
- Module-side, against the four checks: a certificate carrying the
  approver's accounts and *not* the local account fails check 3 with no map,
  and passes with a map that lists it. Both paths already exist in
  `checks_test.go`; this adds the cases that matter after the change.
- `Detail`'s preview (`server/service/certrequest.go:525`) must show the
  same principals the approval will grant, so the page cannot promise one
  thing and issue another.

## Provenance

- The principals function and its two call sites: `grep -n "policy.principals"
  server/service/certrequest.go` gives `:525` and `:1192`; the PAM entry is
  `server/service/certtypepolicy.go:140-149`.
- The pinned behaviour: `server/service/pipeline_test.go:203-213` (the
  comment stating the intent) and `:324-326` (the assertion).
- The key ID split: `server/service/keyid.go:67` for the template,
  `:40` for `Username` being `identity.Username`.
- The stated model: `docs/internals/design-brief.md:83`.
- The consent-phishing note: `server/service/certrequest.go:721-725`. The
  `docs/security-review-2026-08-11.md` it cites is not in the tree.
- Map semantics: `internal/principalsmap/principalsmap.go:294-305`.
  Note that `internal/principalsmap/` has uncommitted working-tree changes
  from another session; the anchors above are from `git show HEAD:`.

## Open questions

1. **Does `su` need different handling from `sudo`?** Both go through the
   same module and the same certificate type. `su alice` authenticates as
   `alice`, so after this change it needs `alice` reachable from the
   approver's held accounts, which for `su` to another human's account is
   exactly the mapping decision that should be explicit.
2. **Should the server refuse a `pam` request naming an account the
   approver cannot possibly hold**, at approval, as a fail-fast? It cannot
   know the host's map, so it would be a guess. Probably not, but the
   approval page can warn.
3. **Does this want its own certificate-type behaviour flag** so a
   deployment can stage the rollout host by host, or is a clean break with
   a release note the right call? A flag that re-enables the old behaviour
   is a flag that re-enables the escalation path.
