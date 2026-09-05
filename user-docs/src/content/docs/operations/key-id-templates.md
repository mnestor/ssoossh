---
title: Key ID templates
description: Shaping the certificate key ID sshd writes to its auth log, per certificate type.
eyebrow: Server operations
sidebar:
  order: 8
---

The key ID is the free-text field `sshd` writes to its auth log for every
certificate login, which makes it the audit trail's join key: it is how an
issuance recorded in `ssoosshd` meets a session recorded on the target host.
Each certificate type's key ID is produced by a Go
[`text/template`](https://pkg.go.dev/text/template) configured under
`cert_options.<type>.key_id_template`.

## Template fields

| Field | Value |
| --- | --- |
| `{{.Username}}` | The approver's username (the configured [`authentication.fields.username`](/ssoossh/reference/config/authentication/#fieldsusername) claim) |
| `{{.Subject}}` | The approver's OIDC subject (`sub` claim), stable across logins |
| `{{.Email}}` | The approver's email, when the provider supplies one |
| `{{.ClientIP}}` | The IP the certificate request was created from |
| `{{.UniqueID}}` | The certificate request's UUID |
| `{{.Extra.<name>}}` | An operator-defined extra claim field, captured at the approver's login |

The fields render from the *approver's* login. For a service enrollment that
names the human who approved it, and the key ID is fixed at approval, because
the approving identity is long gone by the time `service retrieve` redeems the
code unattended.

## Extra fields

[`authentication.fields.extra`](/ssoossh/reference/config/authentication/#fieldsextra)
maps template field names to OIDC claim names:

```yaml
authentication:
  fields:
    extra:
      dept: "https://idp.example.com/department"
      accounts: altAccounts

cert_options:
  user:
    key_id_template: '{{.Username}}-{{.Extra.dept}}-{{join .Extra.accounts ";"}}'
```

Each configured claim is read from the ID token at login and stored on the
user record: strings, booleans, and numbers become strings; a JSON array keeps
its string elements as a list. At approval time the values come from that
stored record, so they are as fresh as the approver's most recent login.

A list-valued extra interpolated directly (`{{.Extra.accounts}}`) joins its
elements with commas; `{{join .Extra.accounts ";"}}` joins with the given
separator instead.

A field name needs to be usable in `{{.Extra.name}}` syntax -- letters,
digits, and underscores, not starting with a digit. Other names are still
reachable as `{{index .Extra "my-name"}}`.

Directory attributes can supply extra fields on the same contract; see
[LDAP enrichment](/ssoossh/operations/ldap/).

## MISSING

A template field with no value at issuance renders as `MISSING` rather than as
an empty string, so a key ID shows an auditable gap instead of silently
collapsing (`alice--eng` versus `alice-MISSING-eng`). This covers:

- a configured extra claim the ID token did not carry at login (login itself
  never fails over an absent extra claim; it logs a warning and stores the
  field empty),
- an `{{.Extra.name}}` reference to a name that was never configured,
- an empty standard field (in practice only `{{.Email}}` can be empty).

A typo in a *standard* field (`{{.Emial}}`) is different: it fails at startup,
as does malformed template syntax. Every configured template is parsed and
test-executed once at boot, so those mistakes never reach the first issuance.

## Per-type defaults and fallback

| Type | Unset behavior |
| --- | --- |
| [`user`](/ssoossh/reference/config/cert_options/user/#key_id_template) | Built-in default `{{.Username}}` |
| [`service`](/ssoossh/reference/config/cert_options/service/#key_id_template) | Falls back to the configured `user` template, then to the same built-in default |
| [`pam`](/ssoossh/reference/config/cert_options/pam/#key_id_template) | Built-in default `pam:{{.Username}}`. Deliberately never falls back to the `user` template |
| [`console`](/ssoossh/reference/config/cert_options/console/#key_id_template) | Built-in default `console:{{.Username}}`, on the same terms as PAM |

The reason `pam` and `console` do not inherit the `user` template is that a
`sudo`, a console login, and an SSH login by the same person have to stay
distinguishable in an audit log. An unset template identifies the type instead
of silently reading like a user certificate.

## A worked configuration

```yaml
authentication:
  fields:
    extra:
      dept: "https://idp.example.com/department"

cert_options:
  user:
    key_id_template: '{{.Username}}:{{.Extra.dept}}:{{.ClientIP}}:{{.UniqueID}}'
  pam:
    key_id_template: 'sudo:{{.Username}}:{{.ClientIP}}'
```

An SSH login by `alice` in engineering from `10.1.2.3` then logs as
`alice:eng:10.1.2.3:3f8c...`, and her `sudo` on that host as
`sudo:alice:10.1.2.3` -- two lines nothing can confuse for each other, each
carrying the request UUID or the address needed to find the approval record
behind it.

Keep in mind that everything in a key ID lands in a log on every target host.
`{{.Subject}}` and `{{.Email}}` are both fine identifiers; whether you want
them replicated across a fleet's auth logs is a decision worth making
deliberately.
