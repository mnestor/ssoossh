# Client settings enforcement

`ssoossh` client settings can be locked so a user cannot override them, via
three mechanisms — one per platform's native fleet-management tooling, plus
the cross-platform `enforce` YAML file. All are guardrails, not a security
boundary: the client runs as the user, who can always supply their own
binary. The one setting that's actually enforced beyond the client's own
cooperation is `cert_options.*.valid_duration` on the server. See
`docs/ssoossh.yaml.default` for that caveat in full.

## Precedence

Lowest to highest — each source's value is overridden by any later one that
also sets the same key:

1. Built-in defaults (`client/config/defaults.yaml`)
2. User config (`~/.config/ssoossh.yaml` / `%AppData%\ssoossh\ssoossh.yaml`)
3. Local config (`./ssoossh.yaml`, or `--config`)
4. CLI flags (`--server`, `--key-type`, `--key-size`)
5. The `enforce` file named by the machine-wide config
   (`/etc/ssoossh/ssoossh.yaml` or `%ProgramData%\ssoossh\ssoossh.yaml`)
6. Platform-native policy: the Windows registry key or macOS managed
   preferences described below

A value simply absent from a source is left to whatever the next source
down sets — a policy source that only locks `server`, for example, leaves
every other setting free.

Note that `server`, `sshkey.type`, and `sshkey.size` are also the three
settings `ssh login` accepts as CLI flags (`--server`, `--key-type`,
`--key-size`). Because those flags are bound into viper as flags rather
than merged as config, they take precedence over every config-file-tier
source above — including `enforce` and platform policy — if the user
actually passes them. This is pre-existing behavior, not something this
document's mechanisms change, and it's the same "guardrail, not a
boundary" situation as running your own binary: covered by the caveat at
the top of this document, not a gap to fix here.

## Settings

Every setting below can be set via the `enforce` YAML file, the Windows
registry, or macOS managed preferences — same setting, three delivery
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

`fips: true` from any locked source (the `enforce` file or a platform
policy) makes a non-FIPS-approved `sshkey.type` a hard error at startup —
see `client/config/sshkey.go`.

## Windows: Group Policy

The client reads `HKLM\SOFTWARE\Policies\ssoossh`. `SOFTWARE\Policies` is
the conventional location for Group Policy-delivered settings, and HKLM is
writable only by administrators/SYSTEM — the same trust boundary
`%ProgramData%` gives the `enforce` file.

There's no ADMX/ADML template (yet) for a Group Policy Editor UI. Push the
values above with whatever mechanism already manages the fleet's registry:
Group Policy Preferences, Intune, or a login script. String settings are
`REG_SZ`; boolean and integer settings are `REG_DWORD` (booleans as 0/1).
A value simply absent from the key is unset, same as an absent YAML key.

## macOS: managed preferences

The client reads two plist locations that macOS materializes for an
installed configuration profile targeting the preference domain
`com.mnestor.ssoossh`:

- **Device-scoped** profile: `/Library/Managed Preferences/com.mnestor.ssoossh.plist`
- **User-scoped** profile: `/Library/Managed Preferences/<username>/com.mnestor.ssoossh.plist`

When both exist, the user-scoped file wins for any key it sets, matching
the user channel outranking the device channel for managed preferences
generally. Push either via an MDM's Custom Settings/Managed Preferences
payload naming that domain (Jamf, Kandji, Mosyle, Apple Business Manager,
or `profiles install` for local testing).

This reads the plist files directly rather than going through
`CFPreferencesCopyAppValue` (the officially documented API): that call
requires CGo and a macOS SDK, and this repo's release pipeline
cross-compiles the macOS build from Linux CI with `CGO_ENABLED=0` and no
Apple cross-toolchain configured (see `.goreleaser.yml`). The on-disk
location isn't a stable public API, but it has been used directly by other
enterprise Mac tooling for years. If the build pipeline ever moves to a
native macOS runner, switching to `CFPreferencesCopyAppValue` (which would
also add real `CFPreferencesAppValueIsForced` detection) is worth
revisiting.

**Verified on a real Mac**: the per-user directory
(`/Library/Managed Preferences/<username>/`) is not writable by that
user — the same root/admin-only write property the Linux/Windows locations
have for `/etc` and `HKLM`. A user cannot place their own plist there to
defeat the lock.

Still worth confirming on an MDM-enrolled test Mac if this is deployed
against a macOS version not already covered: that the profile actually
materializes a plist at these exact paths, and that the user-scope value
really does outrank the device-scope one when both are set.

## Linux

No platform-native mechanism — the `enforce` file already matches how
Linux fleets are normally provisioned (config management tooling dropping
files under root-owned `/etc`). Point `/etc/ssoossh/ssoossh.yaml` at an
`enforce:` target the same way on Linux as on the other platforms; see the
`enforce` comment in `docs/ssoossh.yaml.default`.
