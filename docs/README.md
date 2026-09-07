# ssoossh documentation

**ssoossh** (pronounced *sue-sssh*) is SSO for SSH: users authenticate via
OIDC and receive short-lived SSH certificates instead of managing long-lived
keys. Self-hosted, homelab-friendly, and released on a 1.x line.

**The documentation is at <https://mnestor.github.io/ssoossh/>.** It is built
from `user-docs/` in this repository and covers users, host administrators,
server operators, and the internals, with the configuration, CLI, and HTTP API
references generated from the source.

This directory used to hold a second copy of that material. It no longer does:
one copy in two places drifts, and the site is the copy people read. What is
left here is what has no site page.

| Path | What it is |
| --- | --- |
| [proposals/](proposals/) | Designs for work not yet built |
| [dev/](dev/) | Contributing and testing notes |
| [man/](man/) | Man pages (`.1` client, `.5` config formats, `.8` server) |
| [openapi.yaml](openapi.yaml) | The HTTP API wire contract |
| [wire-contract.json](wire-contract.json) | The versioned wire contract manifest |

## Where the old pages went

Every page removed from here has a page on the site. Nothing was deleted
without a replacement.

| Was | Now |
| --- | --- |
| `guide/getting-started.md` | [Getting started](https://mnestor.github.io/ssoossh/getting-started/) |
| `guide/walkthrough.html` | [Walkthrough](https://mnestor.github.io/ssoossh/concepts/walkthrough/) |
| `guide/features.md`, `guide/flows.md` | [How it works](https://mnestor.github.io/ssoossh/concepts/) |
| `guide/faq.md` | [FAQ](https://mnestor.github.io/ssoossh/guides/faq/), split by audience across the user, host and operator sections |
| `operations/configuration.md` | [Configuration reference](https://mnestor.github.io/ssoossh/reference/config/), generated from the config structs |
| `operations/deployment.md` | [Install](https://mnestor.github.io/ssoossh/operations/install/) and the operations section, split one page per topic |
| `operations/certificate-lifetime-policy.md` | [Certificate policy](https://mnestor.github.io/ssoossh/operations/certificate-policy/) |
| `operations/certificate-keyid-template.md` | [Key ID templates](https://mnestor.github.io/ssoossh/operations/key-id-templates/) |
| `operations/client-settings-enforcement.md` | [Client enforcement](https://mnestor.github.io/ssoossh/hosts/client-enforcement/) |
| `operations/email-notifications.md` | [Email notifications](https://mnestor.github.io/ssoossh/operations/email-notifications/) |
| `operations/ldap.md` | [LDAP](https://mnestor.github.io/ssoossh/operations/ldap/) |
| `operations/audit-log.md` | [Audit log](https://mnestor.github.io/ssoossh/operations/audit-log/) |
| `operations/hsm.md` | [HSM](https://mnestor.github.io/ssoossh/operations/hsm/) |
| `internals/invariants.md` | [Invariants](https://mnestor.github.io/ssoossh/internals/invariants/) |
| `internals/signing-pipeline.md` | [Architecture](https://mnestor.github.io/ssoossh/internals/architecture/) |
| `internals/wire-types.md` | [Wire types](https://mnestor.github.io/ssoossh/internals/wire-types/) |
| `internals/design-brief.md` | [Design brief](https://mnestor.github.io/ssoossh/internals/design-brief/) |
| `internals/host-context.md` | [Host context](https://mnestor.github.io/ssoossh/internals/host-context/) |
| `project/decisions.md` | [Decisions](https://mnestor.github.io/ssoossh/project/decisions/) |
| `project/release-notes.md` | [Release notes](https://mnestor.github.io/ssoossh/project/release-notes/) |
| `project/DEPENDENCY-SCANNING.md` | [Dependency scanning](https://mnestor.github.io/ssoossh/project/dependency-scanning/) |

The annotated defaults that ship as `/etc/ssoossh/*.yaml` are still in the
source tree they are generated into:
[server/config/defaults.yaml](../server/config/defaults.yaml) and
[client/config/defaults.yaml](../client/config/defaults.yaml).

The annotated `pam.d` stack for `sudo`/`su` ships from
[github.com/mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam) with
the module; its documentation is still maintained here, on the site's
[host administration pages](https://mnestor.github.io/ssoossh/hosts/pam/sudo/).

## Generated artifacts

Neither is hand-edited.

- [openapi.yaml](openapi.yaml) comes from the handler annotations in
  `server/controller`. Run `make openapi`.
- [wire-contract.json](wire-contract.json) versions the shapes ssoosshd puts
  on the wire, for the C module in
  [ssoossh-pam](https://github.com/mnestor/ssoossh-pam), which shares no code
  with this repository. Run `make wire-contract`; see
  [Wire types](https://mnestor.github.io/ssoossh/internals/wire-types/).

Man pages under [man/](man/) are generated too, except the `.5` config-format
pages. Run `make gendocs`.

## proposals/

Designs for work that has not been built. Each states its status and the
commit its `file:line` anchors were verified against, because those anchors
drift.

Nothing below has been built. Designs whose work has shipped are removed
rather than kept: the operator-facing page on the site becomes the record,
and the design's reasoning is in the commit that implemented it.

| Document | What it covers |
| --- | --- |
| [source-address-restrictions.md](proposals/source-address-restrictions.md) | Approver-chosen source-address pinning and a retrieval allowlist |
| [service-retrieval-anomaly-policy.md](proposals/service-retrieval-anomaly-policy.md) | Alerting and locking an enrollment code redeemed from too many source networks |
| [config-coordination.md](proposals/config-coordination.md) | Detecting and reporting configuration divergence between instances |
| [gui-client-approval-flow.md](proposals/gui-client-approval-flow.md) | Approving for a GUI SSH client, which has no terminal to print the URL to |
| [certificate-lifetime-policy-rework.md](proposals/certificate-lifetime-policy-rework.md) | Untangling source-address pinning from the lifetime rule, and runtime-editable policy. Partly overtaken: see the doc |
| [hsm-cloud-readiness.md](proposals/hsm-cloud-readiness.md) | What a cloud HSM/KMS backend needs, and what the SoftHSM simulation can and cannot rehearse |
| [ldap-gssapi-bind.md](proposals/ldap-gssapi-bind.md) | Binding to the directory with a Kerberos keytab instead of a static password |
| [enhancements.md](proposals/enhancements.md) | Small feature modifications logged for later, each too small for its own doc |

## dev/

| Document | What it covers |
| --- | --- |
| [e2e-testing-plan.md](dev/e2e-testing-plan.md) | The end-to-end merge gate: login, browser approval, certificate, `ssh` |
| [cross-platform-testing.md](dev/cross-platform-testing.md) | Testing the client across macOS, Linux, and Windows |
| [testing-strategy-assessment.md](dev/testing-strategy-assessment.md) | What further test investment would and would not buy |
| [test-coverage-gap-map.md](dev/test-coverage-gap-map.md) | Where coverage is thin and why |
| [testing-needs.md](dev/testing-needs.md) | Known coverage gaps, each with the evidence it is real. A worklist |
| [mutation-testing-findings.md](dev/mutation-testing-findings.md) | What mutation testing surfaced |
| [multi-instance-safety-plan.md](dev/multi-instance-safety-plan.md) | Design record for multi-instance safety (implemented) |
| [signer-split-deferred.md](dev/signer-split-deferred.md) | Design record for the split signer process (implemented) |

See also [CONTRIBUTING.md](../CONTRIBUTING.md) for the contribution process
and [Makefile.md](../Makefile.md) for what each `make` target does.
