# What ssoossh is

**ssoossh** (pronounced *sue-sssh*) is SSO for SSH. Users authenticate through your
existing identity provider via OIDC and receive an SSH certificate for the session,
instead of provisioning, distributing, and rotating long-lived SSH keys.

It is self-hosted and homelab-friendly. The reference configuration uses
[pocket-id](https://github.com/pocket-id/pocket-id) as the OIDC provider and
[lldap](https://github.com/lldap/lldap) for directory lookups.

**Status:** early development. Interfaces and configuration are expected to change.

---

## The problem

SSH keys are bearer credentials with no expiry and no central revocation. Once a
public key lands in an `authorized_keys` file, it works until someone remembers to
remove it. There is no link between "this key is authorized" and "this person still
works here, is still in that group, and is connecting from somewhere we expect."

SSH certificates already solve the mechanics: a host trusts one or more CAs,
certificates carry principals and constraints, and they expire on their own.
Revocation doesn't exist, bit it stops mattering much since certificates are
short-lived enough to expire before a revocation would reach anyone. What is usually
missing is the piece that decides *who gets a certificate, with which principals, for
how long* — and ties that decision to the identity provider you already run.

ssoossh is that piece.

---

## How it works

1. Your `ssh_config` invokes the ssoossh client through `ProxyCommand` or `Match exec`.
2. The client checks for a usable certificate. If none is valid, it generates a fresh
   SSH keypair and sends the public key to the ssoossh server over an HTTPS REST call.
3. The server responds with a URL. The client attempts to open it in the local browser
   (toggleable by command-line flag or in the client config YAML) and prints it either way.
4. The user authenticates at the identity provider via OIDC. The server enriches the
   identity from LDAP if the OIDC response does not carry everything it needs.
5. The client holds a Server-Sent Events stream open while it waits. The wait window is
   configurable server-side and can run up to several minutes (default target: 5 minutes),
   which covers MFA prompts and hardware token taps.
6. On authorization, the server signs the public key into a certificate carrying the
   [principals, critical options, extensions, and validity window](#certificate-terms)
   that policy allows.
7. The client loads the keypair and certificate into the local ssh-agent, or writes the
   private key, public key, and certificate to disk if no agent is available. SSH proceeds.

---

## Components

### ssoossh client

Runs on macOS, Linux, and Windows. Invoked from `ssh_config`, so the user's workflow
stays `ssh host`.

- Manages its own SSH keypair, regenerated each time a new certificate is needed
- Talks to the server over HTTPS REST; waits for issuance over SSE
- Prefers the local ssh-agent; falls back to key files when no agent is present
- Configured by YAML, with command-line overrides

### ssoossh server

The trust anchor and the policy decision point.

- Authenticates users via OIDC
- Optionally enriches identity from LDAP when the OIDC response is not sufficient
- Maps identity and request context to certificate contents — principals, critical
  options, extensions, and validity
- Signs the client's public key with the CA key
- Serves a web UI where users confirm issuance, particularly for service certificates

**Web UI.** Beyond confirming issuance, the web UI gives users a view of their own
certificate history: what they approved, when, and whether the certificate actually
reached the requesting client or the delivery failed. Per-certificate detail — the
principals, critical options, extensions, and validity that were issued — is available
as well. That level of verbosity is more than most users want, so it is not the
default: it is exposed either through a user-configurable setting, or gated on group
membership from OIDC/LDAP, or both. *Which of those is the right control is still open.*

**CA key custody.** In the first version the CA key lives in an ssh-agent that the
server process has access to, keeping the private key out of the server's own address
space. Later versions are planned to support PKCS#11 / HSM, cloud KMS, and other
custody backends behind the same signing interface.

### pam_ssoossh

A Linux PAM module for the `auth` management group, scoped to `sudo` and `su`. SSH
login is not in scope — that path is already handled by certificate-based SSH auth.
Console login is out of scope entirely for the current design.

It generates a keypair, requests a certificate, then validates the certificate's
principals and the signer against the configured CA. It retains neither the key nor the
certificate. Certificates issued for this path are deliberately very short-lived —
on the order of 30 to 60 seconds, comparable to a hardware token code — and the request
carries a nonce so a captured certificate cannot be replayed.

**Possible future direction: console login.** A separate module could cover console
login by displaying a short code the user types into the ssoossh website to
authenticate — no browser or network access needed on the console itself. That would
put every authentication path on the system behind OIDC. It is a distinct problem from
the `sudo`/`su` case and is not part of the current scope.

---

## Certificate types

| Type | Purpose | Notes |
| --- | --- | --- |
| **User** | Interactive SSH sessions | Principals and constraints derived from OIDC claims and LDAP groups |
| **Host** | Server identity | Removes `known_hosts` churn and TOFU prompts |
| **Service** | Non-interactive SSH — scheduled jobs, file transfer, remote process invocation | User-type certificate; enrolled once, then reissued unattended against a persistent keypair |

Service certificates are requested with explicit options, either passed by the client
or set by the user in the web UI at the point of authorized issuance. Requestable
options include the [critical options and extensions](#certificate-terms) defined in the
SSH certificate specification — notably `force-command` and `source-address`, plus the
extension set governing pty allocation, agent forwarding, port forwarding, X11, and
user rc execution. Policy sets the ceiling; the request can only narrow it.

### Service certificate enrollment

Service certificates involve a user exactly once, at enrollment, and never again:

1. A user invokes the ssoossh client to start enrollment. A keypair is established for
   the service account, either supplied by the operator or generated by the client (see
   below). The invocation carries the options the certificate should be allowed to
   request — principals, `force-command`, `source-address`, the extension set, and
   `no-touch-required` where the enrolled key is a hardware-backed `sk-` type.
2. The user authenticates via OIDC and reviews the request in the web UI, where those
   options can be adjusted or overridden before the code is issued. What the command
   line asks for is a starting point, not the final answer.
3. The server returns an enrollment code bound to that specific public key and to the
   authorized option set — intersected against the server configuration, which is the
   ultimate authority on which options exist at all (see below).
4. Every later invocation presents the code and reuses the *same* keypair — no new key
   is generated, and no browser or user interaction is involved.

**The server config gates everything.** Neither the command line nor the web UI can
introduce an option the server has not made available. The server configuration defines
the full set of options that may ever be issued in this deployment, and every request is
intersected against it before anything is signed. The client request asks, the
authorizing user in the web UI narrows or adjusts, and the server config sets the outer
bound that neither can exceed. Options the deployment does not permit are trimmed rather
than treated as an error, and the web UI shows what was removed alongside what remains,
so the user approves the certificate they are actually going to get.

**Approval prompt.** The URL the user opens presents an approval prompt after
authentication, showing the options that will be issued and anything that was trimmed.
Whether that prompt is mandatory is a server setting: it can be forced, or users can be
allowed to bypass it. The default is to force approval.

**Confirmation page.** After approval, the user lands on a confirmation page listing the
details of the certificate that was issued — its principals, critical options,
extensions, and validity — along with any options that were trimmed. This is a record of
what was actually signed rather than a repeat of what was requested, so the two can be
compared after the fact.

The code alone is not sufficient to obtain a usable certificate: the certificate is
issued against the enrolled public key, so an attacker who steals the code still needs
the corresponding private key.

**Key custody is the operator's choice.** Enrollment accepts either an existing public
key or a request to generate a keypair:

- **Bring your own key.** The operator supplies a public key and ssoossh never sees the
  private half. Whether it lives on an HSM, a PKCS#11 token, or an encrypted file on
  disk is outside ssoossh's concern and deliberately so — the server signs a public key
  and has no business knowing how the private key is protected. An enrolled key may be a
  hardware-backed `sk-` type, in which case the certificate can carry
  `no-touch-required` so the job runs without a physical touch it cannot provide.
- **Generated by the client.** ssoossh creates the keypair, with passphrase protection
  of the private key as a configurable option.

**Private keys never leave the machine that holds them.** This holds for every
certificate type, not just service certificates. The server receives public keys only —
it has no endpoint that accepts a private key and no reason to want one. The client
hands a generated private key to the local ssh-agent or writes it to a local file, and
nowhere else. A compromised ssoossh server can issue certificates it should not, which
is a real risk worth designing against, but it cannot leak private keys it never
received.

The SSH RFCs do not constrain this either way:
[RFC 4253](https://www.rfc-editor.org/info/rfc4253/) defines public key algorithm names
and the wire format of key blobs and signatures, but says nothing about how a private
key is protected at rest. That is implementation territory, which is exactly why the
bring-your-own-key path is the more flexible of the two.

---

## Certificate lifetime and context policy

**Design in progress.** Lifetime is not a single global setting. The server evaluates
each request against a policy and derives a validity window from it, so that a
certificate's lifetime reflects how much confidence the issuance context justifies.

### Framing

In zero trust terms (NIST SP 800-207), the ssoossh server is the policy decision point
and the target host's sshd is the policy enforcement point. A certificate is a cached
authorization decision, and its validity window is how long that decision is allowed to
stand without re-evaluation. Weaker context should not mean "denied" by default and
should not mean "same certificate as everyone else" either — it should mean a shorter
window, tighter constraints, or both.

### Signals under consideration

- **Client source network.** Which zone the certificate request originates from.
- **Browser/client correlation.** Whether the OIDC authentication plausibly happened on
  the same host that will use the certificate. Establishing this without a listening
  socket on the client is genuinely hard: the practical signals are comparing the source
  address of the client's REST request against the source address of the browser session
  that authorizes it, and the client's own report of whether it successfully launched a
  local browser. The first cannot distinguish same host from same NAT; the second is
  self-attested and trivially forged by a modified client. Treat this as a
  confidence hint that can *shorten* a lifetime when absent, not as proof that can
  extend one when present.

  *Rejected:* a loopback redirect to `127.0.0.1` would prove co-location, but it
  requires the client to bind a listening port. That is not a cost this design accepts
  for a signal this soft — the browser lands on the ssoossh server and the client learns
  the outcome over the SSE stream it already holds open.
- **Group membership.** OIDC claims and LDAP groups, as a ceiling on both lifetime and
  the principals available.
- **Certificate type and requested options.** Service certificates and anything with
  `force-command` or broad principals warrant separate treatment from ordinary
  interactive user certificates.

### Policy mechanics being considered

- Server-side configuration only. Clients may request narrower, never broader.
- Default deny; rules grant explicitly.
- Each matching rule contributes a maximum lifetime plus allowed principals, critical
  options, and extensions. The effective result is the intersection: shortest lifetime,
  narrowest principal set.
- Pair short lifetimes with the `source-address` critical option, so a certificate is
  bound to the network it was issued for rather than only to a clock. NAT complicates
  this for user certificates: the address the server sees is the NAT's public address,
  not the address the client will connect to internal hosts from, so binding to either
  one alone breaks a legitimate connection path. The intended approach is for the client
  to send the addresses configured on its own interfaces, with the server adding the
  address it observed the request coming from, and the union going into
  `source-address`. *Still to settle:* the client-supplied list is unverified input, so
  it needs a policy ceiling — a client claiming `0.0.0.0/0` should get a rejection, and
  ranges outside the zones a rule permits should be dropped rather than trusted.
- Cap cumulative session lifetime independently of individual certificate lifetime, so
  silent renewal against a live OIDC session cannot extend indefinitely.
- Log the signals behind every issuance decision, not just the outcome.

---

## Certificate terms

Definitions follow the SSH certificate format
([draft-miller-ssh-cert-01](https://www.ietf.org/archive/id/draft-miller-ssh-cert-01.html)).
OpenSSH implements this format and adds options of its own; where that matters, it is
noted.

**Principal** — a certificate carries a *list* of principals, each one a name the
certificate is valid for. Not the same thing as a user account, though the values often
look like usernames. For a user certificate, a principal is a name the holder may
authenticate *as* on the target host; sshd accepts the certificate if the requested
login name is in the list (or is mapped to one via `AuthorizedPrincipalsFile` /
`AuthorizedPrincipalsCommand`). The values are opaque strings as far as the protocol is
concerned, so the list can hold whatever identifier the target hosts key off — a Unix
username, a UPN, a `sAMAccountName`, or any other account-linking attribute pulled from
LDAP. That flexibility is what lets one certificate work across hosts that disagree
about what a user is called. For a host certificate, principals are hostnames the
certificate vouches for. A certificate with an empty principal list is valid for *any*
principal — ssoossh never issues one.

**Critical option** — a constraint that sshd must understand, or it rejects the
certificate outright. Fail-closed, which makes these the right place for security
restrictions. The specification defines two:

- `force-command` — the certificate can only run this command. Whatever the client
  asks to execute is ignored. Useful for service certificates scoped to a single job.
- `source-address` — a comma-separated list of CIDR ranges the certificate may be
  used from. Binds the certificate to a network, not just to a clock.

OpenSSH adds others, including `verify-required` (the signing key must require user
verification, e.g. a PIN or biometric on a FIDO key). ssoossh does not use it.

**Extension** — an optional capability grant. Unlike critical options, an extension
sshd does not recognize is ignored, so these are fail-open and are for enabling
features rather than restricting them. Absence of an extension means the capability is
denied. The specification defines:

- `permit-pty` — allocate a pty. Without it, no interactive shell.
- `permit-agent-forwarding` — allow `ssh -A`.
- `permit-port-forwarding` — allow `-L` / `-R` / `-D`.
- `permit-X11-forwarding` — allow X11 forwarding.
- `permit-user-rc` — run the user's `~/.ssh/rc` on login.

OpenSSH adds `no-touch-required` for FIDO keys. ssoossh does not offer it for
client-generated keys: it only has meaning for hardware-backed `sk-` key types, and
generated keys are ordinary software keypairs, so the setting would have nothing to act
on. It is relevant on the service certificate path, where an operator may enroll an
`sk-` key that would otherwise demand a touch no unattended job can supply.

**Validity interval** — the *valid after* and *valid before* timestamps. This is the
certificate lifetime discussed above, and it is enforced by the target host against its
own clock, which is why host time sync matters.

**Key ID** — a free-form string the CA stamps into the certificate. sshd logs it on
every authentication, so it is the audit trail: ssoossh puts identity and issuance
context here.

**Serial number** — a CA-assigned integer, mainly useful for revoking a single
certificate through a key revocation list if that feature is ever adopted.

---

## References

- SSH certificate format — [draft-ietf-sshm-cert](https://datatracker.ietf.org/doc/draft-ietf-sshm-cert/)
- SSH Transport Layer Protocol — [RFC 4253](https://www.rfc-editor.org/info/rfc4253/) — public key algorithm names, key blob and signature wire formats
- PAM specification — [RFC 86.0](https://github.com/linux-pam/linux-pam/blob/master/doc/specs/rfc86.0.txt)
- [pocket-id](https://github.com/pocket-id/pocket-id) — OIDC provider used in the reference configuration
- [lldap](https://github.com/lldap/lldap) — LDAP directory used in the reference configuration
