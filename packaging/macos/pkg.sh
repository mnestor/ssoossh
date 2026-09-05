#!/usr/bin/env bash
#
# Wrap one released macOS client archive as a macOS installer package.
#
#   packaging/macos/pkg.sh dist/ssoossh-client_1.2.3_darwin_arm64.zip [outdir]
#
# Why this exists at all: goreleaser's OSS build has no macOS installer
# pipe, so the client shipped as a .zip that a user had to unpack and place
# on PATH by hand -- and that .zip carries the ssoosshd man pages too,
# because an archive owns no filesystem paths and shipping the whole manual
# in one is more useful than shipping half of it. A package does own paths,
# so this installs the client's pages only, exactly the split the .deb and
# .rpm make in .goreleaser.yml's nfpms block.
#
# Nothing is compiled here. The binary inside the archive is the one
# goreleaser built and `quill sign-and-notarize` signed and notarized as a
# build post-hook, and it is copied in untouched: re-signing it would
# replace a notarized Developer ID signature with a fresh one that has no
# notarization behind it.
#
# The package installs, all outside System Integrity Protection:
#
#   /usr/local/bin/ssoossh
#   /usr/local/share/man/man1/ssoossh*.1
#   /usr/local/share/man/man5/ssoossh.yaml.5
#   /usr/local/share/doc/ssoossh-client/  LICENSE, NOTICE, THIRD-PARTY-LICENSES.md
#   /usr/local/share/ssoossh/ssoossh.yaml
#
# Nothing under /etc. The .deb and .rpm ship the defaults to
# /etc/ssoossh/ssoossh.yaml as `config|noreplace`, and a macOS package has no
# equivalent: a payload file is overwritten on every upgrade, and a
# postinstall script that seeds it conditionally is a second, invisible rule
# about when /etc changes. The defaults go in /usr/local/share/ssoossh
# instead, as a file to copy from -- the readme pane says so, and the client
# needs no configuration file to run, since the same defaults are compiled
# into the binary (client/config, //go:embed defaults.yaml).
#
# One package per architecture, matching every other artifact in the
# release. The two darwin binaries are not lipo'd into a universal one:
# the merge rewrites the Mach-O, which discards both quill signatures and
# would make this script the only thing that ever signed the shipped
# client.
#
# Signing and notarization are by environment and off when the variables are
# empty -- a local run gets an unsigned package and the log says so. The
# names are the ones .github/workflows/build.yaml already reads from
# 1Password for quill, so the same item serves both:
#
#   QUILL_INSTALLER_P12   PKCS#12 holding the "Developer ID Installer"
#                         identity, base64. The identity, not the
#                         certificate alone: an export without the private
#                         key imports cleanly and then signs nothing
#   QUILL_SIGN_P12        the "Developer ID Application" P12 quill uses,
#                         base64. Read only as a fallback, for the case
#                         where that one file holds both identities
#   QUILL_SIGN_PASSWORD   the password of whichever file is used
#   QUILL_NOTARY_ISSUER   App Store Connect API issuer id
#   QUILL_NOTARY_KEY_ID   App Store Connect API key id
#   QUILL_NOTARY_KEY      that key, PEM or the PEM's base64
#
# Both P12s must be legacy-shape PKCS#12 -- what Keychain Access exports and
# what `openssl pkcs12 -export -legacy` writes. `security import` cannot read
# an OpenSSL 3 default export (PBES2, AES-256-CBC, SHA-256 MAC): it fails the
# MAC check and reports it as a wrong password, whatever the password is.
#
# A Developer ID *Application* certificate cannot sign an installer package;
# Gatekeeper refuses a downloaded package that is not Installer-signed. So
# once any signing material is present the Installer identity is not
# optional: its absence fails here, loudly, rather than producing a package
# every Mac would reject.
set -euo pipefail

archive=${1:?usage: $0 <darwin client zip> [outdir]}
outdir=${2:-$(dirname "$archive")}
PKG_IDENTIFIER=${PKG_IDENTIFIER:-org.mikenestor.ssoossh-client}
here=$(cd "$(dirname "$0")" && pwd)

