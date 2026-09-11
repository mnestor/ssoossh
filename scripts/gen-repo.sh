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
skip_apk=""

while [ $# -gt 0 ]; do
	case "$1" in
	--dist) dist=$2; shift 2 ;;
	--out) out=$2; shift 2 ;;
	--base-url) base_url=$2; shift 2 ;;
	--key-id) key_id=$2; shift 2 ;;
	--suite) suite=$2; shift 2 ;;
	--component) component=$2; shift 2 ;;
	--releasevers) releasevers=$2; shift 2 ;;
	--skip-apk) skip_apk=yes; shift ;;
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

# copy_unique copies $1 into directory $2, refusing to overwrite a file of
# the same name whose contents differ.
#
# This is the guard on the rule below that a package with no dist tag is
# published into every release's tree. Two builds that differ only in what
# they link against -- pam-ssoossh built in almalinux:8 against
# libcrypto.so.1.1 and in almalinux:9 against libcrypto.so.3 -- produce the
# same name, version and release, and therefore the same file name and the
# same NEVRA. Same NEVRA IS the same package: a repository cannot hold both,
# and without this the second copy silently overwrites the first, leaving
# whichever the directory walk reached last to be offered to every release.
# An EL8 host is then offered a package built against an OpenSSL it does not
# have.
#
# Failing the build is the only honest answer, but note carefully what the
# failure does and does not tell you. It says two files claim one NEVRA. It
# CANNOT say whether they are different packages that need distinct Release
# values, or one package built twice in separate jobs -- because rpm stamps
# BUILDTIME from the clock, so even a rebuild of identical input from the
# same tarball differs in bytes and checksum. A comparison of contents can
# never separate those two cases, and the error text says so rather than
# asserting the first and sending someone hunting a payload difference that
# does not exist.
copy_unique() {
	src=$1
	destdir=$2
	base=$(basename "$src")

	if [ -e "$destdir/$base" ]; then
		if cmp -s "$src" "$destdir/$base"; then
			return 0
		fi
		echo "gen-repo: refusing to publish two different files as $base" >&2
		echo "gen-repo:   candidate: $src" >&2
		echo "gen-repo:   already placed: $destdir/$base" >&2
		echo "gen-repo:" >&2
		echo "gen-repo: They share a NEVRA, so they are one package as far as any" >&2
		echo "gen-repo: repository is concerned, and it cannot hold both. There are" >&2
		echo "gen-repo: two ways to get here and they need opposite fixes:" >&2
		echo "gen-repo:" >&2
		echo "gen-repo:  1. Genuinely different packages sharing a version -- the" >&2
		echo "gen-repo:     OpenSSL variants of a module, say. Give them distinct" >&2
		echo "gen-repo:     Release values (a dist tag: 1.el8, 1.el9) so they can be" >&2
		echo "gen-repo:     filed per release." >&2
		echo "gen-repo:" >&2
		echo "gen-repo:  2. ONE package built twice, in separate jobs. Do not chase a" >&2
		echo "gen-repo:     payload difference: rpm stamps BUILDTIME from the clock," >&2
		echo "gen-repo:     so two builds of identical input are never byte-identical" >&2
		echo "gen-repo:     and this check cannot tell them apart from case 1. Build" >&2
		echo "gen-repo:     it once and publish that, rather than picking a winner" >&2
		echo "gen-repo:     here." >&2
		exit 1
	fi

	cp "$src" "$destdir/$base"
}

rm -rf "$out"
mkdir -p "$out"

