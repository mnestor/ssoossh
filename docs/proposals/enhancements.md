# Enhancements

Small feature modifications logged for later. Each entry should say what
changes and why; promote an entry to its own proposal doc if it grows.

## Sign the Windows installer with Azure Artifact Signing via jsign

The MSI built by `packaging/windows/msi.sh` and the `ssoossh.exe` inside it
ship unsigned. Sign both with Azure Artifact Signing (formerly Trusted
Signing), driven by `jsign`.

The decision is made; only the work is deferred, and it is deliberately
small: one more `post:` hook line on `windows-build` for the exe, one
`jsign` call at the end of `msi.sh` for the package, `jsign` and a headless
JRE in `.github/docker/Dockerfile.runner`, and the Azure credentials in the
release workflow's secret load. Nothing about the artifact layout, the
checksum globs or the release contents changes.

Why this combination:

- **Not osslsigncode.** It is already in the runner image and needs no JVM,
  but it reaches a key over PKCS#11 and Artifact Signing exposes a REST
  digest-signing API instead. Using osslsigncode means buying an OV
  certificate on a cloud HSM (DigiCert KeyLocker, SSL.com eSigner) at
  $150-300/yr against roughly $120/yr for the service, and on Ubuntu 24.04
  and newer DigiCert documents an incompatibility between osslsigncode and
  the distro's PKCS#11 engine that requires building libp11 from source.
  That is more moving parts than the JVM it avoids, and more money.
- **Not a Windows runner.** Every signing integration Microsoft documents
  for the service is Windows-bound: SignTool plus their dlib, the Azure
  DevOps task, the GitHub Action, PowerShell. `trusted-signing-cli` is a
  SignTool wrapper and requires the Windows SDK; `dotnet/sign` is
  Windows-only and speaks to Key Vault, not this service. Adding a Windows
  job would reintroduce the artifact round-trip and the hand-written
  checksum patching that moving the macOS packaging into the container
  deleted.
- **Not EV.** SmartScreen stopped treating an EV certificate as an instant
  reputation bypass in 2024, so the premium over OV buys nothing.
- **jsign** is the only client that runs on Linux, and it signs both PE and
  MSI, so one tool covers the exe and the package.

Two things to settle before starting:

1. Eligibility. Individual developers are limited to the USA and Canada;
   organisations to the USA, Canada, the EU and the UK. Identity validation
   takes a few business days.
2. SignPath Foundation is free for open source and would cost nothing, but
   its terms require manual approval of each signing request, and it is
   unconfirmed whether the free tier exposes the code signing gateway that
   `jsign`'s `SIGNPATH` store type needs. Without that the flow becomes
   upload, wait for a human, download, which breaks the property that one
   goreleaser run produces every artifact. Worth applying for in parallel,
   not worth blocking on.

## Tighten the ACL on %ProgramData%\ssoossh

The MSI creates the directory (`packaging/windows/ssoossh.wxs`), which is
what `client/config/paths.go` asks for: it stops a non-administrator
creating it first and owning the file that is supposed to constrain them.
The directory then inherits ProgramData's ACL rather than carrying an
explicit one.

Setting an explicit ACL from the package needs `util:PermissionEx`, a WiX
extension, and `wixl` has no extension support. Options if this becomes
worth doing: a custom action, or move the check into the client so it
refuses to honour an enforced config file whose owner is not an
administrator or SYSTEM. The second is better -- it defends the
installations that predate the installer as well.
