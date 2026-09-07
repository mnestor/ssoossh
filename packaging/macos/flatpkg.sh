#!/usr/bin/env bash
#
# Build, sign and notarize one macOS installer package for the client,
# without any of Apple's tools.
#
#   packaging/macos/flatpkg.sh --binary dist/macos-build_darwin_arm64/ssoossh \
#       --version 1.2.3 --goarch arm64 [--outdir dist]
#
# Successor to packaging/macos/pkg.sh, which needed pkgbuild, productbuild,
# productsign and notarytool and therefore needed a macOS runner of its own,
# a second secrets load, an artifact round-trip for the darwin archives, and
# a hand-written pass over the release's checksums file afterwards. None of
# that is inherent to the format: a flat package is a xar archive with a
# fixed layout, and every part of it can be written on Linux.
#
#   Distribution                 the installer-gui-script, from the template
#                                beside this script
#   Resources/en.lproj/          welcome.html, readme.html, LICENSE.txt
#   component.pkg/PackageInfo    what pkgbuild would have written
#   component.pkg/Bom            bomutils' mkbom
#   component.pkg/Payload        gzip'd cpio, odc format
#
# Signing and notarization are rcodesign
# (github.com/indygreg/apple-platform-rs), which signs a xar and drives the
# notary service from any OS. That is also what deletes the keychain
# handling the old script carried: rcodesign parses a PKCS#12 itself, so
# there is no temporary keychain, no `security import`, no
# set-key-partition-list, and no OpenSSL-3-versus-legacy P12 trap.
#
# Nothing is compiled here and the binary is copied in untouched. quill
# signed and notarized it as a goreleaser build post-hook immediately before
# this runs; re-signing it would replace a notarized Developer ID signature
# with a fresh one that has no notarization behind it.
#
# One package per architecture, matching every other artifact in the
# release. The two darwin binaries are not lipo'd into a universal one: the
# merge rewrites the Mach-O, which discards both quill signatures and would
# make this script the only thing that ever signed the shipped client.
#
# Needs mkbom (bomutils), xar, bsdcpio (libarchive-tools), gzip, python3 and
# rcodesign. bsdcpio specifically, NOT GNU cpio: see the payload step below.
# Linux is the supported platform; CI builds the package here and verifies it
# on a Mac, which is the only place Gatekeeper can actually be asked.
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
# about when /etc changes. The defaults go to /usr/local/share/ssoossh
# instead, as a file to copy from -- the readme pane says so, and the client
# needs no configuration file to run, since the same defaults are compiled
# into the binary (client/config, //go:embed defaults.yaml).
#
# Unlike the old script the payload is assembled from the working tree
# rather than unpacked from the release archive, because a goreleaser build
# hook runs before any archive exists. Same files either way:
# THIRD-PARTY-LICENSES.md is written by the before-hook
# (scripts/gen-third-party-licenses.sh) and the man pages are committed.
#
# Signing and notarization are by environment and off when the variables are
# empty -- a local run gets an unsigned package and the log says so. The
# names are the ones .github/workflows/build.yaml already reads from
# 1Password for quill, so the same item serves both:
#
#   QUILL_INSTALLER_P12   PKCS#12 holding the "Developer ID Installer"
#                         identity, base64. The identity, not the
#                         certificate alone: an export without the private
#                         key is rejected here rather than signing nothing
#   QUILL_SIGN_P12        the "Developer ID Application" P12 quill uses,
#                         base64. Read only as a fallback, for the case
#                         where that one file holds both identities
#   QUILL_SIGN_PASSWORD   the password of whichever file is used
#   QUILL_NOTARY_ISSUER   App Store Connect API issuer id
#   QUILL_NOTARY_KEY_ID   App Store Connect API key id
#   QUILL_NOTARY_KEY      that key, PEM or the PEM's base64
#
# Either P12 shape is accepted now. `openssl pkcs12 -export -legacy` and an
# OpenSSL 3 default export both work, because nothing here goes through
# Apple's importer any more.
#
# A Developer ID *Application* certificate cannot sign an installer package;
# Gatekeeper refuses a downloaded package that is not Installer-signed. So
# once any signing material is present the Installer identity is not
# optional: its absence fails here, loudly, rather than producing a package
# every Mac would reject.
set -euo pipefail

