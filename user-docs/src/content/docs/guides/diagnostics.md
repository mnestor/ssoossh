---
title: Diagnostics
description: The -v and --debug flags, what each one prints, and what to attach when asking for help.
eyebrow: User guide
sidebar:
  order: 5
---

Two flags answer the two different questions you can have about a failing
client: what did it do, and what did it think it was configured with. Both
write to stderr only, so neither disturbs a `ProxyCommand` relay or a
certificate on stdout, and both have an environment equivalent for the
invocations whose command line is not yours to edit -- an `ssh_config`
`Match exec` line, a cron entry, a systemd unit.

| | Flag | Environment | Answers |
| --- | --- | --- | --- |
| Trace | `-v`, `-vv`, `-vvv` | `SSOOSSH_VERBOSE=1..3` | what the client did, in order |
| Report | `--debug` | `SSOOSSH_DEBUG=1` | what it resolved, and from where |

A value in either variable that is not a number (`SSOOSSH_VERBOSE`) or a
boolean (`SSOOSSH_DEBUG`) reads as off. A diagnostic aid must never be the
reason a login fails.

## `-v`: what the client did

The ladder mirrors `ssh`'s own, which matters for a tool you invoke from
`ssh_config`.

| Level | Adds |
| --- | --- |
| `-v` | The high-level steps: starting, configuration loaded, key storage resolved |
| `-vv` | Requests and file operations |
| `-vvv` | Bodies |

More `v`s clamp at three rather than erroring: someone reaching for `-vvvvv`
wants as much as there is.

```bash
ssoossh -vv ssh login 2> ssoossh.log
```

When `ssh` is the one invoking the client:

```bash
SSOOSSH_VERBOSE=2 ssh bastion.example.com 2> ssoossh.log
```

Output is plain text with no timestamps -- the audience is a person reading a
terminal or pasting into a bug report, and the ordering is the informative part.

## `--debug`: what it resolved

`--debug` prints a configuration report and then implies `-v`, so one flag
gives both the report and the steps that followed it. The report has four
parts:

**Identity of the invocation.** The version, platform, the command path, and
the working directory -- which is what decides where the local config file is
looked for, so a surprising entry in the chain below usually has its
explanation here.

**The config sources, in the order they were applied**, each overriding the
ones above it, with what came of each:

| Status | Meaning |
| --- | --- |
| `merged` | The source contributed values |
| `absent` | The file was not there, which is normal everywhere except a file you named yourself |
| `error` | The file existed but could not be read or parsed, so its settings are silently not in effect |
| `not given` | An optional source was not selected at all, such as `--config` or `enforce` |

Entries marked with `*` are administrator locks -- the `enforce` file and
platform-native policy -- which override everything above them including your
own config file and any command-line flag. The `error` status is the case this
report exists for: a config file you believe is in effect but which was skipped.

**The resolved settings**: server, TLS verification, key type and size, FIPS
steering, the storage backend, the key file name, whether a browser will be
opened, a short form of the CA public key, and any policy-forbidden
certificate extensions.

**Key storage**: `use_agent` and `fallback_file_agent` as configured, the
backend that actually resolved, `SSH_AUTH_SOCK`, and the three key files with
their resolved paths and whether each exists (with size and mode when it does,
or the real error when it cannot be read). The preference and the outcome are
both shown because the interesting bug is when they disagree: `use_agent: true`
with a file backend means the agent was unreachable and `fallback_file_agent`
caught it.

```bash
ssoossh --debug ca
```

```bash
SSOOSSH_DEBUG=1 ssh bastion.example.com
```

The report is deliberately printed even when startup failed, which is when it
is most wanted -- the sources list is populated before most of the things that
can fail.

:::note
`--debug` is hidden from `--help` because it is a diagnostic aid rather than
part of the command's advertised surface. It is supported, not secret.
:::

### Run it on the command you are diagnosing

Some of what the report says is decided by the command it ran on.
`ssoossh ssh config --debug` needs no server but leaves key storage and the CA
unresolved, because an offline command never sets either up. `ssoossh --debug ca`
is the smallest invocation that resolves everything.

`--debug` is also the only place resolved settings are reported. There is no
"show me the config" command printing a shorter version: two commands answering
"what is in effect" with different amounts of truth is a maintenance trap, and
the shorter one is always the one that goes stale. `ssoossh ssh config` prints
the `ssh_config` recipes and nothing more.

## Two more things worth checking

```bash
ssoossh ssh inspect
```

Prints what each held certificate actually grants -- principals, key ID, type,
expiry, serial, extensions, critical options. This is the answer to "I have a
certificate, so why does the host refuse it": compare its principals against
the account you are connecting as.

```bash
ssoossh version
```

Prints version, commit, and build info. Worth including in a report, and it
needs no server.

## What to send when asking for help

Re-run with `-v` and attach the stderr. That is the flag to reach for first.

```bash
ssoossh -vv ssh login 2> ssoossh.log
```

Add `--debug` when the problem looks like the wrong configuration rather than
the wrong behavior: the wrong server, a config file you expected to be picked
up and was not, a key file it cannot find.

```bash
SSOOSSH_VERBOSE=2 SSOOSSH_DEBUG=1 ssh bastion.example.com 2> ssoossh.log
```

Include `ssoossh version` and, if the connection gets as far as the target
host, `ssh -v` output from the same attempt.

:::caution
Read the log before sending it. At `-vvv` it contains request bodies, and at
any level it names your server, your username, and your file paths.
:::

## Where to go next

- [Client configuration](/ssoossh/guides/client-config/) -- what the source
  chain in the report means.
- [The ssoossh client](/ssoossh/guides/client/) -- every command and flag.
- [User FAQ](/ssoossh/guides/faq/).
