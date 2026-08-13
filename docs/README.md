# ssoossh documentation

**ssoossh** (pronounced *sue-sssh*) is SSO for SSH: users authenticate via OIDC
and receive short-lived SSH certificates instead of managing long-lived keys.
Self-hosted, homelab-friendly, early development.

## Start here

| Document | What it covers |
| --- | --- |
| [what-ssoossh-is.md](what-ssoossh-is.md) | The problem, how it works, components, certificate types and terms |
| [ssoossh-context.md](ssoossh-context.md) | Design context: hard constraints, invocation modes, enrollment, CLI surface, open questions |
| [flows.md](flows.md) | Mermaid sequence diagrams for each flow |

## As built

| Document | What it covers |
| --- | --- |
| [signing-pipeline.md](signing-pipeline.md) | The create → approve → sign → deliver pipeline: components, topics, and the decisions that constrain the code |
| [certificate-keyid-template.md](certificate-keyid-template.md) | Per-type key ID templating |

## Delivery plan

[delivery-plan.md](delivery-plan.md) is the index — why the work is ordered the
way it is, the decisions taken, and what is deferred. Each phase has its own
file:

1. [Build, lint, and CI correctness](delivery-phase1-build-ci.md)
2. [Security hardening and identity binding](delivery-phase2-security-identity.md)
3. [Server API surface for the web UI](delivery-phase3-web-api.md)
4. [Web UI](delivery-phase4-web-ui.md)
5. [Client `ssh login` / `logout`](delivery-phase5-client-ssh.md)
6. [First release — user certificates end to end](delivery-phase6-release-user.md)
7. [pam_ssoossh](delivery-phase7-pam.md)
8. [Service certificates and enrollment](delivery-phase8-service.md)
9. [Host certificates and principal mapping](delivery-phase9-host.md)
10. [GA release](delivery-phase10-ga.md)

## Designed but deferred

| Document | What it covers |
| --- | --- |
| [signer-split-deferred.md](signer-split-deferred.md) | Running the signer as its own process: NATS, mTLS authorization, JetStream durability, startup modes |
| [certificate-lifetime-policy-plan.md](certificate-lifetime-policy-plan.md) | Deriving lifetime and critical options from issuance context rather than flat config |
| [multi-instance-safety-plan.md](multi-instance-safety-plan.md) | What must change before more than one `ssoosshd` can run against a shared database |

## Reference

| Document | What it covers |
| --- | --- |
| [security-review-2026-08-11.md](security-review-2026-08-11.md) | Branch security review, including the findings filtered out and why |
| [ssoosshd.yaml.default](ssoosshd.yaml.default) | Annotated server configuration sample |
| [ssoossh.yaml.default](ssoossh.yaml.default) | Annotated client configuration sample |
| [descriptions.md](descriptions.md) | Short and long descriptions of each component |
