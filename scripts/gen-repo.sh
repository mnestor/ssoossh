#!/usr/bin/env bash
# Builds signed apt and yum repository trees from packages goreleaser has
# already produced, so `dnf install ssoossh-client` replaces constructing a
# download URL by hand.
#
# The whole point is the dependency solver. The packages already declare
# what they need -- the PAM module's per-format soname dependencies are
# correct today -- but a release page cannot run a solver, so a deployer
# maps x86_64 to amd64, picks .deb or .rpm, resolves "latest" through the
# GitHub API, and installs with GPG checking disabled on packages that are
# signed. A repository deletes all of that.
#
# Produces a static tree, servable by anything. It does not publish: where
# this goes is a separate decision, and nothing here assumes a host beyond
# the --base-url it is told.
#
# Usage:
#   scripts/gen-repo.sh --dist dist --out repo --base-url https://example.com/repo
#                       [--key-id KEYID] [--suite stable] [--component main]
#
# Signing is skipped with a warning when no --key-id is given, which is what
# makes the tree reproducible in a test. A published repository must be
# signed: an unsigned one trains users to pass --nogpgcheck, which is the
# habit this is meant to end.
set -euo pipefail

dist=dist
out=repo
base_url=""
key_id=""
suite=stable
component=main
releasevers="8 9 10"

while [ $# -gt 0 ]; do
	case "$1" in
	--dist) dist=$2; shift 2 ;;
	--out) out=$2; shift 2 ;;
	--base-url) base_url=$2; shift 2 ;;
	--key-id) key_id=$2; shift 2 ;;
	--suite) suite=$2; shift 2 ;;
	--component) component=$2; shift 2 ;;
	--releasevers) releasevers=$2; shift 2 ;;
	-h | --help) sed -n '2,30p' "$0"; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

if [ -z "$base_url" ]; then
	echo "gen-repo: --base-url is required; it is written into the .list and .repo files" >&2
	exit 2
fi
if [ ! -d "$dist" ]; then
	echo "gen-repo: no such directory: $dist" >&2
	exit 2
fi

need() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "gen-repo: $1 is not installed ($2)" >&2
		exit 1
	}
}

rm -rf "$out"
mkdir -p "$out"

# ---------------------------------------------------------------- apt ------
# A pool plus one binary-<arch> index per architecture found. Architectures
# are read from the packages themselves rather than hardcoded, so a new
# goreleaser target appears in the repository without editing this script.
debs=$(find "$dist" -name '*.deb' -type f | sort)
if [ -n "$debs" ]; then
	need apt-ftparchive "apt-utils provides it"
	echo "gen-repo: building apt repository"

	# The pool is split by architecture so each index is generated from its
	# own directory. apt-ftparchive's --arch filter is NOT usable here: it
	# matches the file name, not the control field, so a package named
	# ssoossh-server_..._amd64_pkcs11.deb is dropped from an --arch amd64
	# index despite being an amd64 package. That fails silently -- a
	# repository that resolves and installs, just missing a package.
	arches=""
	for deb in $debs; do
		arch=$(dpkg-deb -f "$deb" Architecture)
		mkdir -p "$out/apt/pool/$component/$arch"
		cp "$deb" "$out/apt/pool/$component/$arch/"
		case " $arches " in
		*" $arch "*) ;;
		*) arches="$arches $arch" ;;
		esac
	done
	arches=${arches# }

	for arch in $arches; do
		mkdir -p "$out/apt/dists/$suite/$component/binary-$arch"
	done

	# Paths inside Packages must be relative to the repository root, which
	# is why this runs from $out/apt rather than passing absolute paths.
	(
		cd "$out/apt"
		for arch in $arches; do
			apt-ftparchive packages "pool/$component/$arch" \
				> "dists/$suite/$component/binary-$arch/Packages"
			gzip -9nc "dists/$suite/$component/binary-$arch/Packages" \
				> "dists/$suite/$component/binary-$arch/Packages.gz"
		done

		apt-ftparchive \
			-o "APT::FTPArchive::Release::Origin=ssoossh" \
			-o "APT::FTPArchive::Release::Label=ssoossh" \
			-o "APT::FTPArchive::Release::Suite=$suite" \
			-o "APT::FTPArchive::Release::Codename=$suite" \
			-o "APT::FTPArchive::Release::Components=$component" \
			-o "APT::FTPArchive::Release::Architectures=$arches" \
			release "dists/$suite" > "dists/$suite/Release"

		if [ -n "$key_id" ]; then
			# Both forms: InRelease is what apt prefers, Release.gpg is what
			# older clients still look for, and serving only one of them
			# fails a subset of hosts rather than all of them, which is the
			# harder failure to notice.
			gpg --batch --yes --local-user "$key_id" \
				--clearsign -o "dists/$suite/InRelease" "dists/$suite/Release"
			gpg --batch --yes --local-user "$key_id" \
				-abs -o "dists/$suite/Release.gpg" "dists/$suite/Release"
		fi
	)
fi

# ---------------------------------------------------------------- yum ------
rpms=$(find "$dist" -name '*.rpm' -type f | sort)
if [ -n "$rpms" ]; then
	need createrepo_c "createrepo-c provides it"
	echo "gen-repo: building yum repository"

	# One repository per EL major, addressed by $releasever in the .repo
	# baseurl -- the layout Docker and PostgreSQL use.
	#
	# It exists for the PAM module, whose OpenSSL variants are built in
	# almalinux:8 and almalinux:9 and carry .el8/.el9 dist tags. Those are
	# one package name at distinct NEVRAs, which is what lets the solver
	# pick: the same name at the same version in one repository would BE
	# the same package, and createrepo would collide rather than offer a
	# choice. A package with no dist tag -- everything ssoossh itself
	# builds, all static Go -- is the same file on every release and is
	# published into each.
	for releasever in $releasevers; do
		mkdir -p "$out/yum/el/$releasever/packages"
	done

	for rpm in $rpms; do
		base=$(basename "$rpm")
		matched=""
		for releasever in $releasevers; do
			case "$base" in
			*".el$releasever."*)
				cp "$rpm" "$out/yum/el/$releasever/packages/"
				matched=yes
				;;
			esac
		done
		if [ -z "$matched" ]; then
			for releasever in $releasevers; do
				cp "$rpm" "$out/yum/el/$releasever/packages/"
			done
		fi
	done

	for releasever in $releasevers; do
		createrepo_c --quiet "$out/yum/el/$releasever"

		if [ -n "$key_id" ]; then
			# dnf verifies repomd.xml's detached signature, and the
			# checksums inside it cover everything else, so this one
			# signature is what makes repo_gpgcheck meaningful.
			gpg --batch --yes --local-user "$key_id" \
				-abs -o "$out/yum/el/$releasever/repodata/repomd.xml.asc" \
				"$out/yum/el/$releasever/repodata/repomd.xml"
		fi
	done
fi

# The key every client needs to verify any of the above. Shipped in the tree
# so a bootstrap can fetch it over the same https origin as the packages.
if [ -f gpg-public.asc ]; then
	cp gpg-public.asc "$out/gpg-public.asc"
fi

if [ -z "$key_id" ]; then
	echo "gen-repo: WARNING built unsigned -- do not publish this tree" >&2
fi

echo "gen-repo: wrote $out (base url: $base_url)"
