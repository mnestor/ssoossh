---
title: HSM and PKCS#11
description: Sourcing the CA private key from a PKCS#11 token so it never leaves the hardware, with a SoftHSM2 quick start.
sidebar:
  order: 14
---

`ssoosshd` can source the CA private key from a PKCS#11 token -- a hardware
security module, or a software emulator -- instead of holding it inline in the
config file. The signer reaches the key through the token during issuance, and
the private key never leaves it.

:::tip[Consider an ssh-agent before this page]
`ssh-add -s <module>` loads a PKCS#11 token into an `ssh-agent`, and
`ssoosshd` can take its CA key from there. The key still never leaves the
token, but the vendor's library loads in the agent's process rather than the
signer's, and it works with the default `ssoosshd` build.

The default build has **no PKCS#11 support**: it is cgo-free and statically
linked, and configuring `hsm` in it fails at startup. Everything on this
page needs an `hsm`-tagged build. See
[The CA key in an ssh-agent](/ssoossh/operations/ssh-agent/).
:::

## Which CA keys work on a token

The HSM path accepts a narrower set of keys than
[`ssh_key`](/ssoossh/reference/config/top-level/#ssh_key) does. This is the
whole list:

| CA key on the token | Works | FIPS-approved | Notes |
| --- | --- | --- | --- |
| ECDSA P-256 | yes | yes | |
| ECDSA P-384 | yes | yes | the default elsewhere in ssoossh |
| ECDSA P-521 | yes | yes | |
| RSA >= 2048 | yes | yes | restricted to `rsa-sha2-512` and `rsa-sha2-256` |
| RSA < 2048 | no | no | rejected at startup |
| ECDSA on any other curve | no | no | rejected at startup |
| Ed25519 | no | no | see the caution below |
| DSA, and anything else | no | no | |

RSA of at least 3072 bits is worth preferring over 2048: that is NIST's
recommendation for security beyond 2030, and it is what `ssoossh` itself
defaults to when it generates an RSA key.

### Signing cost

One certificate costs one signature, and that signature is the only
per-request work that depends on the CA key type. Measured with
`make bench-hsm` on a Ryzen 7 5700G, medians of five runs:

| CA key | Signature, token | Signature, `ssh_key` | Whole job, token | Jobs/s, one signer |
| --- | --- | --- | --- | --- |
| ECDSA P-256 | 0.09 ms | 0.04 ms | 0.12 ms | ~8,400 |
| ECDSA P-384 | 0.3 ms | 0.2 ms | 0.34 ms | ~2,900 |
| ECDSA P-521 | 0.4 ms | 0.6 ms | 0.38 ms | ~2,600 |
| RSA 2048 | 0.9 ms | 1.0 ms | 1.1 ms | ~900 |
| RSA 3072 | 2.2 ms | 2.5 ms | 3.2 ms | ~310 |
| RSA 4096 | 4.8 ms | 5.5 ms | 5.1 ms | ~195 |

"Whole job" is `Sign()`: lifetime checks, public-key parsing, certificate
construction, the signature, and marshalling the reply -- everything the
signer's handler does per message. For the fast curves it is noticeably
more than the signature alone, which is why the rate column is derived
from it and not from the signature.

:::caution[The rate column is a ceiling, not a capacity]
Even "whole job" stops at the edge of the process. It excludes NATS
delivery, the acknowledgement round trip, and everything the API tier and
database do per request -- and for an ECDSA CA those dominate completely.
A real deployment will not see 8,400 certificates a second from a P-256
signer; it will see whatever the broker and the database allow, with the
signature invisible in the noise. Use the column to compare key types, and
measure your own pipeline before planning capacity from it.
:::

The rate is per signer process, because a signer works through its queue
one job at a time: the NATS subscription hands over the next job only once
the previous one is acknowledged. A second `ssoosshd sign` process roughly
doubles that ceiling -- both compete on the `signer` queue group and the
work divides -- provided they are not sharing a CPU, and provided signing
was the constraint in the first place. At ECDSA rates it will not have
been. See [Multi-instance and NATS](/ssoossh/operations/multi-instance/).

Read the ratios, not the absolute figures. ECDSA P-256 signs roughly forty
times faster than RSA 4096, and that ordering holds anywhere. The absolute
numbers do not: they come from SoftHSM2, a **software** emulator that does
the arithmetic on the same CPU as everything else.

:::caution[A real HSM is not this fast]
Nothing in the table measures hardware. A physical token adds command,
transport, and session latency -- typically a few milliseconds over PCIe or
USB, tens of milliseconds over a network appliance -- and that latency
usually dominates the arithmetic entirely. Treat the token columns as a
floor, benchmark your own device, and expect the gap between key types to
matter less on real hardware than it does here.
:::

Notice that RSA on the token is not slower than RSA in process, and P-521
is actually faster there. SoftHSM2 is not paying any hardware cost, so what
the comparison really shows is that PKCS#11 call overhead is small next to
the arithmetic itself for the larger keys.

None of this is a reason to pick a key type on its own. Even the slowest
row is around five milliseconds, against a flow whose other half is a human
deciding whether to approve a request. Pick ECDSA P-384 for the reasons in
the table above -- FIPS-approved, supported everywhere on this path -- and
the cost will not be what you notice.

### Under `fips: true`

[`fips`](/ssoossh/reference/config/top-level/#fips) applies to the CA key
whatever its source, so an HSM key is checked the same way an inline one is.
ECDSA and RSA are approved; nothing else is, and the process refuses to start
rather than issue a certificate it should not.

On the HSM path that gate never actually fires: every key type the token path
accepts is already FIPS-approved, so the two lists coincide. Turning FIPS on
takes nothing away from an HSM deployment.

It is `ssh_key` where the distinction bites, because Ed25519 works there
normally and is refused under FIPS:

```text
ssoosshd: failed to initialize signer:
CA key algorithm "ssh-ed25519" is not FIPS-approved
```

So "move the CA into an HSM" and "turn FIPS on" push toward the same key
choice from opposite directions, and an ECDSA P-384 CA satisfies both.

`ssoossh` treats Ed25519 as not FIPS-approved deliberately, even though EdDSA
entered FIPS 186-5 in 2023: several FIPS policies still reject `ssh-ed25519`
outright, so a key generated with it may be unusable against a FIPS-mode
server. Leaving `fips` unset follows the Go runtime's own mode,
`crypto/fips140.Enabled()`.

### What rejection looks like

Each of these fails at startup, not at the first signature:

| Key | Error |
| --- | --- |
| Ed25519 | `find CA key pair in HSM: unsupported key type: 40` |
| ECDSA on a non-NIST curve | `unsupported ECDSA curve "..." for HSM CA key` |
| RSA below 2048 | `HSM CA RSA key is 1024 bits, must be at least 2048` |
| Ed25519 via `ssh_key`, under `fips: true` | `CA key algorithm "ssh-ed25519" is not FIPS-approved` |

:::caution
Ed25519 is **not** supported on the HSM path, a limitation of the PKCS#11
library in use. For an Ed25519 CA key, keep
[`ssh_key`](/ssoossh/reference/config/top-level/#ssh_key).

The token will accept such a key if you import one, and `ssoosshd` will then
fail at startup rather than at first signature --
[Ed25519 needs a third tool](#ed25519-needs-a-third-tool) has the conversion,
the error, and why it is a dead end.
:::

Exactly one of `ssh_key`,
[`ssh_key_file`](/ssoossh/reference/config/top-level/#ssh_key_file),
[`ssh_key_agent`](/ssoossh/operations/ssh-agent/) or
[`hsm`](/ssoossh/reference/config/hsm/) may be set: two CA key sources would
be ambiguous, and configuring more than one fails at startup. API-mode
instances have none; signing modes require one.

An Ed25519 CA that cannot go on a token has two homes rather than one:
inline `ssh_key`, or `ssh_key_file`, which at least keeps it out of the
config file. It can also carry a passphrase; see
[what that does and does not buy](/ssoossh/concepts/security-model/#what-a-passphrase-changes-and-what-it-does-not).

## Configuration

The PKCS#11 module path varies by distribution. The examples below use
`/usr/lib/softhsm/libsofthsm2.so`, the standard Debian and Ubuntu location. If
your system differs, find it with `find /usr -name 'libsofthsm2.so'` and
substitute.

### A token with a PIN

```yaml
hsm:
  module: /usr/lib/softhsm/libsofthsm2.so
  token_label: ssoossh-ca
  pin: "1234"
  key_label: ssoossh-ca
```

### A token with a PIN file

Better: keep the PIN out of the config file, where it would also appear
(redacted) in the auditors' effective-configuration view.

```yaml
hsm:
  module: /usr/lib/softhsm/libsofthsm2.so
  token_label: ssoossh-ca
  pin_file: /etc/ssoossh/hsm-pin
  key_label: ssoossh-ca
```

```bash
echo -n "1234" | sudo tee /etc/ssoossh/hsm-pin > /dev/null
sudo chmod 600 /etc/ssoossh/hsm-pin
sudo chown ssoossh:ssoossh /etc/ssoossh/hsm-pin
```

### Selecting by key ID

If the token holds several keys, use the hex key ID (`CKA_ID`) instead of, or
alongside, the label:

```yaml
hsm:
  module: /usr/lib/softhsm/libsofthsm2.so
  token_label: ssoossh-ca
  pin_file: /etc/ssoossh/hsm-pin
  key_id: "01"
```

| Key | Required | Notes |
| --- | --- | --- |
| [`hsm.module`](/ssoossh/reference/config/hsm/#module) | yes | path to the PKCS#11 shared library |
| [`hsm.token_label`](/ssoossh/reference/config/hsm/#token_label) | yes | the token's label |
| [`hsm.pin`](/ssoossh/reference/config/hsm/#pin) | one of the two | the PIN as config text |
| [`hsm.pin_file`](/ssoossh/reference/config/hsm/#pin_file) | one of the two | a file holding the PIN. Exactly one of `pin` or `pin_file` is required |
| [`hsm.key_label`](/ssoossh/reference/config/hsm/#key_label) | one of the two | at least one of `key_label` or `key_id` is required |
| [`hsm.key_id`](/ssoossh/reference/config/hsm/#key_id) | one of the two | hex `CKA_ID`; a non-hex value fails at startup |

## SoftHSM2 quick start

SoftHSM2 is a software PKCS#11 emulator. This is for testing and development,
and is what CI exercises.

### 1. Install SoftHSM2 and OpenSC

```bash
sudo apt-get update && sudo apt-get install -y softhsm2 opensc
softhsm2-util --version
```

### 2. Initialize a token

Labelled `ssoossh-ca`, with user PIN `1234` and SO (Security Officer) PIN
`9999`:

```bash
sudo softhsm2-util --init-token --free --label ssoossh-ca --pin 1234 --so-pin 9999
```

The output shows the slot number -- typically 0. Note it for the commands
below.

### 3. Generate a key pair on the token

```bash
sudo pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so \
  --slot 0 --login --pin 1234 \
  --keypairgen --key-type EC:prime256v1 \
  --label ssoossh-ca --id 01
```

Verify, and note the key ID while you are here:

```bash
sudo pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so \
  --slot 0 --login --pin 1234 \
  --list-objects
```

Both the public and private key objects should appear.

### 4. Or import an existing PEM CA key

`softhsm2-util --import` takes PKCS#8, so convert first. If the key is
already in a format OpenSSL can read:

```bash
openssl pkcs8 -topk8 -inform PEM -outform PEM \
  -in ca-key.pem -out ca-key-pkcs8.pem -nocrypt

sudo softhsm2-util --import ca-key-pkcs8.pem \
  --slot 0 --label ssoossh-ca --id 01 --pin 1234
```

A CA key generated with `ssh-keygen` is not in that format: it is an
`OPENSSH PRIVATE KEY`, which OpenSSL cannot decode at all.

```text
Could not find private key of key from ca-key.pem
error:1E08010C:DECODER routines:OSSL_DECODER_from_bio:unsupported
```

For an ECDSA or RSA key, `ssh-keygen` will rewrite it in place:

```bash
cp ca-key ca-key-pkcs8.pem
ssh-keygen -p -N "" -m PKCS8 -f ca-key-pkcs8.pem
head -1 ca-key-pkcs8.pem     # -----BEGIN PRIVATE KEY-----
```

#### Ed25519 needs a third tool

Neither `openssl` nor `ssh-keygen` will convert an OpenSSH-format Ed25519
private key to PKCS#8. OpenSSL cannot read the input, and `ssh-keygen` has
no Ed25519 support on this path:

```text
do_convert_to_pkcs8: unsupported key type ED25519
```

:::danger[`ssh-keygen -p -m PKCS8` fails silently on Ed25519]
The in-place rewrite above reports success on an Ed25519 key --
`Your identification has been saved with the new passphrase.` -- and leaves
the file in OpenSSH format, unchanged. Check the first line of the file
rather than the exit code. `-m PEM` behaves the same way.
:::

Python's [`cryptography`](https://cryptography.io/) reads the OpenSSH
container and writes PKCS#8:

```python
from cryptography.hazmat.primitives import serialization

with open("ca-key", "rb") as f:
    key = serialization.load_ssh_private_key(f.read(), password=None)

with open("ca-key-pkcs8.pem", "wb") as f:
    f.write(key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    ))
```

Pass `password=b"..."` if the key has a passphrase. The result is an
ordinary PKCS#8 file that `openssl pkey` and `softhsm2-util --import` both
accept.

That conversion is worth knowing, but it will not get an Ed25519 CA onto
the HSM path in `ssoosshd`. SoftHSM2 imports the key happily, and the
signer then refuses it at startup:

```text
ssoosshd: failed to initialize signer: failed to load CA signing key:
find CA key pair in HSM: unsupported key type: 40
```

`40` is `CKK_EC_EDWARDS`. See the caution at the top of this page: an
Ed25519 CA belongs in
[`ssh_key`](/ssoossh/reference/config/top-level/#ssh_key), not a token.

### 5. Token directory permissions

SoftHSM2 stores token data in `/var/lib/softhsm/tokens/` by default. The
account running `ssoosshd` must be able to read and write it:

```bash
sudo chown -R ssoossh:ssoossh /var/lib/softhsm/tokens/
sudo chmod 700 /var/lib/softhsm/tokens/
```

Adjust the user name to whatever your unit runs as -- the systemd unit in
[Installing the server](/ssoossh/operations/install/) uses `ssoossh`.

## Getting the CA public key for sshd

The server publishes the signer's CA public key -- whether it came from
`ssh_key` or a token -- through a registry that persists across signers and
instances. That registry is the canonical source for `sshd`'s
`TrustedUserCAKeys`.

The simplest way to read it is the client, which is also what
[Trusting the CA in sshd](/ssoossh/hosts/sshd-trust/) uses:

```bash
ssoossh --server https://ssh.example.com ca > /etc/ssh/ssoossh_ca.pub
```

Over HTTP the endpoint is `GET /api/ca`. It is public by design -- it returns
a public key -- and answers with the project's standard JSON envelope, whose
`data.ca` holds the keys in `authorized_keys` form, newline-separated when
more than one is active:

```bash
curl -s https://ssh.example.com/api/ca | jq -r .data.ca
```

**In full mode**, the server loads its CA key at startup and registers it.

**In split mode**, start the API server first (it needs no key source), then
the signer. The signer announces its key to the registry over NATS within
seconds. Until an announcement lands the endpoint has nothing to serve.

Then, on each target host:

```text
TrustedUserCAKeys /etc/ssh/ssoossh_ca.pub
```

and reload `sshd`.

### Multiple keys during rotation

Several CA keys can be active at once. Each signer announces its own key, the
endpoint returns the full set, and clients and `pam_ssoossh` accept a
certificate signed by any of them. A new signer can therefore publish its key
while the old one is still registered, which is what makes a gradual cutover
possible -- and also what lets independent signers hold distinct keys.

## Real HSM support

Any PKCS#11-compliant module should work:

| Device | Module |
| --- | --- |
| YubiHSM 2 | `libyubihsm_pkcs11` |
| AWS CloudHSM | the CloudHSM PKCS#11 library |
| TPM 2.0 | `libtpm2_pkcs11` |
| Thales Luna / NetHSM | the vendor's PKCS#11 module |

The configuration is identical: point `module` at the library, and set
`token_label`, a PIN source, and `key_label` or `key_id`.

SoftHSM2 is what CI tests against. For production, consult your vendor's
documentation for PIN and credential management, backup and recovery, and
high availability.

## Docker and containers

Everything on this page needs the **`-pkcs11` image**. The default image has
no PKCS#11 support at all.

- `ghcr.io/mnestor/ssoossh-server:<version>` -- the default. Statically
  linked, built on `distroless/static-debian12` (2.11MB base), no libc, no
  `dlopen`. Configuring `hsm` here fails at startup.
- `ghcr.io/mnestor/ssoossh-server:<version>-pkcs11` -- built on
  `distroless/cc-debian12`, which carries `libstdc++` and `libgcc_s`. This
  is the one that can load a module.

The old `-musl` tag is gone. It existed so a musl-built module could be
mounted into a musl container, but `alpine:3.20` ships no `libstdc++`, so
that never actually worked. The default image being static also removes the
reason to have a musl variant at all: it runs on Alpine like anywhere else.

### Option 1: mount the module and token store

```bash
docker run -v /usr/lib/softhsm/libsofthsm2.so:/usr/lib/softhsm/libsofthsm2.so:ro \
  -v /var/lib/softhsm/tokens/:/var/lib/softhsm/tokens/:rw \
  -v /etc/ssoossh/ssoosshd.yaml:/etc/ssoosshd.yaml:ro \
  ghcr.io/mnestor/ssoossh-server:<version>-pkcs11
```

The module must be built against glibc, and against a glibc no newer than
Debian 12's. Versioned symbols skew in both directions, and a module built
on a newer distribution fails at startup naming the module rather than the
toolchain. `deploy/hsm-sim/` stages one out of a `debian:12-slim` image for
exactly that reason.

:::tip
Option 0, which is not on this page: put the token behind an `ssh-agent`
with `ssh-add -s <module>` and use the default image. The key still never
leaves the token, the vendor library loads in the agent's process rather
than the signer's, and there is no module to match a libc against. See
[The CA key in an ssh-agent](/ssoossh/operations/ssh-agent/).
:::

### Option 2: split signer, recommended for production

Run the signer on the machine the HSM is attached to, and the API server in
the container, connected over NATS:

```bash
# On the HSM host
ssoosshd -c signer.yaml sign
```

```bash
# In the container
docker run -v /etc/ssoossh/api.yaml:/etc/ssoosshd.yaml:ro \
  ghcr.io/mnestor/ssoossh-server:<version> serve api
```

Both processes need `pubsub.backend: nats` with mTLS credentials --
[Startup modes](/ssoossh/operations/startup-modes/) and
[Multi-instance and NATS](/ssoossh/operations/multi-instance/). The container
then holds no private key at all: it learns the CA public key from the
signer's announcement.

## Platform matrix

The default `ssoosshd` build is `CGO_ENABLED=0` and statically linked, so it
has no libc requirement. PKCS#11 needs cgo, which is why it is a separate
artifact.

| Build | Ships as | Requires |
| --- | --- | --- |
| default | `.deb`, `.rpm`, `.apk`, `.tar.gz`, and the `:<version>` image | nothing. One binary per architecture, any distribution |
| pkcs11 | `.deb`, `.rpm`, `.tar.gz`, and the `:<version>-pkcs11` image | glibc 2.28 or newer (RHEL 8, Ubuntu 20.04, Debian 11+) |

`ssoosshd-pkcs11` declares `Conflicts`, `Replaces` and `Provides` against
`ssoosshd`: both install the same `/usr/sbin/ssoosshd`, so a host runs
one or the other, and anything depending on `ssoosshd` is satisfied by
either.

There is no musl package or image for the PKCS#11 build. The `.apk` above is
the static binary, which needs no musl build to run on Alpine.

Both are built for linux/amd64 and linux/arm64. Client binaries (`ssoossh`)
remain statically linked and cross-platform.

Choose by whether you need the module in-process, not by your host's libc:
the default build has no libc to match, and for the `-pkcs11` image it is the
mounted module's libc that has to match Debian 12's, not the host's.
