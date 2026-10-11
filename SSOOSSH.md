## Product intent (source of truth)

  This list says what ssoossh does and must keep doing. If the code or the docs disagree with it, the code or doc is wrong: report the drift and fix the code. Never "resolve" a drift by removing or weakening a feature listed here. Changing this
  list needs the user's explicit say-so.

  ### Invariants
  - The server never receives, generates or stores a private key. The client keeps private keys in ssh-agent or local files only.
  - The client never opens a listening port. There is no loopback redirect: the browser lands on the server and the client learns the outcome over SSE.
  - Server config is the outer bound. The client asks, the web UI narrows, the config gates. Options the config does not allow are trimmed, not rejected, and are shown struck through before approval. Nothing reachable over HTTP can widen
  issuance or anyone's authority.
  - A human approves every issuance in the browser. Service certificates are approved once, at enrollment. Every issuance goes through the sign queue.
  - Issued certificates are never stored on the server. Delivery is the only copy.
  - Group membership never appears in a certificate. Roles and policy come from the session identity, never from database rows.
  - There are no host certificates. `ssoossh host` is local principal mapping for `AuthorizedPrincipalsCommand`, plus `host check`, a read-only report on whether this host's sshd will accept ssoossh certificates.
  - `verify-required` is never used. `no-touch-required` is only for enrolled `sk-` keys on the service path.
  - Policy uses the source address the server observed, never one the client supplied. Audit records never capture `Cookie` or `Authorization`.
  - `client/` and `server/` never import each other. `internal/apitypes` and `internal/principalsmap` are a cross-repo wire contract with pam_ssoossh, gated by `docs/wire-contract.json`.

  ### Certificate types (each has its own `cert_options.<type>` block: `valid_duration` and `extensions` ceilings, `key_id_template`, `require`/`require_group`, `lifetime_policy`)
  - User: interactive SSH, started from `ssh_config` (`Match exec "ssoossh ssh login"` or ProxyCommand). A valid certificate is reused until it expires.
  - Service: a person enrolls once in the browser. Retrieval is unattended, by an enrollment code bound to the public key and the option set. Every retrieval is logged. Enrollments can be expired and have holders and a notification email.
  - PAM: `sudo`, `su`, a password-less `sshd` stack and console `login`, through pam_ssoossh. The certificate lives about 30 seconds, is validated once and is discarded.
  - Console: login at a machine with no browser, approved with a typed code (`/c/<code>`), limited by `allowed_networks`.

  ### Server (ssoosshd)
  - OIDC login with PKCE on by default and claim mapping: subject, username, name, groups, other_accounts, service_accounts, email, extra.
  - Optional LDAP enrichment and scheduled directory sync (`disable_after`, `reenable`), plus `ssoosshd ldap probe` and admin sync, probe and status.
  - Principals are the user's own name plus other_accounts and service_accounts, chosen at approval. These lists are re-read from the database on every request.
  - Lifetime policy only narrows.
    - Conditions: group, claim, at_least, at_most, exactly, equals, one_of, contains, all_of, any_of.
    - Tiers grant duration and extensions inside the type's ceiling.
    - `source_policy` matches a CIDR and can cut the duration or remove extensions. `pin_source_address` applies to service certificates only.
    - `max_cert_lifetime` and `max_service_cert_lifetime` clamp every certificate just before signing.
  - The CA key comes through the `CAKeySource` interface, from one of four sources: `ssh_key`, `ssh_key_file`, `ssh_key_agent` or `hsm` (PKCS#11).
  - Signing is asynchronous and the signer has no database access. Run modes are `serve`, `serve api` and `sign`. Multi-instance needs NATS (mTLS) and a shared database.
  - Database: SQLite or Postgres, with migrations for both.
  - Roles come from OIDC groups named in config: admin implies SOC implies auditor. There is no admin role stored in the database.
  - The audit log is append-only and is swept by retention or row cap. Approval decisions record who, from where, when and what was granted.
  - Email notifications cover certificates issued (user, PAM, console) and service enrollments (created, redeemed, expiring, expired-code attempt). Users set their own notification preferences.
  - Each user has two detail levels, both stored on the server per user: one for the web UI and a separate one for email.
    - The defaults are set once, at the user's first login: advanced for SOC and admin, simplified for everyone else. A later change of groups never moves them.
    - The email level decides only whether a message adds its "Details" section (key IDs, serials, options). Users without a stored level, and a service code's notification address, get simplified.
  - Admins can disable and re-enable users, run diagnostics, view the effective config and use identity echo (re-authentication to inspect claims).
  - Also: branding, FIPS 140-3 mode, OpenTelemetry traces and metrics, rate limiting, and how much `/api/version` discloses is configurable.

  ### Client (ssoossh)
  - Runs on macOS, Linux and Windows.
  - Commands: `ssh login|logout|proxycommand|inspect|config`, `host principals|mapping list|add|remove|check`, `service enroll|retrieve`, `ca`, `version`.
  - A new keypair is generated for every new certificate. Keys go to ssh-agent, or to files when `fallback_file_agent` allows. A preflight store-and-remove round trip runs before the request is made.
  - Key type and size are steered, FIPS-aware. CA public keys are cached and can be a list. `service retrieve` has an offline grace period.
  - Certificate extensions can be opted out in config or with the `--no-*` flags.
  - `-v`, `-vv` and `-vvv` trace what the client does. `--debug` is the only place resolved settings are printed, and it marks admin-locked sources in the merge chain.
  - The client reports its administrative policy (forbidden extensions) to the server. The approval page shows it but never decides on it.
  - `host check` reads sshd's effective configuration (`sshd -T`) and reports whether the host will accept certificates: public key authentication, `TrustedUserCAKeys` present and matching the CA keys the server signs with (or a pinned `capubkey`), CA signature and certificate algorithms, the principals command's path safety, account and mapping access, and clock sync. It changes nothing, and exits non-zero when a check fails. Its local findings never depend on reaching the server.

  ### Client admin locks (`enforce`): never remove or weaken
  - Only the machine-wide config (`/etc/ssoossh/ssoossh.yaml`, `%ProgramData%\ssoossh\ssoossh.yaml`) can name an `enforce` file. A relative path resolves inside the system directory.
  - Platform policy: Windows `HKLM\SOFTWARE\Policies\com.github.mnestor\ssoossh` (GPO or Intune) and macOS managed preferences (MDM).
  - Precedence, lowest to highest: defaults, user file, `--config`, CLI flags, `enforce`, platform policy. A locked setting beats everything the user controls, CLI flags included.
  - A missing or malformed `enforce` file is a hard error (fail closed).
  - `fips: true` from a locked source makes a non-FIPS key type a hard error.
  - `forbidden_certificate_extensions` is subtracted after flags and cannot be re-added.
  - This is a guardrail, not a security boundary, but the shipped client must honour it.

  ### Web UI (SvelteKit, embedded in ssoosshd)
  - Approval page:
    - shows what will be issued, with trimmed options struck through
    - has a principal picker that remembers the last approved set per browser
    - offers a simplified or advanced detail level, saved to the user's account on the server (not per browser) and shared with the certificate and service code pages
  - User pages: dashboard, certificate detail, own history, service codes (holders, redemption log), console code entry, preferences (notification kinds, email detail level, web detail level), account.
  - Admin pages: users (audit timeline, disable and enable), audit, certificate history, service codes (expire), directory, diagnostics, config, identity echo.

  ### pam_ssoossh (separate C repo mnestor/ssoossh-pam; its docs live here)
  - PAM `auth` module with an ephemeral keypair. It shares the principals-map file format with `ssoossh host`.

  ### Not built yet (designs in `docs/proposals/`; not shipped, but do not delete them)
  - Admin elevation, NSS directory (4 parts), client policy assertions, config coordination, service retrieval anomaly policy, service source-address restrictions, GUI client approval flow sections 1-5, LDAP GSSAPI bind, the rest of cloud HSM
  readiness and of the lifetime policy rework.

  ### Declined on purpose (see the Decisions page)
  - Host certificates, a database-stored admin role, runtime policy that can loosen, pinning user certificates to a source address (may be revisited), JetStream, sticky sessions, `client_timeout: 0`, API versioning, i18n.
