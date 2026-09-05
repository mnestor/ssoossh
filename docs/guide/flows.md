# ssoossh flows

Every flow as a sequence of small Mermaid diagrams, each covering one
stage. Rendered by GitHub, GitLab, Obsidian, and anything else that
supports [Mermaid](https://github.com/mermaid-js/mermaid). For the
feature-level view see [features.md](features.md); for a narrated,
illustrated version of flow 1 see [walkthrough.html](walkthrough.html).

---

## 1. Interactive user certificate

The everyday path, in four stages: the client requests a certificate (1a),
the user approves in a browser (1b), the server signs and delivers (1c),
and `ssh` connects (1d).

### 1a. `ssh` invokes the client, which requests a certificate

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant SSH as ssh
    participant Client as ssoossh client
    participant Server as ssoossh server

    User->>SSH: ssh host
    SSH->>Client: invoke (ProxyCommand / Match exec)
    Note over Client: a valid certificate already in the<br/>agent or on disk is reused: done
    Client->>Client: generate SSH keypair
    Client->>Server: POST public key + host IP list
    Server-->>Client: authorization URL
    Client->>User: print URL (and try to open a browser)
    Client->>Server: open SSE stream and wait
```

The wait window is configurable, up to about 5 minutes. The client never
opens a listening port; the browser lands on the server and the client
learns the outcome only over this SSE stream.

### 1b. The user authenticates and approves in a browser

Runs in the browser while 1a waits. The client is not a participant.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Browser
    participant Server as ssoossh server
    participant IdP as OIDC provider

    User->>Browser: open authorization URL
    Browser->>Server: authorization request
    Server->>IdP: OIDC authentication
    IdP-->>Server: identity claims
    Note over Server: optional LDAP lookup when<br/>claims are incomplete
    Server->>Server: trim options to server config,<br/>apply lifetime policy
    Server->>Browser: approval page: what will be issued,<br/>trimmed options struck through
    User->>Browser: approve
```

Approval is prompted by default; a deployment may allow bypassing the
prompt. Option trimming and lifetime policy are diagrams 2 and 3.

### 1c. The server signs and delivers

Signing happens asynchronously off a queue after approval. The signer
holds the CA key and has no database access, so it can run as a separate,
minimally privileged process
([signing-pipeline.md](../internals/signing-pipeline.md),
[signer-split-deferred.md](../dev/signer-split-deferred.md)).

```mermaid
sequenceDiagram
    autonumber
    participant Server as ssoossh server
    participant Signer as signer (no database)
    participant Listener as listener/resolver
    participant Client as ssoossh client

    Server-)Signer: signing job [certrequest.sign]
    Signer->>Signer: sign with CA key
    Signer-)Listener: signed certificate [certrequest.signed]
    Listener->>Listener: record audit row, resolve request
    Listener-->>Client: certificate over the waiting SSE stream
    Note over Client: load into ssh-agent,<br/>or write key files when no agent
```

The certificate is never stored server-side. Delivery is the only copy; a
client that misses the window re-requests.

### 1d. `ssh` connects to the target host

Nothing here involves ssoossh. The client has exited (or, in
`ProxyCommand` mode, is only relaying bytes) and this is ordinary SSH
certificate authentication.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant SSH as ssh
    participant Host as target host sshd

    SSH->>Host: connect, present certificate
    Host->>Host: signed by a CA in TrustedUserCAKeys?
    Host->>Host: requested login name an allowed principal?
    alt permitted
        Host-->>User: session
    else not permitted
        Host-->>User: authentication failure
    end
```

`sshd` also checks the validity window, enforces critical options such as
`source-address` and `force-command`, applies the extension set, and
requires proof of private-key possession. Principals map via
`AuthorizedPrincipalsFile` or `AuthorizedPrincipalsCommand`.

---

## 2. Option resolution: requests ask, the server narrows, config gates

Three layers narrow what a certificate may carry. Only the server config
defines what exists at all; nothing reachable over HTTP can exceed it.

```mermaid
flowchart TD
    A["Client request:<br/>principals, extensions,<br/>force-command, source-address"] --> B{"Permitted by<br/>server config?"}
    B -- "no" --> C["Trim, record as removed"]
    B -- "yes" --> D["Keep"]
    C --> E["Approval page:<br/>candidate set +<br/>trimmed options shown"]
    D --> E
    E --> F["User narrows or approves"]
    F --> G["Lifetime policy applied<br/>(diagram 3)"]
    G --> H["Sign"]
```

---

## 3. Certificate lifetime policy

The server is the policy decision point; the target host's `sshd`
enforces the result. Semantics and rationale:
[certificate-lifetime-policy.md](../operations/certificate-lifetime-policy.md).

```mermaid
flowchart TD
    R["Certificate request"] --> S["Signals:<br/>source network, group membership,<br/>certificate type, requested options"]
    S --> M{"Any policy rule<br/>matched? (default deny)"}
    M -- "no" --> X["Deny"]
    M -- "yes" --> P["Intersect matched rules:<br/>shortest lifetime wins,<br/>narrowest principal set wins"]
    P --> Q["Validity window + constraints"]
    Q --> Y["Sign, log the signals<br/>behind the decision"]
```

Rules only ever narrow: weak correlation between browser and client
shortens a lifetime when absent, and never extends one when present.

---

## 4. Service certificate: enroll once, reissue unattended

> **Status:** the server-side API exists; the client's `service enroll`
> and `service retrieve` commands are not wired up yet and fail with a
> clear message.

A user is involved exactly once. Everything after that runs without a
browser.

### 4a. Enrollment: key registration

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Client as ssoossh client
    participant Server as ssoossh server

    User->>Client: enroll service account + requested options
    alt bring your own key (--public-key)
        User->>Client: supply existing public key
        Note over Client: private key may live on an HSM<br/>or PKCS#11 token; ssoossh never sees it
    else generated by client (--generate)
        Client->>Client: generate keypair
        Note over Client: private key written 0600 to the named path<br/>(owner-only access list on Windows),<br/>public key to &lt;path&gt;.pub; neither is overwritten
    end
    Client->>Server: POST public key + requested options
    Server-->>Client: authorization URL
    Client->>Server: open SSE stream and wait
```

### 4b. Enrollment: approval and the enrollment code

The browser approval is the same as 1b. What comes back is different:

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Browser
    participant Server as ssoossh server
    participant Client as ssoossh client

    User->>Browser: authenticate and approve,<br/>choosing a service account
    Server->>Server: bind code to this public key<br/>AND the authorized option set
    Server-->>Client: enrollment code, service account,<br/>code expiry
    Client->>User: print the code, whose it is,<br/>when it dies, and how to use it
```

The code is printed once and stored nowhere. It is a bearer credential —
anyone holding it can obtain certificates for the service account until the
enrollment expires — and where it belongs is wherever the unattended job
will read it, which only the operator knows. So `service enroll` prints it
along with the exact `service retrieve` command to run, and `service
retrieve` reads it from `--code` or `$SSOOSSH_ENROLLMENT_CODE`.

The chosen service account and the code's expiry come back on the same
event as the code, because the operator at the terminal never sees the
approval screen. Without them the only way to learn which principal the
certificates carry would be to retrieve one and inspect it, and the only
statement about the code's lifetime would be "until the enrollment
expires". The account is set by the approver from their own linked
accounts, and it is the sole principal of every certificate the code
redeems. The expiry bounds the *code*
(`cert_options.service.enrollment_duration`), not those certificates, which
get their own lifetime at each redemption.

Because the account comes back with the code, the printed `ssh_config`
recipe names it: the `Match user` block keys on the service account rather
than a `USERNAME` placeholder the operator has to fill in. A server too old
to report the account leaves the placeholder in place, since there is
nothing on the client side to derive it from.

Afterwards the approver can see what they granted, without the code, at
**Service codes** in the web UI (`GET /api/certs/service/enrollments`): one
row per approved enrollment, opening into which account it mints for, the
options and certificate lifetime fixed at approval, the keypair it is bound
to, when it stops being redeemable, and its redemption log. The code is not
part of that answer and there is no endpoint that returns one — it exists on
the wire exactly once, in the approval event above.

### 4c. Unattended reissue

```mermaid
sequenceDiagram
    autonumber
    participant Job as scheduled job
    participant Client as ssoossh client
    participant Server as ssoossh server
    participant Host as target host sshd

    Job->>Client: need certificate
    Client->>Server: POST enrollment code only
    Server->>Server: look up enrolled public key,<br/>apply authorized option set
    Server-->>Client: certificate
    Client->>Host: authenticate with certificate
    Host-->>Job: session
```

The public key is not resubmitted, so a stolen code cannot be paired with
an attacker's own keypair. The same keypair is used every time: no new
key, no browser, no user.

Each redemption is logged, and the serial ties the two halves together: it
is allocated before the signing job is queued, written to the
`enrollment_retrievals` row, and lands on the issued certificate. That is
what lets the certificate history report the address a service certificate
was actually fetched from — a different fact from the approval's source IP,
which belongs to the human who approved the code and is identical on every
certificate it mints.

---

## 5. `sudo`/`su` via pam_ssoossh

Scoped to `sudo` and `su`, `auth` management group only. The module keeps
nothing: a per-attempt ephemeral keypair, a certificate valid for seconds,
and everything discarded afterward.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant PAM as pam_ssoossh
    participant Server as ssoossh server

    User->>PAM: sudo / su
    PAM->>PAM: generate ephemeral keypair
    PAM->>Server: request certificate with nonce
    Server->>Server: authenticate and authorize user<br/>(browser approval, as in 1b)
    Server-->>PAM: certificate, seconds of validity
    PAM->>PAM: validate CA signature, key binding,<br/>principals + nonce, validity window
    alt valid
        PAM-->>User: auth success
    else invalid, expired, or server unreachable
        PAM-->>User: auth failure (falls through the stack)
    end
```

Nothing is written to disk. Stack configuration and the lockout warning:
[deployment.md](../operations/deployment.md#8-pam-sudo-and-su);
the [pam_ssoossh reference](https://mnestor.github.io/ssoossh/hosts/pam/reference/) documents every module argument.

---

## 6. Console login: a code instead of a URL

A console has a human in front of it and no browser, and nothing on the
screen can be copied. So the machine prints a short code instead of an
approval URL, and the approver carries it to a device that does have a
browser.

The module driving the console side is shipped separately; what follows is
the contract it speaks.

```mermaid
sequenceDiagram
    autonumber
    actor User as User at the console
    participant PAM as console PAM module
    participant Server as ssoossh server
    participant Browser as User's phone or desk

    User->>PAM: login: alice
    PAM->>PAM: generate ephemeral keypair
    PAM->>Server: POST /api/certs/console<br/>public key, account, host, service, tty
    Server->>Server: refuse if outside<br/>cert_options.console.allowed_networks
    Server-->>PAM: user_code, /console, /c/&lt;code&gt;, expires_at
    PAM-->>User: display the code and the URL
    User->>Browser: type the code at /console
    Browser->>Server: POST .../requests/resolve-code<br/>(session required)
    Server->>Server: resolve, then claim for this session
    Server-->>Browser: redirect to /approve/&lt;id&gt;
    Browser->>Server: approve, having seen host, service, tty, account
    Server-->>PAM: certificate, seconds of validity
    PAM->>PAM: the same four checks as sudo
    alt valid
        PAM-->>User: session starts
    else invalid, expired, or server unreachable
        PAM-->>User: auth failure (falls through the stack)
    end
```

Three things about that diagram are load-bearing rather than incidental:

- **Resolving a code requires a session.** An unauthenticated caller never
  learns whether a code is live and never receives a request ID — and the
  request ID is the credential the certificate is delivered against. Step 8
  is the whole reason the code is safe to display.
- **Resolving claims the request**, before either party sees any detail, so
  two people typing the same code is settled at submission rather than at
  the approval page.
- **The window is short on purpose.** `cert_options.console.client_timeout`
  defaults to 2m against the 5m global ceiling. That window is the
  attacker's working time in the phone-call case: someone starts a login at
  an unattended console and calls the victim to read them the code.

Which accounts may console into a machine at all is decided in that
machine's own PAM stack, above the ssoossh line, where it is root-owned and
costs no network call. Configuration and the lockout rules:
[deployment.md §9](../operations/deployment.md#9-console-login); full
reasoning: [console-login-pam.md](../proposals/console-login-pam.md).

---

## 7. Certificate types at a glance

```mermaid
flowchart LR
    CA["ssoossh CA<br/>config PEM or PKCS#11 token"]
    CA --> U["User certificate<br/>interactive SSH<br/>shipped"]
    CA --> S["Service certificate<br/>non-interactive<br/>shipped"]
    CA --> P["PAM certificate<br/>sudo/su<br/>shipped"]
    CA --> C["Console certificate<br/>login at a console<br/>server shipped"]
    U --> T["Target hosts trust the CA"]
    S --> T
```

Service certificates: `service enroll` requests, a human approves in the
browser choosing which of their service accounts the certificate is for,
and the enrollment code redeems certificates unattended via
`service retrieve` until it expires — every redemption logged for the
approver and auditors. There are no host certificates
([decisions.md](../project/decisions.md)); the local principal-mapping commands
(`host mapping`, `host principals`) support sshd's
`AuthorizedPrincipalsCommand` without any server side.
