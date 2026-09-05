# packaging/

Packaging that goreleaser does not do itself.

Almost everything a release ships is described in `.goreleaser.yml`: the
archives, the `.deb`/`.rpm`/`.apk` through nfpm, and the container images
through `dockers_v2`. What is here is the exception.

| Path | What it builds | Runs where |
| --- | --- | --- |
| `macos/pkg.sh` | the macOS client installer, `ssoossh-client_<version>_darwin_<arch>.pkg` | macOS only |

## Why the macOS package is not in .goreleaser.yml

The OSS build of goreleaser has no installer-package pipe, and the tools an
installer needs -- `pkgbuild`, `productbuild`, `productsign`, `notarytool`,
`stapler` -- exist only on macOS, while the release job runs in a Linux
container. So the package is built in a second job on a `macos` runner,
from the darwin client archives the first job produced, and uploaded to the
release beside them (`.github/workflows/build.yaml`).

Nothing is compiled there. The binary inside the archive is the one
goreleaser built and `quill sign-and-notarize` signed as a build post-hook,
and it is copied into the payload untouched.

## Why this directory rather than scripts/

`scripts/` holds shell that runs on its own. These have data beside them --
`Distribution.xml` and the installer's HTML panes -- and the script only
makes sense next to them.

## Running it locally

You need a Mac and a darwin client archive, which
`goreleaser release --snapshot --clean` leaves in `dist/`.
`make macos-client-pkg` then wraps every one it finds there. To package a
single archive:

```sh
packaging/macos/pkg.sh dist/ssoossh-client_1.2.3_darwin_arm64.zip
```

With no signing material in the environment the package is built unsigned
and the log says so, which is fine for checking the payload and the panes
and useless for anything else: Gatekeeper refuses a downloaded package that
is not Developer ID Installer signed. The script's header lists the
variables and where CI reads them from.
