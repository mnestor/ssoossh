#!/bin/sh
# Builds the SELinux policy modules in packaging/selinux/ into
# build/selinux/<module>.pp, where the rpm policy packages take them from
# (.goreleaser.yml):
#
#   ssoossh_nss    ssoossh_nss.{te,fc,if}  ssoossh-nss-selinux (nfpm "nss-selinux")
#   ssoossh_agent  ssoossh_agent.te        ssoosshd-selinux (nfpm "server-selinux")
#
#   scripts/build-selinux.sh            # build, in a container if needed
#   scripts/build-selinux.sh --check    # build, then link them into the policy
#   scripts/build-selinux.sh --link     # link the modules already built
#   SELINUX_IMAGE=almalinux:10 scripts/build-selinux.sh --link
#
# A policy module is compiled against the refpolicy headers of the policy it
# will load into, which come from selinux-policy-devel. Where that is
# installed (the CI job's almalinux:9 container, an EL host) this builds
# directly. Anywhere else it re-runs itself in a container, almalinux:9
# unless SELINUX_IMAGE names another,
# so a Debian or Ubuntu workstation builds the same modules CI does.
#
# Every module goes through the selinux-policy-devel Makefile, whatever it
# is written in. ssoossh_nss uses the refpolicy interfaces and has its own
# .fc and .if; ssoossh_agent is plain module syntax with neither, which the
# Makefile compiles as it is (it writes an empty .fc and .if for it), so the
# same .te also builds by hand with checkmodule.
#
# --check then installs each module into the policy store without loading
# it (semodule -n), which links it against the whole targeted policy, and
# --link does only that, for the modules a build already made: the shipped
# modules are built once against EL9's headers, and --link in an
# almalinux:10 container is what says they still link into EL10's policy. A
# rule naming a type or attribute the policy lacks fails here rather than
# in a package's postinstall. Nothing reaches the running kernel, so it
# works in a container on a host without SELinux.
#
# The output basename must match the module name in the .te: the refpolicy
# Makefile derives the module from the file name, and semodule names the
# module after it.
set -eu

# Every module this script builds; each needs packaging/selinux/<name>.te.
modules="ssoossh_nss ssoossh_agent"
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

for name in $modules; do
	if [ ! -f "$src/$name.te" ]; then
		echo "build-selinux: run from the repository root ($src/$name.te not found)" >&2
		exit 1
	fi
done

if [ ! -f "$devel" ]; then
	command -v docker >/dev/null 2>&1 || {
		echo "build-selinux: no $devel here and no docker to run $image" >&2
		exit 1
	}
	# Root inside the container for dnf, then the output handed back to the
	# caller's uid so a later `make clean` or rm needs no sudo.
	mkdir -p "$out"
	exec docker run --rm \
		-v "$PWD/$src:/src/$src:ro" \
		-v "$PWD/scripts/build-selinux.sh:/src/scripts/build-selinux.sh:ro" \
		-v "$PWD/$out:/src/$out" \
		-w /src \
		-e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
		"$image" sh -c '
			dnf -y -q install selinux-policy-devel make >/dev/null &&
			scripts/build-selinux.sh '"${1:-}"' &&
			chown -R "$HOST_UID:$HOST_GID" '"$out"
fi

# Compile each module in a scratch directory of its own: the Makefile
# builds every .te it finds beside the target, and writes its tmp/ there.
if "$build"; then
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	mkdir -p "$out"
	for name in $modules; do
		mkdir "$work/$name"
		for ext in te fc if; do
			if [ -f "$src/$name.$ext" ]; then
				cp "$src/$name.$ext" "$work/$name/"
			fi
		done
		make -C "$work/$name" -f "$devel" "$name.pp"

		cp "$work/$name/$name.pp" "$out/$name.pp"
		# The policy the module was compiled against, kept beside it for the
		# package's version floor and for whoever reads a load failure.
		rpm -q --qf '%{VERSION}-%{RELEASE}\n' selinux-policy-devel >"$out/$name.policyver"
		echo "build-selinux: $out/$name.pp, against selinux-policy $(cat "$out/$name.policyver")"
	done
else
	for name in $modules; do
		if [ ! -f "$out/$name.pp" ]; then
			echo "build-selinux: --link needs $out/$name.pp; build it first" >&2
			exit 1
		fi
	done
fi

# Link each module into the store without loading it, confirm the store
# lists it, and take it out again, so the container's store ends as it began.
if "$check"; then
	command -v semodule >/dev/null 2>&1 || dnf -y -q install policycoreutils >/dev/null
	rpm -q selinux-policy-targeted >/dev/null 2>&1 || dnf -y -q install selinux-policy-targeted >/dev/null
	for name in $modules; do
		semodule -n -i "$out/$name.pp"
		semodule -n -l | grep -qx "$name" || {
			echo "build-selinux: $name is not in the policy store after semodule -i" >&2
			exit 1
		}
		semodule -n -r "$name"
		echo "build-selinux: $name links into selinux-policy-targeted $(rpm -q --qf '%{VERSION}-%{RELEASE}' selinux-policy-targeted)"
	done
fi
