# Certificate key ID templates

**Status:** starting set, not finalized — expect fields to be added as other work
(LDAP enrichment, `OtherAccounts`/`ServiceAccounts`, group-based policy) lands.

Four service-only fields are already designed and waiting on phase 8:
`{{.ServiceAccount}}`, `{{.ApprovedBy}}`, `{{.ApproverSubject}}`, and
`{{.ApprovedAt}}`, so a long-lived service certificate records the human who
authorized it. See release-plan.md's deferred-items table (service
certificates and enrollment) for why these are new fields rather than a
redefinition of `{{.Username}}`; the phase document that originally worked
this out, "Service key IDs record who approved them", has since been
removed.

The SSH certificate key ID is a free-form string the CA stamps into every certificate
it signs. `sshd` logs it on every authentication, so it's the audit trail — see
`what-ssoossh-is.md`'s "Certificate terms" section. ssoossh lets you control its shape
per certificate type via a [Go `text/template`](https://pkg.go.dev/text/template)
string, e.g.:

```yaml
cert_options:
  user:
    key_id_template: "{{.Username}}:{{.ClientIP}}:{{.UniqueID}}"
```

## Configuration

Each certificate type has its own `key_id_template` setting:

- `cert_options.user.key_id_template`
- `cert_options.service.key_id_template`
- `cert_options.host.key_id_template`

**Fallback:** user certificates are the common case, so if
`cert_options.service.key_id_template` or `cert_options.host.key_id_template` is
unset, it falls back to `cert_options.user.key_id_template` rather than requiring
every type to be configured explicitly.

**Default (nothing configured at all):** `{{.Username}}` for user and service
certificates, `{{.Hostname}}` for host certificates — this preserves the behavior of
"identity is the key ID" with no configuration required.

Templates are parsed once at startup, so a syntax error fails startup immediately
rather than the first certificate issuance.

## Available fields

| Field | Available for | Description |
| --- | --- | --- |
| `{{.Username}}` | user, service | The resolved OIDC username (`config.OAuthFields.Username`). |
| `{{.Subject}}` | user, service | The OIDC `sub` claim — stable even if `Username` changes. |
| `{{.Email}}` | user, service | The resolved email, if configured/present (`config.OAuthFields.Email`). |
| `{{.ClientIP}}` | user, service, host | The IP address the certificate request was made from. |
| `{{.Hostname}}` | host | The hostname the certificate is being issued for. |
| `{{.UniqueID}}` | user, service, host | The certificate request's own ID — guarantees uniqueness even if every other field collides. |
| `{{.ApproverIP}}` | user, service, host | *Planned.* The address the **approver's browser** came from, as resolved through `http.trusted_proxies`. Distinct from `{{.ClientIP}}`, which is where the requesting client was. Available for every type, since every type is approved by a human. |

Since this list isn't final, template execution against an unknown field name fails
at startup (a bad template is a config error, not a runtime one) — check the error
message against this table if a template you wrote doesn't parse.

## Example

```yaml
cert_options:
  user:
    key_id_template: "{{.Username}}:{{.ClientIP}}:{{.UniqueID}}"
  service:
    key_id_template: "svc:{{.Username}}:{{.UniqueID}}"
  host:
    key_id_template: "host:{{.Hostname}}"
```

produces key IDs like `alice:203.0.113.4:9f2c1e3a-...`,
`svc:alice:9f2c1e3a-...`, and `host:db01.internal`.