binary= version= goarch= outdir=
while [ $# -gt 0 ]; do
	case $1 in
	--binary) binary=$2 ; shift 2 ;;
	--version) version=$2 ; shift 2 ;;
	--goarch) goarch=$2 ; shift 2 ;;
	--outdir) outdir=$2 ; shift 2 ;;
	*) echo "macos: unknown argument '$1'" >&2 ; exit 1 ;;
	esac
done
: "${binary:?--binary is required}"
: "${version:?--version is required}"
: "${goarch:?--goarch is required}"
outdir=${outdir:-$(dirname "$binary")}

PKG_IDENTIFIER=${PKG_IDENTIFIER:-org.mikenestor.ssoossh-client}
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)

# Named before anything is built, so a missing tool is one line rather than
# a failure three stages in that reads like a corrupt archive.
for tool in mkbom xar bsdcpio gzip python3; do
	command -v "$tool" >/dev/null ||
		{ echo "macos: $tool is not installed" >&2 ; exit 1 ; }
done

work=$(mktemp -d "${TMPDIR:-/tmp}/ssoossh-flatpkg.XXXXXX")
trap 'rm -rf "$work"' EXIT

# What Distribution.flat.xml's hostArchitectures wants, which is the Mach-O
# spelling rather than Go's. An Intel Mac handed the arm64 package is then
# told so by the installer, instead of by a binary that will not exec.
case $goarch in
amd64) hostarch=x86_64 ; archname="Intel" ;;
arm64) hostarch=arm64  ; archname="Apple silicon" ;;
*) echo "macos: unknown darwin architecture '$goarch'" >&2 ; exit 1 ;;
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
	pkgver="${version%%-*}-$(printf '%s' "${version#*-}" | tr -- '-' '.')" ;;
[0-9]*.[0-9]*.[0-9]*)
	pkgver=$version ;;
*)
	pkgver="0.0.0-$(printf 'dev.%s' "$version" | tr -- '-' '.')" ;;
esac

name="ssoossh-client_${version}_darwin_${goarch}"
mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)
out=$outdir/$name.pkg

# The two facts about the client binary this script needs, read straight out
# of the Mach-O because neither otool nor codesign exists off a Mac.
#
#   minos    the deployment target the Go linker stamped, which becomes the
#            installer's floor. macOS refuses to exec a binary whose minos
#            is above the running system, so this is the same number the
#            user would otherwise meet as a launch failure
#   sigkind  none, adhoc or signed, from the code directory's CS_ADHOC
#            flag. Presence of an LC_CODE_SIGNATURE is not the question:
#            the Go linker ad-hoc signs every darwin/arm64 binary it
#            produces, so "has a signature" is true on arm64 whether quill
#            ran or not, and false on amd64 for the same build. Only the
#            flag separates the two. Not a verification -- that needs
#            Apple's tooling and happens in the macOS job -- but enough to
#            catch the one combination that looks fine here and fails on
#            the user's Mac: a signed wrapper around an unsigned payload,
#            which the notary service rejects
cat >"$work/machoinfo.py" <<'PY'
import struct, sys

LC_CODE_SIGNATURE = 0x1D
LC_VERSION_MIN_MACOSX = 0x24
LC_BUILD_VERSION = 0x32
CSMAGIC_EMBEDDED_SIGNATURE = 0xFADE0CC0
CSMAGIC_CODEDIRECTORY = 0xFADE0C02
CS_ADHOC = 0x0000_0002

data = open(sys.argv[1], "rb").read()
if data[:4] != b"\xcf\xfa\xed\xfe":
    sys.exit("not a thin little-endian 64-bit Mach-O")

ncmds = struct.unpack_from("<I", data, 16)[0]
off, minos, sigoff = 32, None, None
for _ in range(ncmds):
    cmd, cmdsize = struct.unpack_from("<II", data, off)
    if cmd == LC_CODE_SIGNATURE:
        sigoff = struct.unpack_from("<I", data, off + 8)[0]
    elif cmd == LC_BUILD_VERSION:
        minos = struct.unpack_from("<I", data, off + 12)[0]
    elif cmd == LC_VERSION_MIN_MACOSX and minos is None:
        minos = struct.unpack_from("<I", data, off + 8)[0]
    off += cmdsize

