# LDAP bind via Kerberos GSSAPI

**Status:** idea. Not scheduled. Depends on
[ldap-enrichment-and-sync.md](ldap-enrichment-and-sync.md) landing first;
tracked separately so the enrichment work does not grow an authentication
subsystem.

## What

Replace the static `bind_dn` / `bind_password` service credential with a
SASL GSSAPI (Kerberos) bind: the server authenticates to the directory with
a keytab and a service principal, and tickets refresh on their own schedule
instead of a password sitting in the config file.

Primarily an Active Directory story, where GSSAPI is the native mechanism
and password binds are increasingly disabled by policy. Also removes the one
long-lived secret the enrichment feature otherwise introduces.

## Rough shape

```yaml
ldap:
  bind:
    mechanism: "gssapi"          # "simple" (default) keeps bind_dn/password
    keytab: "/etc/ssoossh/ldap.keytab"
    principal: "ssoossh/host.example.net@EXAMPLE.NET"
```

- Ticket acquisition and renewal handled by the server, logged to the
  existing LDAP log destination; an expired-and-unrenewable ticket is a
  "directory unreachable" condition for the sync (counts nothing, disables
  nobody), exactly like a failed simple bind.
- Everything above the bind is untouched: filters, field mapping, searches,
  sync, and storage from the enrichment proposal are bind-mechanism
  agnostic by design.

## Constraint this places on the enrichment proposal

Only one: keep the connection/bind settings shaped so a `mechanism` can
slot in without breaking existing configs. Flat `bind_dn` / `bind_password`
keys are fine to ship first as the implicit "simple" mechanism; this
proposal would add the `bind` block and treat the flat keys as its
shorthand.

## Open questions

- Library support: pure-Go Kerberos vs cgo/system GSSAPI, and what that
  does to the FIPS build.
- Channel binding requirements (AD `LdapEnforceChannelBinding`) and how
  they interact with StartTLS vs ldaps.
- Whether keytab reload (rotation) needs a SIGHUP hook or a mtime watch.
