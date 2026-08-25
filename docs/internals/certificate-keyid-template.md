# Certificate key ID templates

The key ID is the free-text field `sshd` writes to its auth log for every
certificate login, which makes it the audit trail's join key. Each
certificate type's key ID is produced by a Go
[`text/template`](https://pkg.go.dev/text/template) configured under
`cert_options.<type>.key_id_template`.

## Template fields

Keep this table in sync with `keyIDTemplateData` in
`server/service/keyid.go`.

| Field | Value |
| --- | --- |
| `{{.Username}}` | The approver's username (the configured `authentication.fields.username` claim). |
| `{{.Subject}}` | The approver's OIDC subject (`sub` claim), stable across logins. |
| `{{.Email}}` | The approver's email, when the provider supplies one. |
| `{{.ClientIP}}` | The IP the certificate request was created from. |
| `{{.UniqueID}}` | The certificate request's UUID. |
| `{{.Extra.<name>}}` | An operator-defined extra claim field, captured at the approver's login. See below. |

## Extra fields

`authentication.fields.extra` maps template field names to OIDC claim
names:

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
user record: strings, booleans, and numbers become strings; a JSON array
keeps its string elements as a list. At approval time the values come from
that stored record, so they are as fresh as the approver's most recent
login.

A list-valued extra interpolated directly (`{{.Extra.accounts}}`) joins
its elements with commas; `{{join .Extra.accounts ";"}}` joins with the
given separator instead.

A field name needs to be usable in `{{.Extra.name}}` syntax (letters,
digits, and underscores, not starting with a digit); other names are still
reachable as `{{index .Extra "my-name"}}`.

## MISSING

A template field with no value at issuance renders as `MISSING` rather
than as an empty string, so a key ID shows an auditable gap instead of
silently collapsing (`alice--eng` vs `alice-MISSING-eng`). This covers:

- a configured extra claim the ID token did not carry at login (login
  itself never fails over an absent extra claim; it logs a warning and
  stores the field empty),
- an `{{.Extra.name}}` reference to a name that was never configured,
- an empty standard field (in practice only `{{.Email}}` can be empty).

A typo in a *standard* field (`{{.Emial}}`) is different: it fails at
startup, as does malformed template syntax — every configured template is
parsed and test-executed once at boot so those mistakes never reach the
first issuance.

## Per-type defaults and fallback

| Type | Unset behavior |
| --- | --- |
| `user` | Built-in default `{{.Username}}`. |
| `service` | Falls back to the configured `user` template, then to the same built-in default. |
| `pam` | Built-in default `pam:{{.Username}}`. Deliberately never falls back to the `user` template, so a `sudo` and a login by the same person stay distinguishable in an audit log. |
