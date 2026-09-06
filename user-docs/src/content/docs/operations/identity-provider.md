---
title: Identity provider
description: Registering the OIDC client, mapping claims to ssoossh fields, and fixing a rejected redirect URI.
eyebrow: Server operations
sidebar:
  order: 2
---

`ssoosshd` has no user database of its own. Everyone who approves a
certificate signs in through your OIDC provider first, and the provider stays
authoritative for everything that follows -- including who is an admin. This
page covers registering the client and mapping its claims.

## Which providers work

Any OIDC-compliant provider. `ssoosshd` uses standard discovery
(`/.well-known/openid-configuration`), the authorization-code flow with PKCE,
and reads its identity out of the ID token. There is no provider-specific
code.

[pocket-id](https://github.com/pocket-id/pocket-id) is the reference provider
the project's own configuration assumes, and the worked example below. Nothing
about the setup is specific to it.

## What the provider needs from you

| Item | Value |
| --- | --- |
| Redirect URI | `<http.public_url>/auth/callback` -- exactly one, no others |
| Grant | authorization code |
| Scopes | `openid`, plus whatever [`authentication.scopes`](/ssoossh/reference/config/authentication/#scopes) names (default `profile email`) |
| Claims | a username claim, and a groups claim if any policy gates on a group |

`openid` is always requested and does not need to be listed in `scopes`.

## What you need from the provider

```yaml
authentication:
  provider_url: "https://idp.example.com"   # base URL; discovery must resolve
  client_id: "..."
  client_secret: "..."
```

[`authentication.provider_url`](/ssoossh/reference/config/authentication/#provider_url)
is the issuer's base URL. A trailing `/.well-known/openid-configuration` is
stripped automatically, so pasting the discovery URL works too.

The redirect URI is not configured. It is derived from
[`http.public_url`](/ssoossh/reference/config/http/#public_url), because that
is the one place the browser-visible identity of the deployment is written
down. Get `public_url` right and the redirect URI follows.

## Worked example: pocket-id

1. Run pocket-id and complete its first-run admin setup.
2. In its admin UI, create an OIDC client for ssoossh. Set the callback URL to
   `<http.public_url>/auth/callback` -- for a deployment at
   `https://ssh.example.com`, that is
   `https://ssh.example.com/auth/callback`. Note the generated client ID and
   secret.
3. Create or import the people who will log in, and any groups the
   configuration gates on -- an `SSH Sudoers` group for
   [`cert_options.pam.require.group`](/ssoossh/reference/config/cert_options/pam/#requiregroup),
   say, or the admin groups from
   [Roles and containment](/ssoossh/operations/roles/).
4. Put `provider_url`, `client_id`, and `client_secret` into `ssoosshd.yaml`.
   Leave
   [`authentication.fields.username`](/ssoossh/reference/config/authentication/#fieldsusername)
   at its default and set
   [`authentication.fields.groups`](/ssoossh/reference/config/authentication/#fieldsgroups)
   if group-gated certificate types are in use.
5. Restart `ssoosshd`, then confirm from the server itself that
   `GET <provider_url>/.well-known/openid-configuration` resolves. That is the
   first thing a typo'd `provider_url` breaks.

## Mapping claims to fields

`authentication.fields` names which claim fills each ssoossh identity field.
The defaults suit a provider that follows the usual claim names.

| Key | Default | What it feeds |
| --- | --- | --- |
| [`fields.subject`](/ssoossh/reference/config/authentication/#fieldssubject) | `sub` | the unique account identifier every login is keyed by. See [Choosing the account identifier](#choosing-the-account-identifier) |
| [`fields.username`](/ssoossh/reference/config/authentication/#fieldsusername) | `preferred_username` | the primary principal, and `{{.Username}}` in key IDs. Required |
| [`fields.name`](/ssoossh/reference/config/authentication/#fieldsname) | `name` | the person's human-readable name, shown in the web UI and offered to email templates. Display only |
| [`fields.groups`](/ssoossh/reference/config/authentication/#fieldsgroups) | `groups` | `require` gates, lifetime tiers, and the admin roles. A JSON array of names |
| [`fields.other_accounts`](/ssoossh/reference/config/authentication/#fieldsother_accounts) | empty | alternate account names added to a certificate's principal list |
| [`fields.service_accounts`](/ssoossh/reference/config/authentication/#fieldsservice_accounts) | empty | which service accounts this identity may enroll and manage |
| [`fields.email`](/ssoossh/reference/config/authentication/#fieldsemail) | `email` | the address notifications go to. Absent is not an error |
| [`fields.extra`](/ssoossh/reference/config/authentication/#fieldsextra) | none | operator-named claims, for key ID templates and claim conditions |

Group membership is never written into an issued certificate. It feeds policy
decisions and role checks only.

`fields.extra` maps a template field name to a claim name:

```yaml
authentication:
  fields:
    extra:
      dept: "https://idp.example.com/department"
      loc: level_of_confidence
```

Each configured claim is captured at login and stored on the user's row.
Scalars become strings, JSON arrays keep their string elements as a list. A
claim absent from the ID token stores empty and renders as `MISSING`; login
never fails over one. Every claim a policy condition names must be declared
here, and that is checked at startup, so a typo stops the process instead of
quietly failing the condition on every evaluation. Details:
[Key ID templates](/ssoossh/operations/key-id-templates/) and
[Certificate lifetime policy](/ssoossh/operations/certificate-policy/).

:::note
A claim's value is only as fresh as the subject's last login. `Extra` is
written to the users row at login and read back at approval, so lowering
someone's score in the provider takes effect at their next authentication.
:::

Directory attributes can fill the same fields when OIDC does not carry them --
see [LDAP enrichment](/ssoossh/operations/ldap/).

## Choosing the account identifier

`authentication.fields.subject` names the claim ssoossh keys a person by. It
is the value stored on `users.subject`, and the only thing that ties someone
to their certificate history, their enrollments and their audit trail across
logins.

It defaults to `sub`, which is what OIDC guarantees to be stable per issuer
and is the right answer for most providers. Two cases where it is not:

- **Entra ID** issues a per-application `sub` and puts the tenant-stable
  identifier in `oid`. Registering a second application client for the same
  people would give them all new `sub` values, and so new accounts.
- **A provider fronting a directory** may carry the directory's own UUID in a
  private claim, which is worth keying on so that ssoossh and the directory
  agree on who someone is.

**Username and email are not candidates and never will be.** Both change --
a rename, a marriage, a team move -- and keying on either would silently fork
a person's history into a second account, leaving their certificates and
enrollments attached to an identity nobody can log in as.

:::caution[Choose it before the first login]
Changing this on a running deployment re-keys every login. Anyone whose row
was written under the old claim no longer matches and is created fresh, with
an empty history. There is no automatic relink.
:::

The [Claims echo](#seeing-what-your-provider-actually-sends) reports which
claim this reads, and it is the first thing to check there: every other
mapping can be wrong and be corrected later, while a subject claim that varies
between logins creates a new account each time.

## Seeing what your provider actually sends

Writing `fields.extra` is guesswork until you know what is in the token. The
**Claims echo** (`/admin/identity/echo`, admin-only) removes the guessing: it
shows an admin their own decoded ID token, with every claim annotated by the
configured field that consumes it, and the config block that would capture the
ones nothing reads.

It **re-authenticates rather than remembers.** There is no stored copy of your
claims to show -- the server keeps only what the configuration maps -- so
starting an echo signs you in again with `prompt=login`, and the callback
renders the token instead of establishing a session. Your existing session is
untouched, no user record is written, and no login is recorded.

Nothing about the result is stored. It reaches the page in the URL fragment,
which browsers never send to a server, and the page clears the address bar as
soon as it has read it. Reload and it is gone. There is no shareable link, on
purpose: a link would be the thing that turns a non-storing feature into a
storing one.

Two things it deliberately does not do:

- It only ever shows **your own** claims. That answers "what fields do I get
  from this provider", which is the configuration question. It does not answer
  "why is this other person missing a group" -- the
  [user detail page](/ssoossh/operations/roles/) and the
  [directory probe](/ssoossh/operations/ldap/) are for that.
- It shows the token, not the provider's whole user record. A claim your
  provider only releases under a scope you have not requested will not appear;
  add the scope to
  [`authentication.scopes`](/ssoossh/reference/config/authentication/#scopes)
  and echo again.

Each echo is a real authentication against your provider, so it appears in the
provider's own sign-in log like any other.

## Total denial belongs to the provider

Conditions in `ssoosshd` shape what an already-admitted identity receives.
An identity that should not reach the server at all is expected never to be
issued a token in the first place. Offboarding is the same statement: disable
the person in the identity provider and they cannot get a new certificate,
while the one they hold expires on its own.

## Troubleshooting

**The provider rejects the redirect URI.** Almost always
[`http.public_url`](/ssoossh/reference/config/http/#public_url). It must be
the scheme and host browsers actually reach the deployment at, which behind a
reverse proxy is the proxy's public name and `https`, not the address and port
`ssoosshd` binds. Compare the URI registered with the provider against
`<public_url>/auth/callback` character for character; a trailing slash or an
`http` where the browser uses `https` is enough to fail. `public_url` is
origin-only, so a sub-path deployment is not supported and is rejected at
startup rather than producing a redirect URI that silently does not work.

**Discovery fails at startup.** `provider_url` is wrong, or the server cannot
reach the provider. Fetch
`<provider_url>/.well-known/openid-configuration` from the server host.

**Login succeeds but every group gate denies.** The groups claim is not
arriving. Check that the scope releasing it is in
[`authentication.scopes`](/ssoossh/reference/config/authentication/#scopes),
and that
[`fields.groups`](/ssoossh/reference/config/authentication/#fieldsgroups)
names the claim your provider actually emits. Roles fail closed: no identity,
no group, or no configured group all deny.

**Requests are rejected with 421 Misdirected Request.** The request was
addressed to a host name other than `public_url`'s. The health endpoints are
exempt, so probes by IP still work.