if [ "$(uname -s)" != Darwin ]; then
	echo "macos: pkgbuild, productbuild and productsign exist only on macOS" >&2
	exit 1
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/ssoossh-macos-pkg.XXXXXX")
keychain=
cleanup() {
	[ -z "$keychain" ] || security delete-keychain "$keychain" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

# The archive's name is the only place the version and the architecture are
# written down, and goreleaser wrote it from one template
# (.goreleaser.yml's client_archive_name), so parsing it back is exact:
#
#   ssoossh-client_1.2.3_darwin_arm64.zip
#   ssoossh-client_1.2.3-SNAPSHOT-abc1234_darwin_amd64.zip
name=$(basename "$archive" .zip)
case $name in
ssoossh-client_*_darwin_*) ;;
*)
	echo "macos: $archive is not a darwin ssoossh-client archive" >&2
	exit 1
	;;
esac
version=${name#ssoossh-client_}
version=${version%_darwin_*}
goarch=${name##*_darwin_}

# What Distribution.xml's hostArchitectures wants, which is the Mach-O
# spelling rather than Go's. An Intel Mac handed the arm64 package is then
# told so by the installer, instead of by a binary that will not exec.
case $goarch in
amd64)
	hostarch=x86_64
	archname="Intel"
	;;
arm64)
	hostarch=arm64
	archname="Apple silicon"
	;;
*)
	echo "macos: unknown darwin architecture '$goarch' in $name" >&2
	exit 1
	;;
esac

# `git describe` shapes, into what a receipt accepts. A tag is the version;
# anything after it becomes a pre-release, which sorts before the release
# proper. A build with no tag is 0.0.0 plus the commit, so a snapshot
# installed by hand can never outrank a real release on that host.
#
#   1.2.3                    -> 1.2.3
#   1.2.3-rc1                -> 1.2.3-rc1
#   1.2.3-SNAPSHOT-abc1234   -> 1.2.3-SNAPSHOT.abc1234
case $version in
[0-9]*.[0-9]*.[0-9]*-*)
	pkgver="${version%%-*}-$(printf '%s' "${version#*-}" | tr -- '-' '.')"
	;;
[0-9]*.[0-9]*.[0-9]*)
	pkgver=$version
	;;
*)
	pkgver="0.0.0-$(printf 'dev.%s' "$version" | tr -- '-' '.')"
	;;
esac

mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)
out=$outdir/$name.pkg

stage=$work/stage
mkdir -p "$stage"
unzip -q "$archive" -d "$stage"

# Every member the payload draws from, named before anything is built. A
# missing man page or license is the failure this catches: it would
# otherwise produce a package that installs, works, and quietly documents
# nothing.
for required in ssoossh defaults.yaml LICENSE NOTICE THIRD-PARTY-LICENSES.md \
	docs/man/ssoossh.1 docs/man/ssoossh.yaml.5; do
	if [ ! -f "$stage/$required" ]; then
		echo "macos: $archive has no $required" >&2
		exit 1
	fi
done

# The OS floor the installer refuses below, read from the binary rather than
# written down here: it is the deployment target the Go linker stamped, so
# it follows a toolchain bump on its own instead of going stale in a
# constant. macOS refuses to exec a binary whose minos is above the running
# system, so this is the same number the user would otherwise meet as a
# launch failure.
minver=${PKG_MIN_MACOS:-}
if [ -z "$minver" ]; then
	minver=$(otool -l "$stage/ssoossh" |
		awk '/LC_BUILD_VERSION/ {found = 1} found && $1 == "minos" {print $2; exit}')
fi
if [ -z "$minver" ]; then
	echo "macos: no LC_BUILD_VERSION minos in the client binary;" \
		"set PKG_MIN_MACOS to the floor this build needs" >&2
	exit 1
fi

# The payload, laid out as it will land on the host.
root=$work/root
doc=$root/usr/local/share/doc/ssoossh-client
install -d "$root/usr/local/bin" \
	"$root/usr/local/share/man/man1" "$root/usr/local/share/man/man5" \
	"$root/usr/local/share/ssoossh" "$doc"
