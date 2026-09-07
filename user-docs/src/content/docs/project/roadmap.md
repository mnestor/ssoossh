---
title: Roadmap
description: What is designed but not built, and where each design lives.
eyebrow: Project
sidebar:
  order: 2
---

What is designed but not built. This page is for deciding whether to wait for
something or work around it.

:::caution[Designs, not features]
Everything under "Coming later" and "Outstanding designs" is a design, not a
feature, except where a row says which part has shipped. Designs are kept in
the repository under `docs/proposals/`; each one states its own status and the
commit its `file:line` anchors were verified against, because those anchors
drift. The last table on this page lists the designs that have been built, and
the site page that now describes each. For what exists today, see
[How it works](/ssoossh/concepts/).
:::

## Coming later

- **Certificate lifetime policy rework** -- untangling source-address
  pinning from the lifetime rule, which today welds two unrelated policy
  questions onto one list
  ([design](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/certificate-lifetime-policy-rework.md)).
- **Source-address restrictions** -- approver-chosen pinning and a
  retrieval allowlist, superseding `pin_source_address`
  ([design](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/source-address-restrictions.md)).
- **Service retrieval anomaly policy** -- alerting on, and locking, an
  enrollment code redeemed from too many source networks
  ([design](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/service-retrieval-anomaly-policy.md)).
- **Config coordination** -- detecting and reporting configuration
  divergence between instances sharing a database and a NATS cluster
  ([design](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/config-coordination.md)).
- **Cloud KMS signing**, behind the same key-source interface the config
  and PKCS#11 backends use today.
- **QR-code approval at the console**, so the verification URL can be
  photographed instead of typed. The server already returns the short
  `/c/<code>` URL a QR has to encode; drawing it is the console module's
  half
  ([design](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/console-login-pam.md)).
- **Push approval to a registered device**, deferred rather than rejected:
  request creation is unauthenticated, so an opt-in, per-target-user rate
  limit has to come first
  ([design](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/console-login-pam.md)).
- **Host certificates**, only if a secure host-verification mechanism
  (something like an ACME challenge) makes hostname claims provable --
  see [Decisions](/ssoossh/project/decisions/).

## Outstanding designs

Nothing below has been built, except where a row says which part has.

| Design | What it covers |
| --- | --- |
| [source-address-restrictions.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/source-address-restrictions.md) | Approver-chosen source-address pinning and a retrieval allowlist |
| [service-retrieval-anomaly-policy.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/service-retrieval-anomaly-policy.md) | Alerting and locking an enrollment code redeemed from too many source networks |
| [config-coordination.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/config-coordination.md) | Detecting and reporting configuration divergence between instances |
| [gui-client-approval-flow.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/gui-client-approval-flow.md) | Approving for a GUI SSH client, which has no terminal to print the URL to |
| [console-login-pam.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/console-login-pam.md) | Console login behind the identity provider: a typed code or terminal QR instead of a URL nobody can copy. Server half built; QR and push still deferred |
| [certificate-lifetime-policy-rework.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/certificate-lifetime-policy-rework.md) | Untangling source-address pinning from the lifetime rule, and runtime-editable policy. Partly overtaken: see the doc |
| [ldap-gssapi-bind.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/ldap-gssapi-bind.md) | Binding to the directory with a Kerberos keytab instead of a static password |
| [enhancements.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/enhancements.md) | Small feature modifications logged for later, each too small for its own doc |

## Designs that have been built

Kept for the reasoning behind each decision, which the operator-facing
references deliberately do not carry. Read the site page instead; the design
is there for why, not for what.

| Design | Now documented in |
| --- | --- |
| [claim-driven-certificate-policy.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/claim-driven-certificate-policy.md) | [Certificate lifetime policy](/ssoossh/operations/certificate-policy/) |
| [audit-log.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/audit-log.md) | [Audit log](/ssoossh/operations/audit-log/) |
| [ldap-enrichment-and-sync.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/ldap-enrichment-and-sync.md) | [LDAP enrichment](/ssoossh/operations/ldap/) |
| [enrollment-group-ownership.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/enrollment-group-ownership.md) | [Service certificates](/ssoossh/concepts/service-certificates/) |
| [notification-kinds-expansion.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/notification-kinds-expansion.md) | [Email notifications](/ssoossh/operations/email-notifications/) |
| [pam-principal-source.md](https://github.com/mnestor/ssoossh/blob/main/docs/proposals/pam-principal-source.md) | [The approval page](/ssoossh/guides/approving/#the-approval-page): a PAM or console approver picks the principals from their own accounts |