if minos is None:
    sys.exit("no LC_BUILD_VERSION or LC_VERSION_MIN_MACOSX")
x, y, z = minos >> 16, (minos >> 8) & 0xFF, minos & 0xFF
print("%d.%d.%d" % (x, y, z) if z else "%d.%d" % (x, y))

def signature_kind(data, sigoff):
    if sigoff is None:
        return "none"
    magic, _, count = struct.unpack_from(">III", data, sigoff)
    if magic != CSMAGIC_EMBEDDED_SIGNATURE:
        return "none"
    for i in range(count):
        _, bloboff = struct.unpack_from(">II", data, sigoff + 12 + i * 8)
        blob = sigoff + bloboff
        if struct.unpack_from(">I", data, blob)[0] == CSMAGIC_CODEDIRECTORY:
            flags = struct.unpack_from(">I", data, blob + 12)[0]
            return "adhoc" if flags & CS_ADHOC else "signed"
    return "none"

print(signature_kind(data, sigoff))
PY

minver=${PKG_MIN_MACOS:-}
if ! machoinfo=$(python3 "$work/machoinfo.py" "$binary"); then
	echo "macos: cannot read $binary: $machoinfo" >&2
	exit 1
fi
[ -n "$minver" ] || minver=$(printf '%s\n' "$machoinfo" | sed -n 1p)
sigkind=$(printf '%s\n' "$machoinfo" | sed -n 2p)
echo "macos: $binary targets macOS $minver, code signature: $sigkind"

# The payload, laid out as it will land on the host.
root=$work/root
doc=$root/usr/local/share/doc/ssoossh-client
install -d "$root/usr/local/bin" \
	"$root/usr/local/share/man/man1" "$root/usr/local/share/man/man5" \
	"$root/usr/local/share/ssoossh" "$doc"
install -m 0755 "$binary" "$root/usr/local/bin/ssoossh"
# ssoossh*.1 is safe where ssoossh*.5 would not be: every ssoosshd page name
# also starts with "ssoossh", so the .5 is named explicitly and this package
# takes only its own -- the same split .goreleaser.yml's nfpms block makes,
# and for the same reason.
install -m 0644 "$repo"/docs/man/ssoossh*.1 "$root/usr/local/share/man/man1/"
install -m 0644 "$repo/docs/man/ssoossh.yaml.5" "$root/usr/local/share/man/man5/"
install -m 0644 "$repo/client/config/defaults.yaml" \
	"$root/usr/local/share/ssoossh/ssoossh.yaml"
install -m 0644 "$repo/LICENSE" "$repo/NOTICE" \
	"$repo/THIRD-PARTY-LICENSES.md" "$doc/"

# The component package: what pkgbuild used to emit, written by hand.
#
# Ownership is stamped into the archive and the BOM rather than taken from
# the staging directory, which belongs to whichever uid the runner uses.
# root:wheel is what `pkgbuild --ownership recommended` chose for these
# paths, and the macOS verify job diffs this BOM against a pkgbuild one to
# keep that true.
comp=$work/flat/component.pkg
install -d "$comp"
# bsdcpio and not GNU cpio, which is not interchangeable here: GNU cpio
# rewrites "./usr/local/bin/ssoossh" to "usr/local/bin/ssoossh" as it
# archives, stripping the leading "./" that Apple's payloads carry and that
# mkbom records in the BOM. The two then disagree about every path in the
# package. Apple's notary service reports that as "The contents of the
# package could not be extracted", finds no Mach-O to look at, and rejects
# the submission with "has no signed executables or bundles" -- a message
# that says nothing about paths. bsdcpio stores the names verbatim.
( cd "$root" && find . | bsdcpio -o -H odc --owner 0:0 --quiet | gzip -c ) \
	>"$comp/Payload"
mkbom -u 0 -g 0 "$root" "$comp/Bom"

