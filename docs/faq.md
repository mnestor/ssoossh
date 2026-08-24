# FAQ

Grouped by who is asking: people using ssoossh to connect, admins of the
target hosts that accept the certificates, and operators of the ssoossh
server itself. If your question is of the form "why doesn't it just...",
check [decisions.md](decisions.md) first; that document exists for
exactly those.

## Users

### Do I have to replace ssh?

No. Your existing `ssh` invokes the ssoossh client through a line or two
of `ssh_config` ([configuration.md](configuration.md#ssh_config)), and
from there everything is standard OpenSSH certificate authentication.
`ssoossh ssh config` prints those lines; it needs no server, so it answers
before anything is set up. The illustrated
[walkthrough.html](walkthrough.html) shows what a login looks like.

### Do I get a browser prompt every time I ssh?

No. A valid certificate is reused until it expires, so one browser
approval typically covers a workday. The certificate is checked once at
session start; an established SSH session does not drop when the
certificate expires.

### Do I need an ssh-agent?

Only for `ProxyCommand` mode (`ssh` reads key files once at startup, so a
certificate refreshed on disk after that goes unseen). With the
recommended `Match exec` mode, key files on disk work fine without an
agent. [configuration.md](configuration.md#ssh_config).

### Can I approve from a different device than the one running ssh?

Yes. The authorization URL can be opened in any browser; the client just
waits for the outcome on its stream. Be aware the lifetime policy may
correlate the browser's address with the client's, and weak correlation
can shorten the issued lifetime (never lengthen it)
([certificate-lifetime-policy.md](certificate-lifetime-policy.md)).

### Does it work on Windows and macOS?

Yes: macOS, Linux, and Windows, including Pageant and the WSL relay. The
macOS binary is signed and notarized.
[getting-started.md](getting-started.md) has the install steps.

### It is not working. What do I send you?

Re-run with `-v` and attach the stderr. That is the flag to reach for
first; `-vv` adds requests and file operations, `-vvv` adds bodies.

```sh
ssoossh -vv ssh login 2> ssoossh.log
```

If `ssh` is the one invoking the client, its command line is not yours to
edit, so use the environment variable instead:

```sh
SSOOSSH_VERBOSE=2 ssh bastion.example.com 2> ssoossh.log
```

When the problem looks like the wrong configuration rather than the wrong
behavior — the wrong server, a config file you expected to be picked up and
was not, a key file it cannot find — add `--debug` (or `SSOOSSH_DEBUG=1`).
That prints every config source in merge order with what came of each, the
settings that resulted, and where the key files resolve to. It prints even
when startup failed, and it is the only place those settings are reported.
Details: [configuration.md](configuration.md#diagnostics--v-and---debug).

Both write to stderr only, so neither disturbs a `ProxyCommand` relay or a
certificate on stdout. Read the log before sending it: at `-vvv` it contains
request bodies, and it always names your server, username, and file paths.

### Does the server ever see my private key?

No. The client generates the keypair locally and sends only the public
key; the private key goes nowhere except your local ssh-agent or a local
file. This is one of the project's hard invariants
([decisions.md](decisions.md#security-invariants)).

## sshd host admins

### What do I have to configure on my hosts?

One line of `sshd_config`: `TrustedUserCAKeys` pointing at the ssoossh
CA public key, fetched from the running server with `ssoossh ca`
([configuration.md](configuration.md#sshd-on-target-hosts)). Optionally,
map allowed login names with `AuthorizedPrincipalsFile` or
`AuthorizedPrincipalsCommand`. `authorized_keys` files can go away.

### How do I revoke a certificate, or offboard someone?

You don't revoke, and that is deliberate: certificates are short-lived
enough that expiry does the work a revocation list would. Disable the
person in the identity provider; they cannot get a new certificate, and
the current one dies on its own within hours. Nothing on your hosts needs
touching. [decisions.md](decisions.md) covers why there is no revocation
machinery.

### Can certificates be pinned to a source IP?

Service certificates can (services sit still). User certificates are not
pinned: people move between office, VPN, and hotel wifi, and a short
lifetime already covers the risk. What the source network *does* affect
is the lifetime: a request from the office range can get a workday, the
same laptop on hotel wifi gets minutes
([certificate-lifetime-policy.md](certificate-lifetime-policy.md)).

### Can pam_ssoossh lock me out of sudo?

Not if you follow the runbook: with the recommended `sufficient` control
flag, an unreachable ssoossh server falls through to the next module
(normally `pam_unix.so`, so a local password still works). Read the
lockout warning in [deployment.md §8](deployment.md#8-pam-sudo-and-su)
before touching `/etc/pam.d/sudo`, and keep a root shell open while you
do.

### Does the PAM module store anything on disk?

No. Each attempt uses an ephemeral keypair and a certificate valid for
seconds; both are validated once and discarded.

### sudo approvals fail intermittently after working fine

Check the clock. PAM certificates are valid for seconds, and drift beyond
the module's `skew-tolerance` (default 2s) makes valid approvals fail
validity checks. Run NTP (`chronyd`/`systemd-timesyncd`) on every host
using `pam_ssoossh`; widen `skew-tolerance` only as a last resort.
[deployment.md §8](deployment.md#8-pam-sudo-and-su).

### Can my hosts get certificates too?

No. Nothing can verify a host's claim to its hostname, and unverifiable
host identity from the CA that also signs user access is worse than none —
see [decisions.md](decisions.md). That may change if a real
host-verification mechanism (something like an ACME challenge) lands. The client's
`host mapping` and `host principals` commands remain for local
`AuthorizedPrincipalsCommand` mapping; they never talk to the server.

## ssoossh server operators

### Is it production ready?

Early development. User, service, and `sudo`/`su` PAM certificates work
end to end. Interfaces and configuration are expected to change. See
[features.md](features.md) for the status table.

### Which identity providers work?

Any OIDC-compliant provider. The reference configuration uses
[pocket-id](https://github.com/pocket-id/pocket-id); the setup walkthrough
is [deployment.md §4](deployment.md#4-oidc-provider-setup-pocket-id).

### Can I run behind a load balancer?

Yes. Run several `ssoosshd serve api` instances behind the load balancer,
one or more `ssoosshd sign` processes, NATS with mTLS as the message
broker, a shared PostgreSQL database, an explicit `http.cookie_key`, and
`multi_instance: true`. No sticky sessions are needed: approvals,
certificate delivery, and web sessions all cross instance boundaries.
Procedure: [deployment.md §7](deployment.md#7-running-more-than-one-instance);
settings: [configuration.md](configuration.md#multi-instance-and-startup-modes).

### Can I use NATS in single-server mode?

Yes, and there is a good reason to: CA key isolation. A single
`ssoosshd serve api` instance plus a `ssoosshd sign` process, both
connected to NATS, keeps the CA key out of the web tier's memory entirely,
and the signer can live on a separate machine with restricted network
access. You do not need NATS for a plain single-process deployment;
`ssoosshd serve` uses an in-process transport by default. See
[deployment.md §6](deployment.md#6-startup-modes-full-api-and-sign) and
[signing-pipeline.md](signing-pipeline.md) for how the processes and NATS
fit together.

### Can I run behind a reverse proxy (nginx, Caddy, Traefik)?

Yes, and it is the common case. Two settings must be right or login fails
silently: `http.public_url` (the OIDC redirect URI and CSRF origin check
derive from it) and `http.trusted_proxies` (or `X-Forwarded-For` is
ignored). See [configuration.md](configuration.md#tls) and
[deployment.md §5](deployment.md#5-reverse-proxy-and-tls).

### SQLite or PostgreSQL?

SQLite for a single instance (the default; put the file in
`/var/lib/ssoossh/` under systemd). PostgreSQL is required for
multi-instance. [configuration.md](configuration.md#database).

### What happens if NATS goes down, or an instance crashes?

Nothing is lost that matters: the flow is short and interactive, so the
human is the retry mechanism. A client waiting on a lost delivery keeps
waiting, then re-requests after the request TTL; the person who approved
sees their terminal still waiting and reruns login. This is a deliberate
at-most-once design; see the failover notes in
[deployment.md §7](deployment.md#7-running-more-than-one-instance) and the
JetStream entry in [decisions.md](decisions.md).

### Where are issued certificates stored?

Nowhere. The server never persists a signed certificate; delivery to the
waiting client is the only copy, so there is no certificate store to
steal. A client that misses the delivery window simply re-requests.

### Can a compromised web tier, or a rogue admin, widen access?

No. The config file is the outer bound: nothing reachable over HTTP can
make issuance more permissive than the loaded configuration allows. Admin
is an OIDC group named in config, not a database flag, and an admin
cannot approve someone else's request, raise a ceiling, or grant admin.
A compromised web tier can deny service, not escalate.
([decisions.md](decisions.md), [features.md](features.md)).

### Where does the CA private key live?

In an ssh-agent the server process reaches, never in the config-parsing
web tier's memory when you run the split signer. For stronger isolation,
run `ssoosshd sign` on its own machine
([deployment.md §6](deployment.md#6-startup-modes-full-api-and-sign)).
HSM/PKCS#11/cloud-KMS signing is planned behind the same interface.

### Can I lock down client settings across a fleet?

Yes: an `enforce` file, Windows Group Policy, or macOS managed
preferences. These are guardrails, not a security boundary; the only
setting enforced beyond client cooperation is the server-side
`valid_duration` ceiling.
[client-settings-enforcement.md](client-settings-enforcement.md).

### The identity provider rejects the redirect URI

Almost always `http.public_url`: it must be the scheme and host browsers
actually reach the deployment at, because the OIDC redirect URI is
derived from it. [configuration.md](configuration.md#tls).

### Every request shows the proxy's IP in the audit trail

`http.trusted_proxies` is unset, so `X-Forwarded-For` is ignored. Set it
to the proxy's CIDR. [deployment.md §5](deployment.md#5-reverse-proxy-and-tls).

### ssoosshd refuses to start in api or sign mode

Both modes require `pubsub.backend: nats` with complete mTLS credentials;
they fail closed on the in-process backend because signing jobs could not
cross processes. Similarly, `multi_instance: true` without an explicit
`http.cookie_key` fails at startup.
[configuration.md](configuration.md#multi-instance-and-startup-modes).
