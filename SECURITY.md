# Security Policy

## Reporting a Vulnerability

Please report security vulnerabilities using [GitHub's private vulnerability reporting](https://github.com/mnestor/ssoossh/security/advisories/new) (Security tab → **Report a vulnerability**). This keeps the report private while a fix is developed and lets us collaborate with you on a GitHub Security Advisory.

Do not open a public issue for a suspected vulnerability.

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce (PoC code, request/response samples, config used)
- Affected component(s) and version/commit
- Any known mitigations or workarounds

## Priority Areas

ssoossh handles SSH authentication and certificate/key issuance, so the following are especially high-priority:

- **Authentication flows** — SSO/OIDC login, session and token handling (`server/controller/auth.go` and related)
- **PAM certificate issuance** (`cert_options.pam` in `server/config/`, `server/service/certtypepolicy.go`) — anything affecting host login authorization. The module itself lives in [mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam); report vulnerabilities in its code there
- **SSH key and certificate handling** (`internal/crypto/ssh/`, `server/signer/`) — key generation, signing, storage

Reports touching these areas will be triaged first.

## Supported Versions

ssoossh is pre-1.0 and under active development. Only the latest tagged release receives security fixes; older releases are not backported. If you're running from `main`, update to the latest commit before reporting.

## Response Process

This is a single-maintainer project, so there's no fixed SLA — reports are acknowledged and triaged as promptly as possible. We ask for coordinated disclosure: please give us a reasonable opportunity to investigate and release a fix before any public disclosure, and we'll keep you updated on progress and credit you in the advisory (unless you prefer otherwise).

## Scope

This policy covers the code in this repository (server, client, PAM module, and frontend). Vulnerabilities in third-party dependencies should be reported upstream, but let us know too if they affect ssoossh directly — see [NOTICE](NOTICE) for adapted third-party code.
