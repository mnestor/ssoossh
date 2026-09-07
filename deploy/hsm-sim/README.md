# HSM simulation

Two runnable topologies for exercising `ssoosshd`'s PKCS#11 path against
SoftHSM2, plus the fault injection that a local token cannot produce on its
own.

**Neither is a production deployment.** SoftHSM2 is a software emulator; the
token store is a file tree encrypted under the user PIN, and in both
topologies the signer holds that PIN. For production, use split mode or a
real HSM. The reasoning is in
[`docs/proposals/hsm-cloud-readiness.md`](../../docs/proposals/hsm-cloud-readiness.md).

| File | Topology it models |
| --- | --- |
| `docker-compose.direct.yml` | an HSM attached to the same machine: the module is mounted into the signer and `dlopen`'d in-process |
| `docker-compose.network.yml` | a network HSM: the token lives in a separate container reached over mutual TLS |

## Prerequisites

These compose files configure `hsm:`, which the default (cgo-free) server
build refuses at startup. They build the image from the repo's
`Dockerfile.pkcs11` (`distroless/cc-debian12`), so the binary has to carry
`-tags=hsm`:

```bash
cd ../..            # repo root
make server-linux-pkcs11-build-local
```

Then prepare a config, following the same convention as
`deploy/docker-compose.yml`:

```bash
cd deploy/hsm-sim
mkdir -p config
cp ../../server/config/defaults.yaml config/ssoosshd.yaml
```

Edit `config/ssoosshd.yaml`:

- set `http.address` to `0.0.0.0`, since the port mapping publishes from the
  container's external interface and a process bound to its own loopback is
  not reachable there;
- remove `ssh_key` and merge in `config/hsm.direct.yaml` or
  `config/hsm.network.yaml`. Exactly one of `ssh_key` or `hsm` may be set,
  and configuring both fails at startup.

`config/ssoosshd.yaml` is gitignored.

## Direct

```bash
docker compose -f docker-compose.direct.yml up -d
docker compose -f docker-compose.direct.yml --profile verify run --rm hsm-verify
```

`hsm-provision` runs once, initializes the token, generates an ECDSA P-384
CA key, stages `libsofthsm2.so`, and exits before `ssoosshd` starts. It
generates a random user PIN on first run rather than shipping a known one;
to supply your own, write it to the `pin` volume before the first `up`.

Re-running provisioning is safe. It refuses to touch a token that already
exists, which matters because the alternative is destroying the CA on a
container restart.

## Network

```bash
docker compose -f docker-compose.network.yml up -d
docker compose -f docker-compose.network.yml --profile verify run --rm hsm-verify
```

The chain, and why it has this many links:

```
ssoosshd
  -> p11-kit-client.so         PKCS#11 is dlopen'd in-process; the only way
                               to put a boundary under it is a forwarding
                               module
  -> /run/p11/p11.sock         p11-kit's ONLY transport is a Unix socket
  -> hsm-bridge (socat, mTLS)  so the network hop and its security are ours
  -> hsm:12345 (socat, mTLS)
  -> p11-kit server
  -> libsofthsm2.so -> token
```

The mutual TLS is bolted on by hand because p11-kit provides none:
`p11-kit server --help` has no host, port or TLS option, and its whole
access control model is file permissions on the socket. A real appliance
authenticates its clients itself. Read the extra links as a warning about
this shape, not as an endorsement of it.

Note what does not move to the appliance: the PIN. p11-kit forwards
`C_Login` rather than performing it, so the signer still holds the PIN and
still opens an authenticated session. This topology separates the key bytes
from the signer, not the authority to use them.

## Fault injection

This is the reason the network topology exists. A local SoftHSM token cannot
fail the way a network HSM fails, so the failure modes that matter most for
cloud HSM support are exactly the ones a normal test run never reaches.

Stop the appliance while the signer is running:

```bash
docker compose -f docker-compose.network.yml stop hsm      # outage
docker compose -f docker-compose.network.yml start hsm     # recovery
```

Pause it instead, to get an unbounded hang rather than a clean error:

```bash
docker compose -f docker-compose.network.yml pause hsm
docker compose -f docker-compose.network.yml unpause hsm
```

Break the tunnel without touching the appliance, which models a network
partition with the HSM still healthy:

```bash
docker compose -f docker-compose.network.yml stop hsm-bridge
```

### What this currently demonstrates

`ssoosshd` does not survive any of them. Reproduced with a harness making
the same `crypto11` calls the signer makes, signing every two seconds, with
the appliance stopped at t+7s and restarted at t+14s:

```
t+02s ok   sign #1
t+04s ok   sign #2
t+06s ok   sign #3
t+08s FAIL sign #4: pkcs11: 0x30: CKR_DEVICE_ERROR
...
t+24s FAIL sign #12: pkcs11: 0x30: CKR_DEVICE_ERROR
```

The HSM was healthy from t+14s and the process never recovered. `crypto11`
returns broken sessions to its pool without invalidating them, and
`HSMKeySource` caches one signer for the process lifetime, so only a restart
clears it. Meanwhile `/healthz` keeps answering.

That is a known, open defect, not a flaw in the simulation. See findings 3
to 5 in the proposal.

## Teardown

```bash
docker compose -f docker-compose.network.yml down -v
docker compose -f docker-compose.direct.yml down -v
```

`-v` destroys the token volume, and with it the simulated CA. That is
correct for a simulation and would be catastrophic anywhere else: in a real
deployment the token volume is the CA, and it needs backups and encryption
at rest like any private key. Note also that a fresh volume causes the
provisioner to generate a *new* CA, and because the key registry keeps every
announced key active, that accumulates CAs rather than failing loudly.

## Files

| Path | Purpose |
| --- | --- |
| `Dockerfile.hsm-tools` | `debian:12-slim` plus softhsm2, opensc, p11-kit, socat. Matches `Dockerfile.pkcs11`'s Debian base so staged modules are ABI-correct |
| `scripts/provision-token.sh` | idempotent token and CA key creation; stages `libsofthsm2.so` |
| `scripts/stage-client.sh` | stages `p11-kit-client.so` and `libffi` for the network topology |
| `scripts/gen-certs.sh` | throwaway CA and mTLS keypairs for the tunnel |
| `scripts/hsm-serve.sh` | appliance side: p11-kit server behind a socat mTLS listener |
| `scripts/hsm-bridge.sh` | client side: local Unix socket forwarded over mTLS |
