---
title: Why ssoossh
description: The three gaps ssoossh and pam_ssoossh close, from long-lived SSH keys to hardware tokens that cannot reach a virtual console.
sidebar:
  order: -1
---

Most fleets have an identity provider with multi-factor authentication in front
of every web application, and then a pile of SSH keys and local passwords that
know nothing about it. ssoossh closes that gap for `ssh`, and `pam_ssoossh`
closes it for `sudo`, `su`, and console logins.

## Three gaps

**Long-lived keys outlive the person.** An `authorized_keys` entry proves
someone holds a private key, and nothing re-checks who that is. It does not
expire, it is copied by whoever holds it, and revoking it means editing a file
on every host it ever reached.

**MFA stops at the SSH door.** Your identity provider enforces a passkey, a
smartcard, or a push for the wiki. The bastion asks for a key, and `sudo` on the
far side asks for a password. Neither one has ever heard of that factor.

**Hardware tokens cannot reach a virtual console.** A token authenticates by
being physically attached to the machine doing the authenticating. That
assumption fails the moment the machine is a VM in a hypervisor UI, a BMC or
KVM viewer, a serial line, or a crash cart. There is no port to put anything
into, and USB passthrough puts the reader on the hypervisor rather than in the
hand of the person logging in.

## What ssoossh does about it

- **The certificate replaces the key entry.** You sign in at your identity
  provider, a human approves the request, and the server signs your public key
  into a certificate that expires on its own. Hosts trust one CA public key
  instead of a file per account.
- **The factor is inherited, not reimplemented.** ssoossh has no MFA of its
  own. Whatever your identity provider already enforces at sign-in is what
  guards the login behind it.
- **The private key never leaves you.** The client generates the keypair and
  loads the result into your ssh-agent. The server only ever sees public keys.
- **The log names a person.** `sshd` records the certificate key ID, which
  names the identity and the request behind it, not an anonymous fingerprint.
  That holds even on a shared operations account.

## What pam_ssoossh does about it

`pam_ssoossh` is an `auth` module. It generates an ephemeral keypair, requests a
certificate for this one attempt, validates it, and discards everything. Nothing
is left on disk.

- **`sudo` and `su` over SSH** open a browser at the identity provider instead
  of prompting for a local password.
- **A console with no browser** gets an eight-character code, and a QR code
  where the terminal font allows it, so a BMC viewer is photographed rather
  than transcribed. You approve from the phone or laptop in your hand.
- **The approval says what it is approving.** The page names the host, the
  service, the tty, the account, and the command line, so an approval is a
  decision rather than a reflex.

The inversion is the whole point: the factor stays with the person, and the
machine being logged into needs only enough screen to print eight characters
and a route to `ssoosshd`. A VM with no ports gets the same factor as a laptop
with a token in it.

## Where tokens still win

| Situation | Hardware token | `pam_ssoossh` |
| --- | --- | --- |
| At the keyboard of a physical machine | yes | yes |
| `sudo` over SSH to a remote host | only by forwarding the reader or the agent | yes |
| VM console, BMC, KVM, or serial line | no | yes |
| The host cannot reach the network | yes | no |
| Unattended, nobody at a keyboard | no | no: use [service certificates](/ssoossh/concepts/service-certificates/) |

The last two rows are why the module is wired `sufficient` and the local stack
stays underneath it. `pam_ssoossh` is a factor added in front of a host's
existing authentication, not a replacement for it: a denial, a timeout, or an
outage falls through to what follows. Hosts whose local logins already carry a
token keep it, and send remote `sudo` through the browser flow with
[`ssh-only`](/ssoossh/hosts/pam/reference/#ssh-only).

## Read next

- [Getting started](/ssoossh/getting-started/) -- the shortest path to a working
  `ssh` login.
- [Passwords, tokens, and pam_ssoossh](/ssoossh/concepts/pam-factors/) -- the
  full comparison, including enrollment, revocation, and phishing.
- [Keys and certificates compared](/ssoossh/concepts/keys-and-certificates/) --
  the same argument for SSH login rather than local authentication.
- [How ssoossh works](/ssoossh/concepts/) -- the components and each flow end to
  end.
