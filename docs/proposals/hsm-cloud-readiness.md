# Cloud HSM readiness, and what SoftHSM can and cannot rehearse

**Status: designed, nothing built.** No production code has been written for
this. The simulation in `deploy/hsm-sim/` exists and works; every finding
below was reproduced against commit `3ae74f6` (2026-09-07) and will drift.

> **Before planning from this document**, re-run the checks in
> [Provenance](#provenance-what-was-verified-and-how). They are cheap, and
> three of the five findings are one-line container commands. Decisions
> resting on judgement rather than on a measurement are flagged
> **[judgement]** and are the ones worth re-opening first.

Related documents this one does not edit:

- [HSM and PKCS#11](https://mnestor.github.io/ssoossh/operations/hsm/) owns the operator-facing
  configuration. Its "Docker and containers" section contains a broken
  recipe; see [Finding 1](#finding-1-the-runtime-images-cannot-load-any-pkcs11-module).
- [Security model](https://mnestor.github.io/ssoossh/concepts/security-model/) records that a token-backed
  CA key "never leaves the hardware". That claim is what
  [Decision 2](#decision-2-the-server-image-carries-no-pkcs11-provider) protects.

## The question this answers

Can `ssoosshd` support a cloud HSM (AWS CloudHSM, GCP KMS via `libkmsp11`,
Luna DPoD, YubiHSM, Fortanix) without significant change, and can that
support be developed and tested against SoftHSM2?

**The configuration surface already fits.** `module`, `token_label`,
`pin`/`pin_file`, `key_label`/`key_id` is exactly the PKCS#11 vocabulary all
of those providers speak. No new config concepts are needed, and none of
what follows changes the YAML shape.

**Five things stand in the way, and SoftHSM alone will surface only two of
them.** That asymmetry is the real subject of this document: the gaps that
matter most for a network-attached HSM are precisely the ones a local
software token cannot produce.

## Findings

### Finding 1: the runtime images cannot load any PKCS#11 module

`distroless/base-debian12` ships glibc, OpenSSL and little else. It has no
`libstdc++.so.6` and no `libgcc_s.so.1`. SoftHSM2 is C++, and so are the
CloudHSM, Luna and Fortanix client libraries.

```
$ docker run --rm --entrypoint /lib64/ld-linux-x86-64.so.2 \
    -v /path/to/libsofthsm2.so:/tmp/m.so:ro \
    gcr.io/distroless/base-debian12:nonroot --list /tmp/m.so
/tmp/m.so: error while loading shared libraries: libstdc++.so.6:
cannot open shared object file: No such file or directory
```

`alpine:3.20`, the `-musl` image's base, has the same gap: `libcrypto.so.3`
is present, `libstdc++` is not.

This makes Option 1 in the HSM documentation ("mount the module and token
store") non-functional as written, on both image variants, for every module
including every vendor's. It is not a SoftHSM-specific problem, and it
blocks cloud HSM support outright.

`distroless/cc-debian12` resolves all of it, at a cost of 2.9MB
(20.8MB to 23.7MB):

```
libcrypto.so.3 => /usr/lib/x86_64-linux-gnu/libcrypto.so.3
libstdc++.so.6 => /usr/lib/x86_64-linux-gnu/libstdc++.so.6
libgcc_s.so.1  => /lib/x86_64-linux-gnu/libgcc_s.so.1
```

A Debian 12 `libsofthsm2.so` then loads with **no `LD_LIBRARY_PATH`**,
because `dlopen` on an absolute path resolves dependencies through the
image's ordinary search path. That matters: it means `hsm.module` can name a
staged module directly and the signer needs no environment tampering.

### Finding 2: a PIN is mandatory, and for some providers there is none

`server/config/types_signer.go:66`:

```go
if (h.PIN == "") == (h.PINFile == "") {
	return fmt.Errorf("exactly one of hsm pin or pin_file is required")
}
```

An XOR: exactly one must be non-empty, so an empty PIN cannot be expressed.
Providers that authenticate out of band cannot be configured at all. GCP's
`libkmsp11` takes its credentials through its own YAML config referenced by
an environment variable rather than a meaningful `C_Login` PIN.

**Verify the specific provider behaviour against vendor documentation before
implementing.** The shape of the gap is certain; the exact list of providers
it blocks is not, and this document does not assert one.

The fix is a tri-state so "explicitly no PIN" is distinguishable from
"unset". Because `PIN` and `PINFile` are both plain strings, that means
either a `*string` or an explicit `pin_mode: none`. **[judgement]** A
pointer keeps the YAML unchanged for everyone who has a PIN, which is
almost everyone; an explicit mode is self-documenting in the config file.
Preference is for the explicit mode, on the grounds that "this token needs
no PIN" is a statement an operator should have to make deliberately.

### Finding 3: a dead session is never recovered

This is the most important finding, and the only one with a runtime
consequence rather than a startup one.

`crypto11@v1.6.8/sessions.go:46`:

```go
func (c *Context) withSession(f func(session *pkcs11Session) error) error {
	session, err := c.getSession()
	if err != nil {
		return err
	}
	defer c.pool.Put(session)
	return f(session)
}
```

The session returns to the pool unconditionally, whatever `f` returned.
Grepping the whole module for `CKR_SESSION_HANDLE_INVALID`,
`CKR_DEVICE_ERROR` or any reconnect logic returns nothing. There is no error
classification, no session invalidation, no re-login, no re-`Configure`.

`NewHSMKeySource` (`server/signer/hsmkeysource.go:77`) connects once at
startup, and `HSMKeySource.Signer` (`:105`) returns the same cached
`ssh.Signer` for the process lifetime. So a broken session is recycled
forever.

Reproduced against the network simulation, using a harness that makes the
identical `crypto11.Configure` / `FindKeyPair` / `SignCert` calls the signer
makes, running in `distroless/cc-debian12`, signing every two seconds while
the HSM container was stopped at t+7s and restarted at t+14s:

```
OK  CA key type: ecdsa-sha2-nistp384
t+02s ok   sign #1
t+04s ok   sign #2
t+06s ok   sign #3
t+08s FAIL sign #4: pkcs11: 0x30: CKR_DEVICE_ERROR
...
t+24s FAIL sign #12: pkcs11: 0x30: CKR_DEVICE_ERROR
```

**The HSM was healthy again from t+14s onward and the process never
recovered.** Only restarting `ssoosshd` clears it.

Against SoftHSM on local files this never fires. Against a network-attached
HSM, session death is routine: appliance failover, idle timeout, a network
blip, credential rotation. Today each of those converts a transient event
into a permanent signing outage requiring manual intervention.

### Finding 4: no deadline on the signing call

`server/signer/sign.go:193` obtains the signer with `ks.Signer(ctx)`, but
`HSMKeySource.Signer` ignores the context and returns the cached value. The
subsequent `cert.SignCert(rand.Reader, caSigner)` is a blocking cgo call
into the module with no deadline of any kind.

A hung network HSM therefore hangs the request, and the goroutine, for as
long as the module takes to give up. A local SoftHSM never hangs, so this
too is invisible under the current test suite.

### Finding 5: readiness does not include the HSM

`healthzHandler` (`server/bootstrap/router.go:254`) answers the liveness
probe without touching the key source. With a cloud HSM the process can be
alive, scheduled, and receiving traffic while structurally unable to sign.

Findings 3 and 5 compound: after a transient outage the signer is
permanently broken and still reporting healthy.

## What SoftHSM does and does not rehearse

| Behaviour | SoftHSM2 reproduces it |
| --- | --- |
| PKCS#11 call sequence, `C_Login`, session handling | yes |
| Token and key selection by label and by `CKA_ID` | yes |
| Key type gating (`wrapCASigner`) | yes |
| Startup failure on a misconfigured token | yes |
| Signing latency characteristics | no, it is faster than any appliance |
| Session death and reconnection | **no** |
| Partition, hang, unbounded call duration | **no** |
| Credential expiry mid-process | **no** |

Findings 3, 4 and 5 all live in the bottom half. A test suite built on a
local SoftHSM token will pass while every one of them is broken, which is
why they are still open.

**The fix is to give SoftHSM a network topology.** Putting the token behind
`p11-kit server` and a socket turns the provider into a separate process
that can be stopped, paused and restarted. That reproduces the entire bottom
half of the table with no hardware and no cloud account, and it is what
`deploy/hsm-sim/docker-compose.network.yml` builds.

## Decisions and the reasoning behind each

### Decision 1: move the server images to a base that can load modules

Glibc image to `distroless/cc-debian12`. This is not about SoftHSM; it is the
precondition for every cloud HSM, and it fixes a documented feature that
does not currently work.

The musl image is harder and is **not** resolved here. `Dockerfile.musl`
carries no `RUN` deliberately, so goreleaser builds both architectures from
one amd64 runner without QEMU, and `apk add libstdc++` would reintroduce
that. Two candidate routes, **neither validated**:

- a `FROM --platform=$BUILDPLATFORM` stage using `apk --arch <target>
  --root /out` to cross-download the package, then `COPY --from`, which
  preserves the no-QEMU property if `apk`'s cross-install works cleanly;
- accept QEMU on the arm64 musl build and take the CI time cost.

**[judgement]** The glibc change should not wait on the musl one. The musl
image exists for operators mounting a musl-built module, a strictly smaller
population than "everyone who wants an HSM".

### Decision 2: the server image carries no PKCS#11 provider

It carries the *ability to load* one and nothing more. Not SoftHSM, not
`pkcs11-tool`, not a vendor library.

The reasoning is the same for each, and it is about which path is easiest
rather than which is possible:

- Shipping `libsofthsm2.so` makes a software emulator the path of least
  resistance for every operator, while the security model documentation
  tells them a token-backed key "never leaves the hardware". For SoftHSM in
  the same container as its PIN, that sentence is false. An operator who
  understands `ssh_key` is the CA will guard the config file; one who
  believes they have moved to an HSM will not.
- Shipping `pkcs11-tool` is worse. Today a code-execution bug lands an
  attacker in a distroless container with no shell and no tooling. Adding
  `pkcs11-tool` hands them a scriptable CLI aimed at whatever token is
  reachable, including a real appliance. They can already sign through the
  application; the tool adds enumeration, `C_DestroyObject` and extraction
  of anything marked extractable.
- Either one couples server image respins to that provider's CVEs.

This also yields one uniform path: SoftHSM becomes just another mounted
module, staged the same way a CloudHSM or Luna library is mounted. One code
path, one documentation section, and the development setup is a faithful
rehearsal of production rather than a special case.

Corollary, and a hardening item worth stating in the operator docs:
`hsm.module` is a `dlopen` path taken from configuration
(`hsmkeysource.go:74`), so anyone who can write the config file gets
arbitrary native code in the process holding the CA. `deploy/docker-compose.yml`
already mounts config read-only; the docs should require it rather than
leave it a happenstance.

### Decision 3: provisioning lives in a separate, clearly non-production image

`ssoossh-hsm-tools`, built `FROM debian:12-slim` so it matches
`distroless/*-debian12`'s own base. A module staged out of it is ABI-correct
by construction.

That last point is not incidental. Bind-mounting a module from an arbitrary
host is fragile even when the base image family is right, because versioned
symbols skew in both directions. Staging a module built on my devcontainer
into `cc-debian12` fails on `OPENSSL_3.4.0`, `GLIBC_2.38` and
`CXXABI_1.3.15`, while Debian 12's own package loads cleanly. The failure
surfaces at `crypto11.Configure` during startup with a message naming the
module rather than the toolchain, which is a poor place to learn about it.

Requirements on the image, all implemented in `deploy/hsm-sim/`:

- distinct package name, never a tag variant of `ssoossh-server`, so a typo
  in `SSOOSSHD_VERSION` cannot reach it;
- excluded from the floating `1.2` / `1` tag promotion in `.goreleaser.yml`,
  so no restart can drift onto it;
- no `ENTRYPOINT`, so it reads as a toolbox rather than a daemon;
- the provisioning script refuses to touch an existing token. Compose
  restarts and Kubernetes init containers re-run it on every start, and
  `softhsm2-util --init-token` on a live token destroys the CA. This guard
  is the single most important line in the simulation.

### Decision 4: a PKCS#11 proxy is a test fixture, not a transport

Pointing `hsm.module` at `p11-kit-client.so` needs no code change: PKCS#11
modules are interchangeable and `crypto11` does not care what is behind the
`.so`. It was tempting as a way to get a network boundary for free. It is
not one, for three measured reasons.

**The PIN is not separated.** `p11-kit` forwards `C_Login` rather than
performing it. Through the proxy, without login, only the public object is
visible; with `--login --pin` supplied by the *client*, the private key
appears. The signer still holds the PIN and still opens an authenticated
session, so a compromise of the signer process is still a compromise of the
CA. The key bytes move; the authority does not.

**There is no transport.** `p11-kit server --help` offers no host, port or
TLS option. Its only transport is a Unix socket and its entire access
control model is that socket's file permissions (`-u/--user`, `-g/--group`).
`p11-kit remote` speaks its RPC over stdin and stdout, so a network hop
means tunnelling it through ssh or, as the simulation does, bolting on
socat with mutual TLS by hand.

**The authority granted is larger than the application's own.** A session
reached this way carries the full PKCS#11 verb set: `C_Sign` over arbitrary
bytes, `C_DestroyObject`, `C_GenerateKeyPair`. Split mode's NATS boundary
carries "issue a certificate for this principal" and runs it through the
approval and policy pipeline. Adding the proxy would create a second, weaker
path to the same asset.

So it is documented as a fault-injection harness and as a narrow operator
option, never as the recommended topology. Its value in that role is real
and is what Finding 3's reproduction rests on.

For production the answer is unchanged and already shipped: **split mode**.
The API tier holds no key material and learns the CA public key from the
signer's announcement. And for real cloud HSMs the network client is the
vendor's own PKCS#11 library, which does this properly with appliance-side
authentication, so "HSM over the network" needs nothing from this project
beyond Finding 1.

## An ssh-agent CA key source

**Status: built.** `AgentKeySource` (`server/signer/agentkeysource.go`),
`ssh_key_agent` config, algorithm gating shared with the HSM path
(`server/signer/casigner.go`).

### Why it is worth more than it looks

An agent-backed CA key gives the property that SoftHSM-in-a-container does
not: **the private key never enters `ssoosshd`'s address space.** The signer
holds a socket, sends a blob, and gets a signature. A compromise of the
signer process is a signing oracle, not a key disclosure, and the agent
protocol has no "export private key" operation by design.

That is the same property a real HSM provides, reached without a PKCS#11
module. Note what it sidesteps:

| Finding above | Agent source |
| --- | --- |
| 1, no `libstdc++` in the runtime images | irrelevant: `golang.org/x/crypto/ssh/agent` is pure Go, no cgo, no `dlopen`, no ABI skew |
| 2, PIN semantics | irrelevant: the agent holds an unlocked key, there is no credential to send |
| 3, no session recovery | tractable: recovery is redialling a Unix socket, not re-`Configure`-ing a PKCS#11 context |

It also does something the token path explicitly cannot: **Ed25519**.
`hsm.md` documents at length that an Ed25519 CA cannot live on a token
because `crypto11` cannot sign with it. An agent has no such restriction, so
this would be the first way to hold an Ed25519 CA key outside the signer
process.

And it reaches hardware by a second route. `yubikey-agent`, `gpg-agent` with
a smartcard, and Secretive on macOS all present hardware-held keys through
the agent protocol, so a YubiKey-backed CA becomes possible without any of
the PKCS#11 machinery in this document.

### What it does not give you

- **Not least-authority.** The agent signs arbitrary blobs. A compromised
  signer can sign anything the CA could, exactly as with a PKCS#11 proxy.
  What it prevents is the attacker walking away with the key.
- **Socket permissions are the whole access control**, as with p11-kit. Two
  containers sharing a volume share the key.
- **Something must load the key.** `ssh-add` needs the key material at load
  time, so an unattended restart still needs the key and any passphrase
  reachable from somewhere -- unless the agent lives on a different host and
  outlives the signer, which is the arrangement that actually pays.

**[judgement]** Given that, the honest positioning is: better than
`ssh_key`, better than SoftHSM in the same container, not a replacement for
split mode or a real HSM, and uniquely the only option for an Ed25519 CA
that is not inline PEM.

### The trap to get right

`agentKeyringSigner.Sign` uses `underlyingAlgo(pub.Type())`, which for RSA
is `ssh-rsa` -- SHA-1. An unconstrained agent-backed RSA CA would therefore
issue SHA-1 signatures, the exact defect `wrapCASigner`
(`server/signer/hsmkeysource.go:22`) already guards against on the HSM path.

`agentKeyringSigner` does implement `ssh.AlgorithmSigner` and maps
`ssh.KeyAlgoRSASHA256` / `KeyAlgoRSASHA512` onto the agent's
`SignatureFlagRsaSha256` / `SignatureFlagRsaSha512`
(`x/crypto@v0.56.0/ssh/agent/client.go:846-858`), so the fix is the same one
`wrapCASigner` already applies: wrap with `ssh.NewSignerWithAlgorithms`
restricted to the SHA-2 algorithms. Sharing that gating between the HSM and
agent sources, rather than reimplementing it, is the right shape.

### What shipped

```yaml
ssh_key_agent:
  socket: /run/ssoossh/agent.sock        # falls back to $SSH_AUTH_SOCK
  key_fingerprint: "SHA256:WdJsHl..."    # optional when the agent holds one key
```

A fourth `CAKeySource`, selected in `newCAKeySource` and added to the
exclusivity check in `resolveCAKey`. Selection is by fingerprint, not
position: `key_fingerprint` may be omitted when the agent holds exactly one
key, and is required otherwise rather than letting the CA depend on the
order keys were added.

It also carries the Finding 3 fix that the HSM path still lacks. `Signer`
revalidates the connection and reconnects if the agent has gone away, at the
cost of one Unix-socket round trip per certificate. The HSM path has no
equivalent and stays broken until the process restarts.

Two things only came out of testing against a real in-process agent rather
than a stand-in, and both would have shipped broken otherwise:

- **`agent.Key` does not implement `ssh.CryptoPublicKey`.** An agent's
  `Signers()` returns its own public-key type built from a wire blob, so a
  gate that type-asserts rejects every agent-held key. `cryptoPublicKey`
  marshals and re-parses to get the concrete type.
- **`agent.NewClient` starts a read-loop goroutine per client.** Building a
  fresh client per call left two goroutines racing to read the same socket,
  stealing each other's replies. It deadlocked rather than erroring. The
  client is now created once per connection.

## Splitting the build: cgo-free by default

**Status: built.** `//go:build hsm` on `server/signer/hsmkeysource.go` and
`server/bootstrap/cakeysource_hsm.go`, with `!hsm` counterparts.

### The measurement that made this obvious

`crypto11` was the **only** thing in `ssoosshd` requiring cgo. Everything
else, sqlite included, is pure Go (`glebarez/sqlite`, not `mattn`):

```
$ CGO_ENABLED=0 go build ./cmd/ssoosshd
# github.com/eclipse-keypont/crypto11
crypto11.go: undefined: pkcs11.ObjectHandle
sessions.go: undefined: pkcs11.Ctx
...        # and nothing else
```

So one build tag buys a great deal:

| | default build | `-tags=hsm` |
| --- | --- | --- |
| cgo | disabled | required |
| linking | static (`not a dynamic executable`) | dynamic against glibc or musl |
| runtime image | `distroless/static-debian12`, **2.11MB** | `distroless/cc-debian12`, 23.7MB |
| glibc/musl split | **none** | two variants, as today |
| HSM | through ssh-agent | direct PKCS#11 |

The default image base drops from 20.8MB to 2.11MB, and the entire
`-musl` variant -- with the unresolved `apk` cross-install question from
Decision 1 -- **disappears** for it. Finding 1 stops applying to the image
most people run.

Verified: the static binary runs in `distroless/static-debian12:nonroot`,
and the full test suite passes with `CGO_ENABLED=0` (one test file,
`server/service/auth_test.go`, was the last user of the cgo sqlite driver
and now uses the same pure-Go driver as production).

### Why the agent route is not a downgrade

`ssh-add -s <module>` loads a PKCS#11 token into an ssh-agent. Verified end
to end: a SoftHSM2 token with an ECDSA P-384 key, loaded with `ssh-add -s`,
signing a certificate for a **statically linked, cgo-free** binary.

The key still never leaves the token. What changes is *which process* loads
the vendor's C++ module -- the agent's, not the signer's. That is a
containment win on its own: a memory-safety bug in a vendor PKCS#11 library
can no longer corrupt the process holding the certificate pipeline.

### What shipped in the release pipeline

The distribution matrix, with the axis changed from "which libc" -- an
implementation detail operators should never have had to reason about -- to
"do you need the module in-process", which is an actual deployment decision.

| | default (static) | pkcs11 (dynamic) |
| --- | --- | --- |
| goreleaser build | `server-linux-build`, `CGO_ENABLED=0` | `server-linux-pkcs11-build`, `-tags=nomsgpack,hsm` |
| binaries per arch | **1** | 1, glibc 2.28 via zig |
| `.deb` / `.rpm` | `ssoosshd` | `ssoosshd-pkcs11` |
| `.apk` | **the same binary again** | none |
| image | `:<ver>`, distroless/static, 2.11MB base | `:<ver>-pkcs11`, distroless/cc, 23.7MB base |

Three decisions worth recording:

- **`-pkcs11`, not `-hsm`.** The default build supports HSMs perfectly well
  through ssh-agent. Naming the variant `-hsm` would imply it does not,
  which is the confusion this whole change exists to remove.
- **`ssoosshd-pkcs11` is a separate package name** with `Conflicts`,
  `Replaces` and `Provides` on `ssoosshd`, because both install the same
  `/usr/local/sbin/ssoosshd`. Without those, two packages fight over one
  path and nobody can tell which is installed.
- **The `-musl` image and the musl build are gone.** They existed so a
  musl-built module could be mounted into a musl container, and
  `alpine:3.20` ships no `libstdc++`, so that never worked. The static
  default build removes the reason for a musl variant anyway: it runs on
  Alpine like anywhere else, and the `.apk` now carries it. This also
  retires the unresolved `apk` cross-install question from Decision 1
  rather than answering it.

The release workflow's musl linkage check is replaced by one asserting the
invariant that actually matters now: the default build is statically linked
and the pkcs11 build is not. A regression in either direction is otherwise
silent -- cgo creeping back into the default build gives it an undeclared
libc floor, and cgo falling out of the pkcs11 build makes PKCS#11 dead on
arrival.

### The signer-only question, still open

`BootstrapSigner` is already clean -- pub/sub, CA key, signer handler, key
announcer, and no database, HTTP, OIDC or LDAP -- so restricting the pkcs11
artifact to `sign` mode would fit the architecture the documentation already
recommends.

**[judgement]** Not done, and I would leave it. The pkcs11 artifact is
currently the same binary with a tag, so all modes work. Restricting it
would remove single-box full-mode-plus-HSM, a legitimate deployment, to
enforce a preference; and the tag already delivers the security benefit that
motivated the split, since the internet-facing default build has no
`dlopen`, no cgo and no vendor C++ in its address space. A separate `cmd/`
would also not shrink the binary much without splitting `server/bootstrap`,
which still imports the database layer.

## Provenance: what was verified, and how

Everything below was run on 2026-09-07 against commit `3ae74f6`, on
linux/amd64, Docker 29.7.2.

| Claim | How to re-check |
| --- | --- |
| `base-debian12` cannot load a C++ module | `docker run --rm --entrypoint /lib64/ld-linux-x86-64.so.2 -v <module>:/tmp/m.so:ro gcr.io/distroless/base-debian12:nonroot --list /tmp/m.so` |
| `cc-debian12` can, with no `LD_LIBRARY_PATH` | same command against `cc-debian12` and a Debian 12 `libsofthsm2.so` |
| Image size delta | `docker image ls` after pulling both |
| `alpine:3.20` also lacks `libstdc++` | `docker run --rm alpine:3.20 ls /usr/lib/libstdc++*` |
| `p11-kit-client.so` needs `libffi.so.8` | `ldd` it in a `debian:12-slim` container with `p11-kit-modules` installed |
| p11-kit has no network transport | `p11-kit server --help`, `p11-kit remote --help` |
| The proxy forwards `C_Login` | `deploy/hsm-sim/docker-compose.network.yml`, verify profile, with and without `--login` |
| crypto11 never recovers a session | `deploy/hsm-sim/README.md`, "Fault injection" |
| crypto11 has no reconnect logic | grep `crypto11@v1.6.8` for `CKR_`, `reconnect`, `SessionHandleInvalid`; only `sessions.go:46` is relevant and it has none |

The Finding 3 timeline came from a throwaway Go harness reproducing
`hsmkeysource.go`'s call sequence, built in `golang:1.27-bookworm` and run
in `distroless/cc-debian12`. It was deliberately not committed: its proper
home is a tagged test under `server/signer`, which is the tier-3 work item.
To rebuild it, mirror `NewHSMKeySource`'s `crypto11.Configure` /
`FindKeyPair` / `wrapCASigner` sequence, then loop over
`ssh.Certificate.SignCert` on the single cached signer while stopping and
starting the `hsm` service.

Not verified, and flagged as such wherever it appears: the precise set of
cloud providers blocked by Finding 2; whether `apk`'s cross-architecture
install satisfies the musl route in Decision 1.
