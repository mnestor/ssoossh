#!/bin/sh
# Builds the ssoossh-nss SELinux policy module, packaging/selinux/
# ssoossh_nss.{te,fc,if}, into build/selinux/ssoossh_nss.pp, where the
# ssoossh-nss-selinux package (.goreleaser.yml, nfpm "nss-selinux") takes it
# from.
#
#   scripts/build-nss-selinux.sh            # build, in a container if needed
#   scripts/build-nss-selinux.sh --check    # build, then link it into the policy
#   scripts/build-nss-selinux.sh --link     # link the module already built
#   SELINUX_IMAGE=almalinux:10 scripts/build-nss-selinux.sh --link
#
# A policy module is compiled against the refpolicy headers of the policy it
# will load into, which come from selinux-policy-devel. Where that is
# installed (the CI job's almalinux:9 container, an EL host) this builds
# directly. Anywhere else it re-runs itself in a container, almalinux:9
# unless SELINUX_IMAGE names another,
# so a Debian or Ubuntu workstation builds the same module CI does.
#
# --check then installs the module into the policy store without loading
# it (semodule -n), which links it against the whole targeted policy, and
# --link does only that, for the module a build already made: the shipped
# module is built once against EL9's headers, and --link in an almalinux:10
# container is what says it still links into EL10's policy. A
# rule naming a type or attribute the policy lacks fails here rather than
# in a package's postinstall. Nothing reaches the running kernel, so it
# works in a container on a host without SELinux.
#
# The output basename must match the module name in the .te: the refpolicy
# Makefile derives the module from the file name, and semodule names the
# module after it.
set -eu

name=ssoossh_nss
src=packaging/selinux
out=build/selinux
devel=/usr/share/selinux/devel/Makefile
# The build container when this host has no selinux-policy-devel.
image=${SELINUX_IMAGE:-almalinux:9}

build=true
check=false
case "${1:-}" in
--check) check=true ;;
--link) build=false check=true ;;
"") ;;
*)
	echo "usage: $0 [--check | --link]" >&2
	exit 2
	;;
esac

if [ ! -f "$src/$name.te" ]; then
	echo "build-nss-selinux: run from the repository root ($src/$name.te not found)" >&2
	exit 1
fi

if [ ! -f "$devel" ]; then
	command -v docker >/dev/null 2>&1 || {
		echo "build-nss-selinux: no $devel here and no docker to run $image" >&2
		exit 1
	}
	# Root inside the container for dnf, then the output handed back to the
	# caller's uid so a later `make clean` or rm needs no sudo.
	mkdir -p "$out"
	exec docker run --rm \
		-v "$PWD/$src:/src/$src:ro" \
		-v "$PWD/scripts/build-nss-selinux.sh:/src/scripts/build-nss-selinux.sh:ro" \
		-v "$PWD/$out:/src/$out" \
		-w /src \
		-e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
		"$image" sh -c '
			dnf -y -q install selinux-policy-devel make >/dev/null &&
			scripts/build-nss-selinux.sh '"${1:-}"' &&
			chown -R "$HOST_UID:$HOST_GID" '"$out"
fi

if "$build"; then
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	cp "$src/$name.te" "$src/$name.fc" "$src/$name.if" "$work/"
	make -C "$work" -f "$devel" "$name.pp"

	mkdir -p "$out"
	cp "$work/$name.pp" "$out/$name.pp"
	# The policy the module was compiled against, kept beside it for the
	# package's version floor and for whoever reads a load failure.
	rpm -q --qf '%{VERSION}-%{RELEASE}\n' selinux-policy-devel >"$out/$name.policyver"
	echo "build-nss-selinux: $out/$name.pp, against selinux-policy $(cat "$out/$name.policyver")"
elif [ ! -f "$out/$name.pp" ]; then
	echo "build-nss-selinux: --link needs $out/$name.pp; build it first" >&2
	exit 1
fi

if "$check"; then
	command -v semodule >/dev/null 2>&1 || dnf -y -q install policycoreutils >/dev/null
	rpm -q selinux-policy-targeted >/dev/null 2>&1 || dnf -y -q install selinux-policy-targeted >/dev/null
	semodule -n -i "$out/$name.pp"
	semodule -n -l | grep -qx "$name" || {
		echo "build-nss-selinux: $name is not in the policy store after semodule -i" >&2
		exit 1
	}
	semodule -n -r "$name"
	echo "build-nss-selinux: $name links into selinux-policy-targeted $(rpm -q --qf '%{VERSION}-%{RELEASE}' selinux-policy-targeted)"
fi
