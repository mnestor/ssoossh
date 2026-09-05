---
title: HSM and PKCS#11
description: Sourcing the CA private key from a PKCS#11 token so it never leaves the hardware, with a SoftHSM2 quick start.
eyebrow: Server operations
sidebar:
  order: 13
---

`ssoosshd` can source the CA private key from a PKCS#11 token -- a hardware
security module, or a software emulator -- instead of holding it inline in the
config file. The signer reaches the key through the token during issuance, and
the private key never leaves it.

Supported algorithms: ECDSA P-256, P-384, P-521, and RSA keys of 2048 bits or
more (signed with `rsa-sha2-512`).

:::caution
Ed25519 is **not** supported on the HSM path, a limitation of the PKCS#11
library in use. For an Ed25519 CA key, keep
[`ssh_key`](/ssoossh/reference/config/top-level/#ssh_key).
:::

Exactly one of `ssh_key` or [`hsm`](/ssoossh/reference/config/hsm/) may be
set: two CA key sources would be ambiguous, and configuring both fails at
startup. API-mode instances have neither; signing modes require one.

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

Convert to PKCS#8 first:

```bash
openssl pkcs8 -topk8 -inform PEM -outform PEM \
  -in ca-key.pem -out ca-key-pkcs8.pem -nocrypt

sudo softhsm2-util --import ca-key-pkcs8.pem \
  --slot 0 --label ssoossh-ca --id 01 --pin 1234
```

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

`ghcr.io/mnestor/ssoosshd` ships two image variants per version, both
dynamically linked so a PKCS#11 module can be `dlopen`'d:

- `ghcr.io/mnestor/ssoosshd:<version>` -- glibc, built on
  `distroless/base-debian12`.
- `ghcr.io/mnestor/ssoosshd:<version>-musl` -- musl, built on Alpine.

Which one to run has nothing to do with the *host* OS; Docker abstracts that
away, and either image runs on any host with a container runtime. It matters
only for option 1 below: a module built against glibc cannot be loaded from
inside a musl container, or vice versa. Match the image variant to the libc
the module you are mounting was built against.

### Option 1: mount the module and token store

```bash
docker run -v /usr/lib/softhsm/libsofthsm2.so:/usr/lib/softhsm/libsofthsm2.so:ro \
  -v /var/lib/softhsm/tokens/:/var/lib/softhsm/tokens/:rw \
  -v /etc/ssoossh/ssoosshd.yaml:/etc/ssoosshd.yaml:ro \
  ghcr.io/mnestor/ssoosshd:<version>
```

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
  ghcr.io/mnestor/ssoosshd:<version> serve api
```

Both processes need `pubsub.backend: nats` with mTLS credentials --
[Startup modes](/ssoossh/operations/startup-modes/) and
[Multi-instance and NATS](/ssoossh/operations/multi-instance/). The container
then holds no private key at all: it learns the CA public key from the
signer's announcement.

## Platform matrix

`ssoosshd` is dynamically linked (cgo-enabled) to support PKCS#11 module
loading.

| Build | Ships as | Requires |
| --- | --- | --- |
| glibc | `.deb`, `.rpm`, `.tar.gz`, and the default image | glibc 2.28 or newer (RHEL 8, Ubuntu 20.04, Debian 11+). The recommended build for most distributions |
| musl | `.apk`, `.tar.gz`, and the `-musl` image | Alpine and other musl systems. Also dynamic, so modules load at runtime |

Both are built for linux/amd64 and linux/arm64, and all four combinations
support HSM configuration. Client binaries (`ssoossh`) remain statically
linked and cross-platform.

Choose the build matching your OS package format and C library. For the
container images, it is the mounted module's libc that decides, not the host's.
