---
title: Client settings enforcement
description: Lock ssoossh client settings across a fleet with the enforce file, Windows Group Policy, or macOS managed preferences.
eyebrow: Host administration
sidebar:
  order: 10
---

`ssoossh` client settings can be locked so a user cannot override them, through
three mechanisms: one per platform's native fleet-management tooling, plus the
cross-platform `enforce` YAML file.

:::caution[Guardrails, not a security boundary]
All three are guardrails. The client runs as the user, who can always supply
their own binary. The one setting that is actually enforced beyond the client's
own cooperation is `cert_options.*.valid_duration` on the server.
:::

## Precedence

Lowest to highest. Each source's value is overridden by any later one that also
sets the same key:

1. Built-in defaults
2. User config (`~/.config/ssoossh.yaml`, or
   `%AppData%\ssoossh\ssoossh.yaml`)
3. Local config (`./ssoossh.yaml`, or `--config`)
4. CLI flags (`--server`, `--key-type`, `--key-size`)
5. The `enforce` file named by the machine-wide config
   (`/etc/ssoossh/ssoossh.yaml`, or `%ProgramData%\ssoossh\ssoossh.yaml`)
6. Platform-native policy: the Windows registry key or the macOS managed
   preferences described below

A value simply absent from a source is left to whatever the next source down
sets. A policy source that only locks `server`, for example, leaves every other
setting free.

:::note[The three flags outrank everything]
`server`, `sshkey.type` and `sshkey.size` are also the three settings
`ssh login` accepts as CLI flags (`--server`, `--key-type`, `--key-size`).
Those flags are bound as flags rather than merged as config, so they take
precedence over every config-file-tier source above -- including `enforce` and
platform policy -- if the user actually passes them.

That is pre-existing behaviour, not something these mechanisms change, and it
is the same "guardrail, not a boundary" situation as running your own binary.
:::

## Settings

Every setting below can be set through the `enforce` YAML file, the Windows
registry, or macOS managed preferences. Same setting, three delivery
mechanisms, admin's choice.

| YAML key | Windows registry value | macOS plist key |
| --- | --- | --- |
| `server` | `Server` (REG_SZ) | `server` (string) |
| `capubkey` | `CAPubkey` (REG_SZ) | `capubkey` (string) |
| `insecure_skip_verify` | `SkipVerifySSL` (REG_DWORD 0/1) | `insecure_skip_verify` (bool) |
| `use_agent` | `UseAgent` (REG_DWORD) | `use_agent` (bool) |
| `fallback_file_agent` | `FallbackFileAgent` (REG_DWORD) | `fallback_file_agent` (bool) |
| `key_filename` | `KeyFilename` (REG_SZ) | `key_filename` (string) |
| `try_open_browser` | `TryOpenBrowser` (REG_DWORD) | `try_open_browser` (bool) |
| `fips` | `FIPS` (REG_DWORD; absent = unset) | `fips` (bool; absent = unset) |
| `sshkey.type` | `SSHKeyType` (REG_SZ) | `sshkey.type` (string) |
| `sshkey.size` | `SSHKeySize` (REG_DWORD) | `sshkey.size` (integer) |

`fips: true` from any locked source -- the `enforce` file or a platform policy
-- makes a non-FIPS-approved `sshkey.type` a hard error at startup rather than
a silent downgrade.

## Agent preflight verification

Before requesting a certificate, `ssoossh ssh login` verifies that the resolved
key storage will accept and release a key. The preflight runs a real
store-then-remove round trip with a throwaway keypair, to catch storage
failures **before** the user approves a certificate. That prevents the "approve
then lose it" hazard, where the server issues a certificate the client then
cannot store.

What it does depends on `use_agent` and `fallback_file_agent`:

| Settings | Behaviour |
| --- | --- |
| `use_agent: true`, `fallback_file_agent: true` (both defaults) | Probes the live agent. If that fails, probes file storage as a fallback. If file storage works, the login proceeds using files instead of the agent and prints "Agent storage check failed, falling back to file-based key storage." to stdout. If both fail, the login aborts before requesting a certificate |
| `use_agent: true`, `fallback_file_agent: false` | Probes the live agent. If that fails, the login aborts before requesting a certificate, with an error explaining that fallback is disabled |
| `use_agent: false` | Probes file-based storage directly. If that fails, the login aborts before requesting a certificate |

Locking `fallback_file_agent: false` across a fleet is therefore a decision
about failure mode as much as about key storage: it turns a broken agent into a
refused login rather than a file on disk.

## Windows: Group Policy

The client reads `HKLM\SOFTWARE\Policies\com.github.mnestor\ssoossh`,
following the conventional `Policies\<Vendor>\<Product>` layout with
`com.github.mnestor` as the vendor key -- the same identifier the macOS
preference domain is built from.

`SOFTWARE\Policies` is the conventional location for Group Policy-delivered
settings, and HKLM is writable only by administrators and SYSTEM: the same
trust boundary `%ProgramData%` gives the `enforce` file.

There is no ADMX/ADML template yet, so there is no Group Policy Editor UI.
Push the values above with whatever mechanism already manages the fleet's
registry -- Group Policy Preferences, Intune, or a login script. String
settings are `REG_SZ`; boolean and integer settings are `REG_DWORD`, booleans
as 0 or 1. A value simply absent from the key is unset, the same as an absent
YAML key.

## macOS: managed preferences

The client reads the two plist locations macOS materialises for an installed
configuration profile targeting the preference domain
`com.github.mnestor.ssoossh`:

| Profile scope | Path |
| --- | --- |
| Device-scoped | `/Library/Managed Preferences/com.github.mnestor.ssoossh.plist` |
| User-scoped | `/Library/Managed Preferences/<username>/com.github.mnestor.ssoossh.plist` |

When both exist, the user-scoped file wins for any key it sets -- matching the
user channel outranking the device channel for managed preferences generally.
Push either through an MDM's Custom Settings or Managed Preferences payload
naming that domain (Jamf, Kandji, Mosyle, Apple Business Manager), or with
`profiles install` for local testing.

The client reads the plist files directly rather than going through
`CFPreferencesCopyAppValue`, the officially documented API: that call requires
CGo and a macOS SDK, and the release pipeline cross-compiles the macOS build
from Linux CI with `CGO_ENABLED=0` and no Apple cross-toolchain. The on-disk
location is not a stable public API, but it has been used directly by other
enterprise Mac tooling for years. If the build pipeline ever moves to a native
macOS runner, switching to `CFPreferencesCopyAppValue` -- which would also add
real `CFPreferencesAppValueIsForced` detection -- is worth revisiting.

**Verified on a real Mac:** the per-user directory
(`/Library/Managed Preferences/<username>/`) is not writable by that user, the
same root-and-admin-only write property the Linux and Windows locations have
for `/etc` and HKLM. A user cannot place their own plist there to defeat the
lock.

Still worth confirming on an MDM-enrolled test Mac if this is deployed against
a macOS version not already covered: that the profile really materialises a
plist at these exact paths, and that the user-scope value really does outrank
the device-scope one when both are set.

## Linux

No platform-native mechanism, because the `enforce` file already matches how
Linux fleets are normally provisioned -- configuration management tooling
dropping files under root-owned `/etc`. Point `/etc/ssoossh/ssoossh.yaml` at an
`enforce:` target the same way as on the other platforms.

## See also

- [Client configuration](/ssoossh/guides/client-config/) for what each setting
  means and where a user's own file lives.
- [`ssoossh.yaml` reference](/ssoossh/reference/client-config/) for every key,
  its type and its default.