install -m 0755 "$stage/ssoossh" "$root/usr/local/bin/ssoossh"
# ssoossh*.1 is safe where ssoossh*.5 would not be: every ssoosshd page name
# also starts with "ssoossh", so the .5 is named explicitly and this package
# takes only its own -- the same split .goreleaser.yml's nfpms block makes,
# and for the same reason.
install -m 0644 "$stage"/docs/man/ssoossh*.1 "$root/usr/local/share/man/man1/"
install -m 0644 "$stage/docs/man/ssoossh.yaml.5" "$root/usr/local/share/man/man5/"
install -m 0644 "$stage/defaults.yaml" "$root/usr/local/share/ssoossh/ssoossh.yaml"
install -m 0644 "$stage/LICENSE" "$stage/NOTICE" \
	"$stage/THIRD-PARTY-LICENSES.md" "$doc/"

# Extended attributes would otherwise ride along as AppleDouble `._` entries
# in the payload. The quarantine bit unzip set goes here; com.apple.provenance,
# which a tagged process's files get at creation, cannot be removed by anyone
# and is absent on a runner, which tags nothing.
xattr -rc "$root" 2>/dev/null || true

# What the binary arrived with. Not re-signed and not fixed up: on a release
# quill signed and notarized it before this script ever saw it, and on a
# local snapshot there was no quill, so the answer here is the one line that
# distinguishes the two afterwards in a log.
if codesign --verify --strict "$root/usr/local/bin/ssoossh" 2>/dev/null; then
	echo "macos: client binary carries a valid signature:"
	codesign --display --verbose=2 "$root/usr/local/bin/ssoossh" 2>&1 |
		sed -n 's/^\(Authority\|TeamIdentifier\|Timestamp\)/  &/p'
	signed_payload=yes
else
	echo "macos: client binary is unsigned or its signature does not verify" \
		"(expected for a snapshot build, never for a release)"
	signed_payload=no
fi

# A secret arrives as the file's base64, which is the one form an environment
# variable and a secrets store can both hold; a PEM key may also arrive as
# itself. Written to a file either way, mode 0600.
#
# The whole body is a subshell so the umask stays in it. Set at function
# scope it would outlive the call, and every file made afterwards -- the
# package included -- would come out 0600, which is how a signed build ends
# up producing an artifact the unsigned build makes 0644.
materialise() {
	(
		umask 077
		case $1 in
		*-----BEGIN*) printf '%s\n' "$1" >"$2" ;;
		*) printf '%s' "$1" | tr -d '[:space:]' | base64 -d >"$2" ;;
		esac
	)
}

# What the keychain ended up holding, for a failure that has to be read out
# of a log afterwards. An identity is a certificate *and* its private key, so
# a certificate in the second listing that is not in the first says which
# half is missing: the export left the key behind.
dump_keychain() {
	echo "  identities:" >&2
	security find-identity "$keychain" >&2 || true
	echo "  certificates:" >&2
	security find-certificate -a "$keychain" 2>/dev/null |
		sed -n 's/^ *"labl"<blob>=/    /p' >&2
}