# ---------------------------------------------------------------- apt ------
# A pool plus one binary-<arch> index per architecture found. Architectures
# are read from the packages themselves rather than hardcoded, so a new
# goreleaser target appears in the repository without editing this script.
debs=$(find "$dist" -name '*.deb' -type f | sort)
if [ -n "$debs" ]; then
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
		copy_unique "$deb" "$out/apt/pool/$component/$arch"
		case " $arches " in
		*" $arch "*) ;;
		*) arches="$arches $arch" ;;
		esac
	done
	arches=${arches# }

	for arch in $arches; do
		mkdir -p "$out/apt/dists/$suite/$component/binary-$arch"
	done

	# Checked here rather than up front so that the collision guard in
	# copy_unique runs first: a package set that cannot be published is a
	# fault in the input, and reporting it only on a machine that happens
	# to have the index writers installed would be the wrong order.
	need apt-ftparchive "apt-utils provides it"

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
	echo "gen-repo: building yum repository"

	# One repository per EL major, addressed by $releasever in the .repo
	# baseurl -- the layout Docker and PostgreSQL use.
	#
	# It exists for the PAM module, whose OpenSSL variants are built in
	# almalinux:8 and almalinux:9 and carry .el8/.el9 dist tags. Those are
	# one package name at distinct NEVRAs, which is what lets the solver
	# pick: the same name at the same version in one repository would BE
	# the same package, and createrepo would collide rather than offer a
	# choice.
	#
	# The packages themselves live once, in a shared pool, and each
	# release's repodata points back at it with --location-prefix. Copying
	# instead would store every dist-tag-free package -- everything ssoossh
	# builds, all static Go -- once per release, tripling the rpm half of
	# the tree for three EL majors. createrepo_c --pkglist selects which of
	# the pool each tree offers, so an EL8 host is still never shown an
	# el9 package.
	need rpm "rpm provides the query used to read each package's Release"

	pool="$out/yum/pool"
	mkdir -p "$pool"

	for releasever in $releasevers; do
		mkdir -p "$out/yum/el/$releasever"
		: > "$out/yum/el/$releasever/.pkglist"
	done

	for rpm in $rpms; do
		release=$(rpm -qp --queryformat '%{RELEASE}' "$rpm")
		base=$(basename "$rpm")
		copy_unique "$rpm" "$pool"

		matched=""
		for releasever in $releasevers; do
			# 1.el9 and, defensively, anything that appends to it. Anchored
			# on the tag rather than matched loosely so that .el1 cannot
			# swallow .el10.
			case "$release" in
			*".el$releasever" | *".el$releasever."*)
				echo "$base" >> "$out/yum/el/$releasever/.pkglist"
				matched=yes
				;;
			esac
		done
		if [ -z "$matched" ]; then
			for releasever in $releasevers; do
				echo "$base" >> "$out/yum/el/$releasever/.pkglist"
			done
		fi
	done

	for releasever in $releasevers; do
		# --location-prefix rewrites each location href to reach back out of
		# this release's directory into the shared pool. Two levels up from
		# yum/el/<releasever>/ is yum/, where pool/ sits.
		createrepo_c --quiet \
			--outputdir "$out/yum/el/$releasever" \
			--pkglist "$out/yum/el/$releasever/.pkglist" \
			--location-prefix ../../pool \
			"$pool"
		rm -f "$out/yum/el/$releasever/.pkglist"

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

# ---------------------------------------------------------------- apk ------
# Not built, and refused loudly rather than ignored.
#
# Silently dropping a package format is the exact failure this script has
# already produced twice -- once through apt-ftparchive's file-name --arch
# filter and once by reading an rpm's dist tag from its file name. Both left
# a repository that resolved and installed correctly while being short a
# package. An apk quietly absent from the tree is the same bug with a whole
# distribution behind it.
#
# apk is not a small addition, because it cannot share the signing key.
# nfpm signs deb and rpm with the OpenPGP key; apk is signed with a bare RSA
# key whose public half a host installs as /etc/apk/keys/<name>.rsa.pub.
# That is a second long-lived key to generate, publish, protect and commit
# to -- the same class of decision as the repository hostname, and not one
# this script should make by existing.
#
# Note ssoossh's apk is also unsigned today: .goreleaser.yml builds the apk
# format for the server with no apk: signature: block.
#
# --skip-apk proceeds deliberately, and leaves a trace that says so.
apks=$(find "$dist" -name '*.apk' -type f | sort)
if [ -n "$apks" ]; then
	if [ -z "$skip_apk" ]; then
		echo "gen-repo: found .apk packages, which this script does not publish:" >&2
		for apk in $apks; do echo "gen-repo:   $(basename "$apk")" >&2; done
		echo "gen-repo:" >&2
		echo "gen-repo: apk needs its own repository and its own RSA signing key" >&2
		echo "gen-repo: (/etc/apk/keys/<name>.rsa.pub); it cannot use the OpenPGP key" >&2
		echo "gen-repo: that signs the deb and rpm. Decide that, or pass --skip-apk to" >&2
		echo "gen-repo: publish without Alpine and leave those packages to direct" >&2
		echo "gen-repo: download." >&2
		exit 1
	fi
	echo "gen-repo: skipping $(echo "$apks" | wc -l | tr -d ' ') apk package(s) -- Alpine is not served by this repository" >&2
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