# installKBytes the way pkgbuild counts it: each file rounded up to a whole
# kilobyte, directories not counted. `du -sk` overstates it by the
# filesystem's block padding -- 14364 against pkgbuild's 14266 for the same
# payload -- and this field is what the installer checks free space against.
installkb=$(find "$root" -type f -printf '%s\n' |
	awk '{s += int(($1 + 1023) / 1024)} END {print s + 0}')
nfiles=$(find "$root" | wc -l | tr -d ' ')

# Matched field for field against a package productbuild built from this same
# payload (ssoossh-client 1.2.0). The empty elements are not decoration:
# pkgbuild writes them, and the reference package carries them, so they stay
# until something is shown to ignore them.
cat >"$comp/PackageInfo" <<PKGINFO
<?xml version="1.0" encoding="utf-8"?>
<pkg-info overwrite-permissions="true" relocatable="false"
          identifier="$PKG_IDENTIFIER" postinstall-action="none"
          version="$pkgver" format-version="2" install-location="/" auth="root">
    <payload numberOfFiles="$nfiles" installKBytes="$installkb"/>
    <bundle-version/>
    <upgrade-bundle/>
    <update-bundle/>
    <atomic-update-bundle/>
    <strict-identifier/>
    <relocate/>
</pkg-info>
PKGINFO

# The product around it: the panes, and the refusals from the template.
# Top level, not Resources/en.lproj: the reference package productbuild
# built from this same Distribution puts welcome.html, readme.html and
# LICENSE.txt directly in Resources/, and the <welcome file="..."/>
# references resolve from there.
res=$work/flat/Resources
install -d "$res"
cp "$repo/LICENSE" "$res/LICENSE.txt"
fill() {
	sed -e "s|@VERSION@|$version|g" -e "s|@PKGVER@|$pkgver|g" \
		-e "s|@HOSTARCH@|$hostarch|g" -e "s|@ARCHNAME@|$archname|g" \
		-e "s|@MINVER@|$minver|g" -e "s|@IDENTIFIER@|$PKG_IDENTIFIER|g" \
		-e "s|@INSTALLKB@|$installkb|g" \
		"$1" >"$2"
}
fill "$here/welcome.html" "$res/welcome.html"
fill "$here/readme.html" "$res/readme.html"
fill "$here/Distribution.flat.xml" "$work/flat/Distribution"