# `security import` says "1 identity imported." for a certificate with its
# private key and "1 certificate imported." for one without, and a
# certificate on its own is no use here. That line is the whole diagnosis
# when the identity lookup below fails, so it is kept, labelled with the
# variable it came from and with the size of what that variable decoded to --
# an empty, truncated or wrong-field secret shows itself there first.
#
# No -f pkcs12, deliberately: the flag suppresses that line and swallows the
# failure underneath it, so a MAC failure exits 0 and an empty keychain looks
# like a successful import.
import_p12() {
	# What the variable decoded to, named before the keychain is involved at
	# all. A PKCS#12 is DER, so it opens with a SEQUENCE -- 0x30 0x82 -- and a
	# field holding a PEM, a .cer or nothing much fails here, where the
	# message can say so, rather than three lines down as a MAC failure that
	# reads like a wrong password. The digest is the file's, not a secret: it
	# is the one way to tell from a log whether the field holds the P12 you
	# think it does, since the same command over the same file on your Mac
	# prints the same 16 characters.
	local size digest out
	size=$(wc -c <"$2" | tr -d ' ')
	digest=$(shasum -a 256 "$2" | cut -c1-16)
	if [ "$(head -c 2 "$2" | od -An -tx1 | tr -d ' \n')" != 3082 ]; then
		echo "macos: $1: $size bytes, sha256 $digest, first bytes" \
			"$(head -c 8 "$2" | od -An -tx1)" >&2
		echo "macos: $1 did not decode to a PKCS#12, which is DER and opens 30 82;" \
			"the field holds something that is not the base64 of a .p12" >&2
		exit 1
	fi
	if out=$(security import "$2" -k "$keychain" \
		-P "${QUILL_SIGN_PASSWORD:-}" \
		-T /usr/bin/productsign -T /usr/bin/security 2>&1); then
		echo "macos: $1: $size bytes, sha256 $digest;" \
			"${out:-security import printed nothing}"
	else
		echo "macos: $1: $size bytes, sha256 $digest;" \
			"security import failed: ${out:-no output}" >&2
		case ${out:-} in
		*"MAC verification failed"*)
			# Two very different faults share this one message, and the second
			# is the one nobody guesses: `security import` reads only the
			# legacy PKCS#12 shape, and an OpenSSL 3 export -- PBES2,
			# AES-256-CBC, SHA-256 MAC, which is its default -- fails the MAC
			# check here whatever the password.
			echo "macos: either QUILL_SIGN_PASSWORD is not the password $1 was" \
				"exported with -- one password opens both P12s -- or $1 is an" \
				"OpenSSL 3 style PKCS#12, which this importer cannot read whatever" \
				"the password. Check with:" >&2
			echo "    openssl pkcs12 -info -noout -in <the p12> -passin pass:<password>" >&2
			echo "  \"MAC: sha256\" or \"AES-256-CBC\" there is the second fault; re-export it" \
				"as \"openssl pkcs12 -export -legacy\", which Apple's importer accepts." >&2
			;;
		esac
		exit 1
	fi
}

# The Installer identity, into a keychain of its own that the trap removes.
# QUILL_INSTALLER_P12 is the field that holds it; QUILL_SIGN_P12 is read as
# well only because one P12 may carry both identities, which is the setup
# this repository's 1Password item had before the Installer field existed.
sign_inst=
if [ -n "${QUILL_INSTALLER_P12:-}" ] || [ -n "${QUILL_SIGN_P12:-}" ]; then
	keychain=$work/sign.keychain-db
	kcpass=$(head -c 24 /dev/urandom | base64)
	security create-keychain -p "$kcpass" "$keychain"
	security set-keychain-settings "$keychain"
	security unlock-keychain -p "$kcpass" "$keychain"
	if [ -n "${QUILL_INSTALLER_P12:-}" ]; then
		materialise "$QUILL_INSTALLER_P12" "$work/installer.p12"
		import_p12 QUILL_INSTALLER_P12 "$work/installer.p12"
	fi
	if [ -n "${QUILL_SIGN_P12:-}" ]; then
		materialise "$QUILL_SIGN_P12" "$work/sign.p12"
		import_p12 QUILL_SIGN_P12 "$work/sign.p12"
	fi

	# What lets productsign use the key without a dialog nobody is there to
	# click. Not fatal on its own, because the way it fails is a symptom and
	# not the disease: a keychain with no private key in it fails this with
	# "The specified item could not be found in the keychain", which says far
	# less than the identity check just below does. Let that speak.
	if ! keypart=$(security set-key-partition-list -S apple-tool:,apple: -s \
		-k "$kcpass" "$keychain" 2>&1); then
		echo "macos: set-key-partition-list: ${keypart:-no output}" >&2
	fi

	sign_inst=$(security find-identity -v "$keychain" |
		sed -n 's/.*"\(Developer ID Installer: [^"]*\)".*/\1/p' | head -1)
	if [ -z "$sign_inst" ]; then
		echo "macos: no valid Developer ID Installer identity in QUILL_INSTALLER_P12" \
			"or QUILL_SIGN_P12; Gatekeeper would refuse the package, so it is not built" >&2
		dump_keychain
		exit 1
	fi

	# A signed wrapper around an unsigned binary is the one combination that
	# looks fine here and fails on the user's Mac: the notary service rejects
	# a payload whose Mach-O carries no Developer ID signature, and a package
	# that skipped notarization gets a Gatekeeper block on first open.
	if [ "$signed_payload" != yes ]; then
		echo "macos: signing material is present but the client binary is not signed;" \
			"the notary service rejects that payload. Check that quill ran." >&2
		exit 1
	fi
