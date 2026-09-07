# packaging/

Packaging that goreleaser does not have a pipe for.

Almost everything a release ships is described in `.goreleaser.yml`: the
archives, the `.deb`/`.rpm`/`.apk` through nfpm, and the container images
through `dockers_v2`. The macOS installer is the exception, but only in the
sense that goreleaser has no pipe that builds one. It is still built,
signed, notarized and published by the same goreleaser run as everything
else, from a `macos-build` post-hook.

| Path | What it builds | Runs where |
| --- | --- | --- |
| `macos/flatpkg.sh` | the macOS client installer, `ssoossh-client_<version>_darwin_<arch>.pkg` | anywhere, Linux included |

## Why a script rather than a pipe

The OSS build of goreleaser has no installer-package pipe. That is the whole
reason this file exists; it is not because the work needs a Mac.

A flat package is a xar archive with a fixed layout, and every part of it can
be written with open tools:

| Part | Written by | Apple's equivalent |
| --- | --- | --- |
| `component.pkg/Payload` | `bsdcpio` + `gzip` | `pkgbuild` |
| `component.pkg/Bom` | `mkbom` (bomutils) | `pkgbuild` |
| `component.pkg/PackageInfo` | the script | `pkgbuild` |
| `Distribution`, `Resources/` | the script, from `Distribution.flat.xml` | `productbuild` |
| the archive | `xar` | `productbuild` |
| signature, notarization, stapling | `rcodesign` | `productsign`, `notarytool`, `stapler` |

Nothing is compiled. The binary is the one goreleaser built and
`quill sign-and-notarize` signed, copied into the payload untouched:
re-signing it would replace a notarized Developer ID signature with a fresh
one that has no notarization behind it.

## What is checked, and where

Two things verify the result, and they catch different faults.

**Apple's notary service**, during the build. It extracts the package and
looks for a signed Mach-O inside, so a package it cannot open fails the
build and no release happens. This is a stronger check than it sounds: it
is what catches a malformed `Distribution`, a payload it cannot unpack, or
a binary quill did not sign.

**`macos-verify`**, a job on a real Mac, for the questions Apple does not
ask: whether the BOM `mkbom` wrote matches the one `pkgbuild` writes from
the same payload, whether `spctl` accepts the package, whether it installs,
and whether `hostArchitectures` still makes the Intel package decline on
Apple silicon. goreleaser writes the release as a draft and tags the images
with the commit sha only; the `publish` job flips the draft and promotes the
images to their version tags, and runs only if this job passed. A draft can
be deleted in silence, and a sha tag never claimed to be a release; a
published release has already emailed every watcher.

## Two things that will bite you

**Use `bsdcpio`, never GNU `cpio`.** GNU cpio rewrites `./usr/local/bin/x`
to `usr/local/bin/x` as it archives, stripping the leading `./` that Apple's
payloads carry and that `mkbom` records in the BOM. The payload and the BOM
then disagree about every path in the package.

**No `--` anywhere in `Distribution.flat.xml`.** XML forbids a double hyphen
inside a comment, and this repository's prose style otherwise puts them
everywhere. Apple parses `Distribution` to find the component packages; an
ill formed one leaves it with none, which it reports as "the contents of the
package could not be extracted" and "has no signed executables or bundles",
neither of which mentions XML. `flatpkg.sh` parses the filled file before
packing so this cannot reach Apple, and `macos-verify` re-checks the shipped
artifact.

## Why this directory rather than scripts/

`scripts/` holds shell that runs on its own. These have data beside them,
`Distribution.flat.xml` and the installer's HTML panes, and the script only
makes sense next to them.

## Running it locally

`goreleaser release --snapshot --clean` builds the packages on its own now,
because the post-hook runs in the build phase, which `--snapshot` does not
skip. To package a single binary directly:

```sh
packaging/macos/flatpkg.sh \
  --binary dist/macos-build_darwin_arm64/ssoossh \
  --version 1.2.3 --goarch arm64 --outdir dist
```

You need `mkbom`, `xar`, `bsdcpio`, `gzip`, `python3` and `rcodesign`. The
CI image installs them (`.github/docker/Dockerfile.runner`); `xar` and
`libarchive-tools` are packaged, `bomutils` is a source build pinned to a
commit because upstream has not tagged since 2014, and `rcodesign` is a
release tarball. On a Mac, `brew install bomutils xar` covers the first two.

With no signing material in the environment the package is built unsigned
and the log says so, which is fine for checking the payload and the panes
and useless for anything else: Gatekeeper refuses a downloaded package that
is not Developer ID Installer signed. The script's header lists the
variables and where CI reads them from.
