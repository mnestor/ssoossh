#!/bin/sh
# ldap.sh: the NSS end-to-end job's directory. Runs in its own container:
# writes a slapd.conf with the nis schema, loads the seed entries offline
# with slapadd, and runs slapd in the foreground on port 389. The admin is
# cn=admin,dc=example,dc=test with the password "test"; nothing outside the
# job's network can reach it.
#
# The seed, under dc=example,dc=test:
#   ada          a person (who also logs in), uid 10001, in devs and ops
#   svc-backup   a service account that never logs in, uid 10002, in devs
#   dup.one      uid 10050 \ the uid collision: neither may resolve
#   dup.two      uid 10050 /
#   locked.out   uid 10005, matching nss.disabled_filter (employeeType=disabled)
#   fresh.name   uid 10006, never looked up until the key is revoked
#   devs         gid 20001: ada and svc-backup
#   ops          gid 20002: ada
set -eu

conf=/etc/ldap/e2e-slapd.conf
data=/var/lib/ldap-e2e
mkdir -p "$data" /run/slapd

cat >"$conf" <<EOC
include /etc/ldap/schema/core.schema
include /etc/ldap/schema/cosine.schema
include /etc/ldap/schema/nis.schema
include /etc/ldap/schema/inetorgperson.schema
pidfile /run/slapd/e2e.pid
modulepath /usr/lib/ldap
moduleload back_mdb
database mdb
maxsize 104857600
suffix "dc=example,dc=test"
rootdn "cn=admin,dc=example,dc=test"
rootpw test
directory $data
index objectClass eq
index uid,uidNumber,gidNumber,memberUid eq
EOC

# account NAME UID [EXTRA-LINE...]: one posixAccount entry.
account() {
	name=$1
	uid=$2
	shift 2
	cat <<EOA
dn: uid=$name,ou=people,dc=example,dc=test
objectClass: inetOrgPerson
objectClass: posixAccount
uid: $name
cn: $name
sn: $name
uidNumber: $uid
gidNumber: $uid
homeDirectory: /home/$name
loginShell: /bin/bash
EOA
	for line in "$@"; do echo "$line"; done
	echo
}

{
	cat <<EOB
dn: dc=example,dc=test
objectClass: dcObject
objectClass: organization
dc: example
o: example

dn: ou=people,dc=example,dc=test
objectClass: organizationalUnit
ou: people

dn: ou=groups,dc=example,dc=test
objectClass: organizationalUnit
ou: groups

EOB
	account ada 10001 "displayName: Ada Lovelace" "mail: ada@example.test"
	account svc-backup 10002 "displayName: Backup Service"
	account dup.one 10050
	account dup.two 10050
	account locked.out 10005 "employeeType: disabled"
	account fresh.name 10006
	cat <<EOG
dn: cn=devs,ou=groups,dc=example,dc=test
objectClass: posixGroup
cn: devs
gidNumber: 20001
memberUid: ada
memberUid: svc-backup

dn: cn=ops,ou=groups,dc=example,dc=test
objectClass: posixGroup
cn: ops
gidNumber: 20002
memberUid: ada
EOG
} >/tmp/seed.ldif

slapadd -q -f "$conf" -l /tmp/seed.ldif
exec slapd -f "$conf" -h "ldap:///" -d 256
