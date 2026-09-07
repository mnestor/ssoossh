#!/usr/bin/env bash
#
# Build one Windows Installer package for the client, on Linux, without any
# of Microsoft's tools.
#
#   packaging/windows/msi.sh --binary dist/windows-build_windows_amd64_v1/ssoossh.exe \
#       --version 1.2.3 --goarch amd64 [--outdir dist]
#
# Same reasoning as packaging/macos/flatpkg.sh, and the same place in the
# pipeline: a goreleaser build post-hook, so the package exists in dist/
# while the checksum pipe is still globbing for it. An `after:` hook would
# run past that point.
#
# The tool is wixl (msitools), which reads WiX v3 source and writes the MSI
# itself. It is a package in Debian and Ubuntu, needs no wine, no .NET and
# no Windows SDK, and is baked into .github/docker/Dockerfile.runner.
#
# An MSI rather than an NSIS or Inno .exe because the audience is the
# administrator, not the person at the keyboard: msiexec /qn, GPO software
# installation, Intune and ConfigMgr all take an MSI directly, and none of
# them take a self-extracting installer without a wrapper. It is also the
# only format with a real uninstall and upgrade story that Add/Remove
# Programs understands.
#
# THE PACKAGE IS NOT SIGNED. That is a deliberate, temporary state, not an
# oversight -- see the "Signing" section below and the entry in
# docs/proposals/enhancements.md.
#
# Signing
# -------
# The decision is Azure Artifact Signing (formerly Trusted Signing) driven
# by jsign, and it is not implemented yet. When it lands it is one step in
# this script and one more in the goreleaser hook ahead of it, changing
# nothing else:
#
#   jsign --storetype TRUSTEDSIGNING \
#         --keystore "$AZURE_SIGNING_ENDPOINT" \
#         --storepass "$AZURE_ACCESS_TOKEN" \
#         --alias "<account>/<certificate-profile>" \
#         "$binary" "$out"
#
# The exe is signed before the MSI is built (an MSI embeds a copy of the
# payload, so signing the exe afterwards leaves the installed file
# unsigned), and the MSI is signed after. Exactly the quill-then-flatpkg
# ordering on the macOS side, for exactly the same reason.
#
# Why that combination, recorded here so it is not relitigated:
#
#   - Every signing integration Microsoft documents for the service is
#     Windows-bound (SignTool plus their dlib, the Azure DevOps task, the
#     GitHub Action, PowerShell). trusted-signing-cli is a SignTool
#     wrapper and needs the Windows SDK; dotnet/sign is Windows-only and
#     speaks to Key Vault, not this service. jsign is the only client that
#     runs on Linux, and it signs both PE and MSI.
#   - osslsigncode, which would avoid the JVM, cannot reach the service:
#     it needs PKCS#11, and the service exposes a REST digest-signing API
#     instead. Reaching for osslsigncode means buying an OV certificate on
#     a cloud HSM (DigiCert KeyLocker, SSL.com eSigner) for more money.
#   - EV buys nothing: SmartScreen stopped treating it as an instant
#     bypass in 2024.
#
# Until then the release ships an unsigned MSI, which still installs
# silently through every machine-managed channel because those run as
# SYSTEM and check no publisher. What it does not survive is a WDAC policy
# in enforcement, and a user double-clicking it gets SmartScreen and an
# "Unknown Publisher" prompt.
#
# amd64 only
# ----------
# wixl 0.106 accepts X86, INTEL, IA64, INTEL64 and X64, and rejects arm64
# outright, so there is no arm64 package to build. windows/arm64 keeps the
# zip archive, and the x64 package remains installable there under
# emulation. This script exits quietly for any other architecture so the
# goreleaser hook, which runs once per build target, does not fail the
# whole build on the one it cannot serve.
set -euo pipefail

binary= version= goarch= outdir=
while [ $# -gt 0 ]; do
	case $1 in
	--binary) binary=$2 ; shift 2 ;;
	--version) version=$2 ; shift 2 ;;
	--goarch) goarch=$2 ; shift 2 ;;
	--outdir) outdir=$2 ; shift 2 ;;
	*) echo "windows: unknown argument '$1'" >&2 ; exit 1 ;;
	esac
done
: "${binary:?--binary is required}"
: "${version:?--version is required}"
: "${goarch:?--goarch is required}"
outdir=${outdir:-$(dirname "$binary")}

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)

# Not an error: see "amd64 only" above.
if [ "$goarch" != "amd64" ]; then
	echo "windows: no MSI for windows/$goarch (wixl cannot target it); zip only"
	exit 0
fi

# Named before anything is staged, so a missing tool is one line rather
# than a wixl error about a file it never got to read.
for tool in wixl; do
	command -v "$tool" >/dev/null ||
		{ echo "windows: $tool is not installed" >&2 ; exit 1 ; }
done

# ProductVersion is three numeric fields, capped at 255.255.65535, and MSI
# has no concept of a pre-release. So the suffix goreleaser puts on an rc
# or a snapshot is dropped rather than encoded:
#
#   1.2.3                    -> 1.2.3
#   1.2.3-rc1                -> 1.2.3
#   1.2.4-SNAPSHOT-abc1234   -> 1.2.4
#   anything else            -> 0.0.0
#
# The consequence is that Windows sees 1.2.3-rc1 and 1.2.3 as the same
# version, so installing the release over its own release candidate is a
# no-op rather than an upgrade. Accepted: an rc is a thing you install
# deliberately on a machine you are testing on, and `msiexec /i` with
# REINSTALL=ALL REINSTALLMODE=vamus still forces it. The file name carries
# the full version either way, so nothing is ambiguous about which package
# is in hand.
msiver=$(printf '%s' "${version%%-*}" | awk -F. '
	/^[0-9]+(\.[0-9]+)*$/ {
		major = $1 + 0; minor = ($2 == "" ? 0 : $2 + 0); build = ($3 == "" ? 0 : $3 + 0)
		if (major <= 255 && minor <= 255 && build <= 65535) {
			printf "%d.%d.%d", major, minor, build
			exit
		}
	}
	{ print "0.0.0" }
')

name="ssoossh-client_${version}_windows_${goarch}"
mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)
out=$outdir/$name.msi

work=$(mktemp -d "${TMPDIR:-/tmp}/ssoossh-msi.XXXXXX")
trap 'rm -rf "$work"' EXIT
payload=$work/payload
mkdir -p "$payload"

# Assembled from the working tree, not from the release archive, because a
# build hook runs before any archive exists. The same files the windows zip
# gets, minus the man pages. THIRD-PARTY-LICENSES.md is written by the
# goreleaser before-hook (scripts/gen-third-party-licenses.sh); it is
# required here rather than optional, so a package can never ship claiming
# a licence set it does not carry.
cp "$binary" "$payload/ssoossh.exe"
for f in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
	[ -f "$repo/$f" ] || { echo "windows: $f is missing from the working tree" >&2 ; exit 1 ; }
	cp "$repo/$f" "$payload/$f"
done
cp "$repo/client/config/defaults.yaml" "$payload/defaults.yaml"

wixl -a x64 \
	-D Version="$msiver" \
	-D PayloadDir="$payload" \
	-o "$out" \
	"$here/ssoossh.wxs"

echo "windows: built $out (ProductVersion $msiver, UNSIGNED)"
