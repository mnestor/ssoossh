# PKCS#11 (HSM) CA Key Configuration

## Overview

ssoosshd can source the CA private key from a PKCS#11 token (Hardware Security Module or software emulator) instead of storing it inline in config or process memory. The signer accesses the key through the token during certificate issuance; the private key never leaves the token.

Supported algorithms: ECDSA P-256, ECDSA P-384, ECDSA P-521, and RSA keys >= 2048 bits (signed with rsa-sha2-512).

**Not supported:** Ed25519 keys on the HSM path (Go's PKCS#11 library limitation). For Ed25519 CA keys, use `ssh_key` directly.

## SoftHSM2 Quick Start

This section demonstrates setting up SoftHSM2, a software-based PKCS#11 emulator, for testing and development.

### 1. Install SoftHSM2 and OpenSC

```bash
sudo apt-get update && sudo apt-get install -y softhsm2 opensc
```

Verify installation:

```bash
softhsm2-util --version
```

### 2. Initialize a Token

Create a token labeled `ssoossh-ca` with user PIN `1234` and SO (Security Officer) PIN `9999`:

```bash
sudo softhsm2-util --init-token --free --label ssoossh-ca --pin 1234 --so-pin 9999
```

The output shows the slot number; note it for later commands (typically slot 0).

### 3. Generate a Key Pair on the Token

Generate an ECDSA P-256 key pair:

```bash
sudo pkcs11-tool --module /usr/local/lib/softhsm/libsofthsm2.so \
  --slot 0 --login --pin 1234 \
  --keypairgen --key-type EC:prime256v1 \
  --label ssoossh-ca --id 01
```

Verify the key was created:

```bash
sudo pkcs11-tool --module /usr/local/lib/softhsm/libsofthsm2.so \
  --slot 0 --login --pin 1234 \
  --list-objects
```

You should see both the public and private key objects.

### 4. Import an Existing PEM CA Key

If you have an existing CA private key in PEM format (e.g., `ca-key.pem`), convert it to PKCS#8 format and import it:

```bash
# Convert OpenSSH private key to PKCS#8 format
openssl pkcs8 -topk8 -inform PEM -outform PEM \
  -in ca-key.pem -out ca-key-pkcs8.pem -nocrypt

# Import into the token
sudo softhsm2-util --import ca-key-pkcs8.pem \
  --slot 0 --label ssoossh-ca --id 01 --pin 1234
```

### 5. Token Directory Permissions

SoftHSM2 stores token data in `/var/lib/softhsm/tokens/` by default. The ssoosshd user (typically the system user running ssoosshd) must have read/write access:

```bash
sudo chown -R ssoosshd:ssoosshd /var/lib/softhsm/tokens/
sudo chmod 700 /var/lib/softhsm/tokens/
```

(Adjust the user name if ssoosshd runs as a different user; commonly it runs as `root` or a dedicated service account.)

## Configuration

Add an `hsm:` block to your `ssoosshd.yaml`. Exactly one of `ssh_key` or `hsm` may be set; both cannot coexist. See `docs/ssoosshd.yaml.default` for additional options.

### Example: Using a Token with PIN

```yaml
hsm:
  module: /usr/local/lib/softhsm/libsofthsm2.so
  token_label: ssoossh-ca
  pin: "1234"
  key_label: ssoossh-ca
```

### Example: Using a Token with PIN File

For better security, store the PIN in a separate file (readable only by ssoosshd) and reference it:

```yaml
hsm:
  module: /usr/local/lib/softhsm/libsofthsm2.so
  token_label: ssoossh-ca
  pin_file: /etc/ssoossh/hsm-pin
  key_label: ssoossh-ca
```

Create the PIN file with restrictive permissions:

```bash
echo -n "1234" | sudo tee /etc/ssoossh/hsm-pin > /dev/null
sudo chmod 600 /etc/ssoossh/hsm-pin
sudo chown ssoosshd:ssoosshd /etc/ssoossh/hsm-pin
```

### Example: Using Key ID

If your token has multiple keys, use the hex key ID (CKA_ID) instead of or in addition to the label:

```yaml
hsm:
  module: /usr/local/lib/softhsm/libsofthsm2.so
  token_label: ssoossh-ca
  pin_file: /etc/ssoossh/hsm-pin
  key_id: "01"
```

Look up the key ID with:

```bash
sudo pkcs11-tool --module /usr/local/lib/softhsm/libsofthsm2.so \
  --slot 0 --login --pin 1234 \
  --list-objects
```

## Getting the CA Public Key for sshd

The server publishes the signer's CA public key (whether sourced from `ssh_key` or HSM) via a registry that persists across signers and instances. This is the canonical source for sshd's `TrustedUserCAKeys`.

### In Full Mode (Single Instance or Multi-Instance with Database)

At startup, the server loads its CA key and registers it in the database. Query the CA public key endpoint:

```bash
curl http://localhost:8080/ca/public-keys
```

The response is newline-delimited OpenSSH public keys (one or more). During key rotation or with multiple signers, multiple lines may appear.

### In Split Mode (Separate Signer Process)

The API server and signer run separately, communicating via NATS. Start the API server first (no key source), then start the signer. The signer announces its key to the registry over NATS within seconds. Query the endpoint as above.

### Using the Keys in sshd

Copy the public key(s) to the sshd host and configure sshd:

```bash
# Fetch and save
curl http://signer.example.com:8080/ca/public-keys > /etc/ssh/ca-user-keys.pub

# In /etc/ssh/sshd_config:
TrustedUserCAKeys /etc/ssh/ca-user-keys.pub
```

Reload sshd. Client certificates signed by any registered CA key are now accepted.

### Multiple Keys During Rotation

If you rotate your CA key, the new signer can publish its key while the old one is still in the registry (with TTL-based expiry). Both keys will appear in the endpoint response, allowing a gradual cutover period.

## Real HSM Support

Any PKCS#11-compliant module should work, including:

- **YubiHSM 2**: use the libyubihsm_pkcs11 module
- **AWS CloudHSM**: use the CloudHSM PKCS#11 library
- **TPM 2.0**: use libtpm2_pkcs11
- **ThalesHSM** (Luna, NetHSM): use the vendor's PKCS#11 module

The configuration is identical: set `module` to the path to your PKCS#11 library, `token_label`, `pin`, and `key_label` or `key_id` as appropriate.

SoftHSM2 is used in CI testing. If deploying to production, consult your HSM vendor's documentation for proper PIN/credential management, backup/recovery, and high-availability setup.

## Docker and Container Deployment

The Docker image is built on `distroless/base` (non-static, to support dynamic linking and dlopen for PKCS#11 modules).

### Option 1: Mount the PKCS#11 Module and Token Store

Mount the HSM library and token data into the container:

```bash
docker run -v /usr/local/lib/softhsm/libsofthsm2.so:/usr/local/lib/softhsm/libsofthsm2.so:ro \
  -v /var/lib/softhsm/tokens/:/var/lib/softhsm/tokens/:rw \
  -v /etc/ssoossh/ssoosshd.yaml:/etc/ssoosshd.yaml:ro \
  ssoossh/ssoosshd:latest
```

### Option 2: Split Signer (Recommended for Production)

Run the signer (`ssoosshd sign` mode) on the host machine near the HSM (or directly attached). Run the API server (`ssoosshd api` mode) in the container, connecting to the signer via NATS:

```bash
# On the HSM host
./ssoosshd sign --config signer.yaml

# In the container
docker run -e NATS_URL=nats://signer.example.com:4222 \
  ssoossh/ssoosshd:latest api --config api.yaml
```

See `docs/deployment.md` for split-mode details.

## Platform Matrix

ssoosshd is now dynamically linked (cgo-enabled) to support PKCS#11 module loading.

- **glibc build** (deb, rpm, tar.gz): glibc >= 2.28 (RHEL 8, Ubuntu 20.04, Debian 11+, etc.). This is the recommended build for most Linux distributions.
- **musl build** (apk, tar.gz): Alpine and other musl-based systems. Also dynamic, so PKCS#11 modules can be loaded at runtime.

Both artifacts support HSM configuration. Client binaries (`ssoossh`) remain statically linked and cross-platform.

Choose the build matching your OS package format and C library.
