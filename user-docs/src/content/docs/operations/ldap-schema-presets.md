---
title: LDAP schema presets
description: One ldap.schema key fills in the directory-specific LDAP and NSS keys for RFC 2307, RFC 2307bis, Active Directory, lldap or FreeIPA.
sidebar:
  order: 11
---

Directories store the same things under different names: an account name
is `uid` in RFC 2307 and `sAMAccountName` in Active Directory, a group lists
its members by name in `memberUid` or by DN in `member`, and every directory
calls its unique entry id something else. One key,
[`ldap.schema`](/ssoossh/reference/config/ldap/#schema), picks a preset that
fills in every key whose right value depends on which directory you run:

```yaml
ldap:
  schema: active-directory   # rfc2307, rfc2307bis, active-directory, lldap or freeipa
```

Unset (the default), no preset applies and every key keeps its own built-in
default. Any other value stops the server at startup with a message naming
the five allowed values.

## What each preset sets

Every value below is exactly what the server merges: the table is generated
from the preset files embedded in `ssoosshd`. "not set" means the preset
leaves that key alone, so it keeps its built-in default (or your own value).
A preset never touches anything outside these keys.

<!-- BEGIN GENERATED: ldap.schema presets (make confdocs) -->
| Key | Unset (built-in default) | `rfc2307` | `rfc2307bis` | `active-directory` | `lldap` | `freeipa` |
| --- | --- | --- | --- | --- | --- | --- |
| [`ldap.id_attribute`](/ssoossh/reference/config/ldap/#id_attribute) | *empty* | `entryUUID` | `entryUUID` | `objectGUID` | `entryUUID` | `ipaUniqueID` |
| [`nss.user_filter`](/ssoossh/reference/config/nss/#user_filter) | `(objectClass=posixAccount)` | `(objectClass=posixAccount)` | `(objectClass=posixAccount)` | `(&(objectCategory=person)(objectClass=user)(uidNumber=*))` | `(&(objectClass=posixAccount)(uidNumber=*))` | `(objectClass=posixAccount)` |
| [`nss.group_filter`](/ssoossh/reference/config/nss/#group_filter) | `(objectClass=posixGroup)` | `(objectClass=posixGroup)` | `(objectClass=posixGroup)` | `(&(objectClass=group)(gidNumber=*))` | `(&(objectClass=groupOfUniqueNames)(gidNumber=*))` | `(objectClass=posixGroup)` |
| [`nss.disabled_filter`](/ssoossh/reference/config/nss/#disabled_filter) | *empty* | not set | not set | `(userAccountControl:1.2.840.113556.1.4.803:=2)` | not set | `(nsAccountLock=TRUE)` |
| [`nss.attributes.name`](/ssoossh/reference/config/nss/attributes/#name) | `uid` | `uid` | `uid` | `sAMAccountName` | `uid` | `uid` |
| [`nss.attributes.uid`](/ssoossh/reference/config/nss/attributes/#uid) | `uidNumber` | `uidNumber` | `uidNumber` | `uidNumber` | `uidNumber` | `uidNumber` |
| [`nss.attributes.gid`](/ssoossh/reference/config/nss/attributes/#gid) | `gidNumber` | `gidNumber` | `gidNumber` | `gidNumber` | `gidNumber` | `gidNumber` |
| [`nss.attributes.home`](/ssoossh/reference/config/nss/attributes/#home) | `homeDirectory` | `homeDirectory` | `homeDirectory` | `unixHomeDirectory` | `homeDirectory` | `homeDirectory` |
| [`nss.attributes.shell`](/ssoossh/reference/config/nss/attributes/#shell) | `loginShell` | `loginShell` | `loginShell` | `loginShell` | `unixShell` | `loginShell` |
| [`nss.attributes.group_name`](/ssoossh/reference/config/nss/attributes/#group_name) | `cn` | `cn` | `cn` | `sAMAccountName` | `cn` | `cn` |
| [`nss.attributes.group_gid`](/ssoossh/reference/config/nss/attributes/#group_gid) | `gidNumber` | `gidNumber` | `gidNumber` | `gidNumber` | `gidNumber` | `gidNumber` |
| [`nss.attributes.group_member`](/ssoossh/reference/config/nss/attributes/#group_member) | `memberUid` | `memberUid` | `member` | `member` | `uniqueMember` | `member` |
<!-- END GENERATED: ldap.schema presets -->

### `rfc2307`

For OpenLDAP or 389 Directory Server with the classic RFC 2307 schema:
accounts are `posixAccount`, groups are `posixGroup`, and groups list their
members **by name** in `memberUid`. The id attribute is `entryUUID`, the
UUID string RFC 4530 defines. There is no disabled filter, because RFC 2307
has no attribute that marks an account disabled.

Sources: the attribute names are sssd's `rfc2307` defaults
(`rfc2307_user_map` and `rfc2307_group_map` in
`src/providers/ldap/ldap_opts.c`, documented in sssd-ldap-attributes(5)).
sssd leaves its own UUID attribute unset for this schema; `entryUUID` comes
from RFC 4530.

### `rfc2307bis`

The same accounts as `rfc2307`, with `posixGroup` used as an auxiliary class
on `groupOfNames` groups that list their members **by DN** in `member`. Use
it when `ldapsearch` shows your groups carrying `member:
uid=alice,ou=people,...` rather than `memberUid: alice`. The id attribute is
`entryUUID`, and there is no disabled filter.

Sources: sssd's `rfc2307bis` defaults (`rfc2307bis_user_map` and
`rfc2307bis_group_map` in `src/providers/ldap/ldap_opts.c`), and RFC 4530
for `entryUUID`.

### `active-directory`

For Active Directory accounts and groups that carry POSIX attributes
(`uidNumber`, `gidNumber`), the layout sssd reads with `ldap_id_mapping =
False`. Names come from `sAMAccountName` for both people and groups, the
home directory from `unixHomeDirectory`, and members **by DN** from `member`.
The user filter requires `objectCategory=person` as well as
`objectClass=user`, because computer accounts are of class `user` too, and
both filters require the numeric id to be present so entries without POSIX
data are not read at all. The id attribute is `objectGUID`, which is sixteen
raw bytes: ssoossh stores it as hex behind a `0x` marker and searches for it
with the RFC 4515 escape of each byte, so it round-trips exactly. The
disabled filter matches the `ACCOUNTDISABLE` bit (`0x0002`) of
`userAccountControl` with the bitwise-AND matching rule
`1.2.840.113556.1.4.803`.

Sources: sssd's AD maps (`ad_2008r2_user_map` and `ad_2008r2_group_map` in
`src/providers/ad/ad_opts.c`) for every attribute name and for
`objectGUID`; sssd's enumeration filters
(`src/providers/ldap/sdap_async_enum.c`) for the presence tests; Microsoft's
"Search Filter Syntax" for `(&(objectCategory=person)(objectClass=user))` and
the matching rule; Microsoft's "UserAccountControl property flags" for the
`ACCOUNTDISABLE` bit.

### `lldap`

For [lldap](https://github.com/lldap/lldap), with the POSIX custom
attributes its PAM guide (`example_configs/pam/README.md`) has you create:
`uidNumber`, `gidNumber`, `homeDirectory` and `unixShell` on users, and
`gidNumber` on groups. Those attributes are not built in, so the preset uses
the names that guide gives them; if you created yours under other names, set
the matching `nss.attributes.*` keys. Note the shell is **`unixShell`**, not
`loginShell`. Users are `posixAccount` (lldap gives every user that class),
groups are `groupOfUniqueNames` with members **by DN** in `uniqueMember`, and
both filters require the numeric id, since lldap entries nobody filled in
have none. The id attribute is `entryUUID`, which lldap serves for every
user and group. There is no disabled filter: lldap has no way to mark an
account disabled.

Sources: lldap's own source (`crates/ldap/src/core/user.rs`, `group.rs` and
`utils.rs`, at commit `99c510a7`) for the object classes, the `entryUUID`
mapping, `uniqueMember` and case-insensitive attribute names; its PAM guide
for the custom attribute names and the `(uidNumber=*)` and `(gidNumber=*)`
filters.

### `freeipa`

For FreeIPA. Accounts are `posixAccount` and groups `posixGroup` (FreeIPA
also has non-POSIX groups, which carry no `gidNumber`), with members **by
DN** in `member`. The id attribute is `ipaUniqueID`, a UUID string FreeIPA
puts on every account and group. The disabled filter is
`(nsAccountLock=TRUE)`, which is what `ipa user-disable` writes.

Set [`ldap.base_dn`](/ssoossh/reference/config/ldap/#base_dn) to
`cn=accounts` under your suffix (for example
`cn=accounts,dc=example,dc=net`). FreeIPA's schema compatibility tree under
`cn=compat` republishes every `posixAccount` and `posixGroup`, so a base
above both would read each account twice.

Sources: sssd's IPA maps (`ipa_user_map` and `ipa_group_map` in
`src/providers/ipa/ipa_opts.c`); FreeIPA's own source for `ipaUniqueID`
(`ipaserver/plugins/baseuser.py`), for `nsAccountLock: TRUE`
(`ipaserver/plugins/ldap2.py`, `set_entry_active`) and for the compat tree
(`install/updates/80-schema_compat.update`).

## Your file always wins

A preset sits between the built-in defaults and your config file. The order,
lowest to highest, is: built-in defaults, then the preset, then your file.
Any key your file sets beats the preset, and the preset's other keys stay in
place beside it. A preset is a starting point, not a lock.

For example, an Active Directory that keeps shells in an attribute of its own:

```yaml
ldap:
  schema: active-directory
nss:
  attributes:
    shell: exampleLoginShell
```

`nss.attributes.shell` is `exampleLoginShell`; every other key is still the
`active-directory` value (`sAMAccountName`, `unixHomeDirectory`, `member`,
`objectGUID` and the rest). An explicit empty string counts too:
`disabled_filter: ""` under `nss` turns the `active-directory` disabled
filter off.

The [effective configuration](/ssoossh/operations/roles/) admin
screen shows the merged result, and `ldap.schema` on it says which preset
is in use. It does not mark which values came from the preset; compare
against the table above.

## Changing the id attribute on an existing database

:::caution
Every preset sets [`ldap.id_attribute`](/ssoossh/reference/config/ldap/#id_attribute).
If your database already stores directory ids read from another attribute,
or from a different `ldap.id_attribute` you set before, picking a preset
changes the attribute and every stored id stops matching. There is no
migration tool yet.

Until there is, on an existing database either keep the attribute your ids
were read from set explicitly (it beats the preset), or start from a fresh
database:

```yaml
ldap:
  schema: freeipa
  id_attribute: entryUUID   # what this database's ids were read from
```

If the attribute does change, login enrichment still finds each person by
their stored DN and logs a warning (`has a different unique id`), but the
[NSS directory](/ssoossh/operations/nss/) treats an entry whose id changed
at the same DN as a new entry where the old one used to be.

Turning `ldap.id_attribute` on for the first time is safe: rows stored
without an id are matched by DN, and pick up their id on the next read.
:::