# The filled Distribution, parsed before it is packed. Apple's notary service
# parses this file to enumerate the component packages, and an ill formed one
# leaves it with none: it reports that as "the contents of the package could
# not be extracted" and then "has no signed executables or bundles", a pair of
# messages that say nothing about XML and send you looking at the payload.
# The way to write a broken one is not exotic -- a "--" in a comment, which
# XML forbids and which this repository's prose style otherwise puts
# everywhere -- so it is checked here, where the error names the line.
if ! xmlerr=$(python3 -c "
import sys, xml.dom.minidom
try:
    xml.dom.minidom.parse(sys.argv[1])
except Exception as err:
    sys.exit(str(err))
" "$work/flat/Distribution" 2>&1); then
	echo "macos: the filled Distribution is not well formed XML: $xmlerr" >&2
	echo "macos: Apple would reject the package for this without naming XML." >&2
	exit 1
fi

# Member order copied from the reference package: the component first, then
# Resources, then Distribution last.
( cd "$work/flat" && xar --compression none -cf "$work/unsigned.pkg" \
	component.pkg Resources Distribution )

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

# What the P12 actually holds, checked before rcodesign is handed it. Two
# faults are worth separating here, because neither says so downstream: a
# certificate exported without its private key signs nothing, and a
# Developer ID *Application* certificate produces a package Gatekeeper
# refuses. openssl reads both P12 shapes -- a modern export directly, a
# legacy one with -legacy -- and rcodesign accepts either, so this check
# costs nothing that the old keychain import used to cost.
p12_subject() {
	local p12=$1 passfile=$2 args
	for args in "" "-legacy"; do
		# shellcheck disable=SC2086
		if openssl pkcs12 -in "$p12" -passin "file:$passfile" -nokeys -clcerts \
			$args 2>/dev/null | openssl x509 -noout -subject 2>/dev/null; then
			return 0
		fi
	done
	return 1
}
p12_has_key() {
	local p12=$1 passfile=$2 args
	for args in "" "-legacy"; do
		# shellcheck disable=SC2086
		if openssl pkcs12 -in "$p12" -passin "file:$passfile" -nocerts -noout \
			$args 2>/dev/null; then
			return 0
		fi
	done
	return 1
}

p12=
if [ -n "${QUILL_INSTALLER_P12:-}" ]; then
	materialise "$QUILL_INSTALLER_P12" "$work/installer.p12"
	p12=$work/installer.p12
	p12_var=QUILL_INSTALLER_P12
elif [ -n "${QUILL_SIGN_P12:-}" ]; then
	# Only because one P12 may carry both identities, which is the setup this
	# repository's 1Password item had before the Installer field existed.
	materialise "$QUILL_SIGN_P12" "$work/sign.p12"
	p12=$work/sign.p12
	p12_var=QUILL_SIGN_P12
fi

if [ -n "$p12" ]; then
	( umask 077 && printf '%s' "${QUILL_SIGN_PASSWORD:-}" >"$work/p12.pass" )

	if ! subject=$(p12_subject "$p12" "$work/p12.pass"); then
		echo "macos: cannot read $p12_var with QUILL_SIGN_PASSWORD; it is either" \
			"the wrong password or the field does not hold a PKCS#12" >&2
		exit 1
	fi
	if ! p12_has_key "$p12" "$work/p12.pass"; then
		echo "macos: $p12_var holds a certificate but no private key;" \
			"the export left the key behind and it would sign nothing" >&2
		exit 1
	fi
	case $subject in
	*"Developer ID Installer"*) ;;
	*)
		echo "macos: $p12_var is not a Developer ID Installer certificate," \
			"so Gatekeeper would refuse the package. It holds: $subject" >&2
		exit 1
		;;
	esac
	echo "macos: signing with $subject"

	# A signed wrapper around an unsigned binary is the one combination that
	# looks fine here and fails on the user's Mac: the notary service rejects
	# a payload whose Mach-O carries no Developer ID signature, and a package
	# that skipped notarization gets a Gatekeeper block on first open.
	if [ "$sigkind" != signed ]; then
		echo "macos: signing material is present but the client binary's signature" \
			"is '$sigkind', not a real one; the notary service rejects that" \
			"payload. Check that quill ran -- note that an unsigned darwin/arm64" \
			"binary still reports 'adhoc', because the Go linker signs it that" \
			"way, so 'adhoc' here means quill did not run rather than that it" \
			"half-ran." >&2
		exit 1
	fi

	# rcodesign timestamps against Apple's server by default, so there is no
	# flag here matching the old productsign --timestamp.
	rcodesign sign --p12-file "$p12" --p12-password-file "$work/p12.pass" \
		"$work/unsigned.pkg" "$out"
	echo "macos: package signed"
else
	mv "$work/unsigned.pkg" "$out"
	echo "macos: no signing material; the package is unsigned and Gatekeeper will refuse it"
fi

# Notarization, then the ticket stapled on so a Mac with no network can still
# check it. Only for a signed package: the service rejects anything else, so
# with no signature there is nothing to submit. rcodesign writes the ticket
# into the xar's notarization trailer, which is what `stapler staple` used
# to do.
if [ -n "$p12" ] && [ -n "${QUILL_NOTARY_KEY:-}" ]; then
	materialise "$QUILL_NOTARY_KEY" "$work/notary.p8"
	( umask 077 && rcodesign encode-app-store-connect-api-key \
		-o "$work/asc.json" \
		"${QUILL_NOTARY_ISSUER:?QUILL_NOTARY_ISSUER is not set}" \
		"${QUILL_NOTARY_KEY_ID:?QUILL_NOTARY_KEY_ID is not set}" \
		"$work/notary.p8" )
	rcodesign notary-submit --api-key-file "$work/asc.json" --staple "$out"
	echo "macos: notarized and stapled"
elif [ -n "$p12" ]; then
	echo "macos: QUILL_NOTARY_KEY is not set; the package is signed but not notarized"
fi

echo "macos: $out"
