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
| [`fields.username`](/ssoossh/reference/config/authentication/#fieldsusername) | `preferred_username` | the primary principal, and `{{.Username}}` in key IDs. Required |
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