fi

# The component package, then the product around it with the panes and the
# refusals from Distribution.xml.
pkgbuild --quiet --root "$root" --identifier "$PKG_IDENTIFIER" \
	--version "$pkgver" --install-location / --ownership recommended \
	"$work/component.pkg"

res=$work/resources
mkdir -p "$res"
cp "$stage/LICENSE" "$res/LICENSE.txt"
fill() {
	sed -e "s|@VERSION@|$version|g" -e "s|@PKGVER@|$pkgver|g" \
		-e "s|@HOSTARCH@|$hostarch|g" -e "s|@ARCHNAME@|$archname|g" \
		-e "s|@MINVER@|$minver|g" -e "s|@IDENTIFIER@|$PKG_IDENTIFIER|g" \
		"$1" >"$2"
}
fill "$here/welcome.html" "$res/welcome.html"
fill "$here/readme.html" "$res/readme.html"
fill "$here/Distribution.xml" "$work/Distribution.xml"
productbuild --quiet --distribution "$work/Distribution.xml" \
	--resources "$res" --package-path "$work" "$work/unsigned.pkg"

if [ -n "$sign_inst" ]; then
	productsign --timestamp --keychain "$keychain" --sign "$sign_inst" \
		"$work/unsigned.pkg" "$out" >/dev/null
	pkgutil --check-signature "$out"
	echo "macos: package signed by $sign_inst"
else
	mv "$work/unsigned.pkg" "$out"
	echo "macos: no signing material; the package is unsigned and Gatekeeper will refuse it"
fi

# Notarization, then the ticket stapled on so a Mac with no network can still
# check it. Only for a signed package: the service rejects anything else, so
# with no signature there is nothing to submit.
if [ -n "$sign_inst" ] && [ -n "${QUILL_NOTARY_KEY:-}" ]; then
	materialise "$QUILL_NOTARY_KEY" "$work/notary.p8"
	xcrun notarytool submit "$out" \
		--key "$work/notary.p8" \
		--key-id "${QUILL_NOTARY_KEY_ID:?QUILL_NOTARY_KEY_ID is not set}" \
		--issuer "${QUILL_NOTARY_ISSUER:?QUILL_NOTARY_ISSUER is not set}" \
		--wait --timeout 30m | tee "$work/notary.log"
	if ! grep -q '^ *status: Accepted' "$work/notary.log"; then
		id=$(sed -n 's/^ *id: //p' "$work/notary.log" | head -1)
		echo "macos: notarization did not end in Accepted; the service's log:" >&2
		[ -z "$id" ] || xcrun notarytool log "$id" \
			--key "$work/notary.p8" --key-id "$QUILL_NOTARY_KEY_ID" \
			--issuer "$QUILL_NOTARY_ISSUER" >&2 || true
		exit 1
	fi
	xcrun stapler staple "$out"
	# What Gatekeeper will say when someone double-clicks it.
	spctl --assess --type install --verbose=2 "$out"
	echo "macos: notarized and stapled"
elif [ -n "$sign_inst" ]; then
	echo "macos: QUILL_NOTARY_KEY is not set; the package is signed but not notarized"
fi

echo "macos: $out"
pkgutil --payload-files "$out" | sed 's/^/  /'
