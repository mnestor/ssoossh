# ssoossh flows

Mermaid diagrams for the flows described in `what-ssoossh-is.md`. Rendered by GitHub,
GitLab, Obsidian, and anything else that supports
[Mermaid](https://github.com/mermaid-js/mermaid).

---

## 1. Interactive user certificate

The everyday path, in three parts. `ssh` invokes the ssoossh client (1a), the user
authenticates and approves in a browser (1b), and once the client has exited `ssh`
makes an ordinary certificate-authenticated connection (1c).

### 1a. `ssh` invokes the client, the client obtains a certificate

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant SSH as ssh [process 1]
    participant Client as ssoossh client [process 2]
    participant Term as terminal
    participant Agent as ssh-agent
    participant Browser as browser [process 3]
    participant Server as ssoossh server

    User->>SSH: ssh host
    Note over SSH,Client: ssh_config ProxyCommand or Match exec
    SSH->>Client: invoke

    Client->>Agent: any valid certificate already loaded?
    Agent-->>Client: none

    Client->>Client: generate SSH keypair
    Client->>Server: POST public key + host IP list [REST]
    Server->>Server: create pending request
    Server-->>Client: authorization URL

    Client->>Term: print authorization URL
    opt browser launch enabled
        Client->>Browser: attempt to open URL
    end
    Client->>Server: open SSE stream and wait
    Note over Browser,Server: user authenticates and approves<br/>see side process 1b<br/>wait window configurable, up to ~5 min

    Server-->>Client: certificate over SSE stream
    alt ssh-agent available
        Client->>Agent: load keypair and certificate
    else no agent
        Client->>Client: write private key, public key, certificate to disk
    end
    Client-->>SSH: certificate ready, exit
```

### 1b. Side process: OIDC authentication and certificate approval

Runs in the browser while 1a waits. The ssoossh client is not a participant — it learns
the outcome only when the certificate arrives on its SSE stream.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Browser as browser [process 3]
    participant Server as ssoossh server
    participant IdP as OIDC provider
    participant LDAP
    participant Signer as signer [no database]
    participant Listener as listener/resolver

    User->>Browser: open authorization URL,<br/>launched by the client or pasted by hand
    Browser->>Server: authorization request
    Server->>IdP: OIDC authentication
    IdP-->>Server: identity claims
    opt claims incomplete
        Server->>LDAP: look up attributes
        LDAP-->>Server: groups and account identifiers
    end

    Server->>Server: resolve options against server config,<br/>trim what is not permitted
    Server->>Server: apply lifetime policy to request context

    opt approval prompt forced [default]
        Server->>Browser: show options to be issued<br/>and anything trimmed
        User->>Browser: approve
    end

    Server->>Server: mark request "signing",<br/>publish signing job to queue
    Server->>Browser: confirmation page: approved,<br/>certificate will be delivered to the client

    Note over Server,Listener: the approving browser is done here —<br/>signing happens asynchronously off the queue
    Server-)Signer: signing job [certrequest.sign]
    Signer->>Signer: sign with CA key<br/>(no database access)
    Signer-)Listener: signed certificate [certrequest.signed]
    Listener->>Listener: record audit row,<br/>mark request resolved
    Note over Listener: certificate is released to the<br/>waiting SSE stream in 1a
```

Signing is deliberately split out: the signer holds the CA key and has no
database access, so it can later run as a separate, minimally-privileged
process (see `docs/signer-split-deferred.md` and
`docs/signing-pipeline.md`). The certificate itself is never stored —
it's delivered once to the waiting client, which re-requests if it misses
that window.

### 1c. `ssh` connects to the target host

Nothing here involves ssoossh — the client has already exited and this is ordinary SSH.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant SSH as ssh [process 1]
    participant Host as target host sshd

    SSH->>Host: connect, present certificate

    Host->>Host: signed by a CA in TrustedUserCAKeys?
    Host->>Host: is the requested login name<br/>an allowed principal?
    Note over Host: AuthorizedPrincipalsFile or<br/>AuthorizedPrincipalsCommand

    alt permitted
        Host-->>User: session
    else not permitted
        Host-->>User: authentication failure
    end
```

sshd also checks the validity window, enforces critical options such as
`source-address` and `force-command`, applies the extension set, and requires the client
to sign a challenge proving it holds the private key. Those steps are standard
certificate authentication and are left out of the diagram.

---

## 2. Option resolution and the server config gate

Three layers narrow what a certificate may carry. Only the server config can define
what exists at all.

```mermaid
flowchart TD
    A["Client request<br/>principals, force-command,<br/>source-address, extensions"] --> B{"Server config<br/>option available in<br/>this deployment?"}
    B -- "no" --> C["Trim option<br/>record it as removed"]
    B -- "yes" --> D["Carry into candidate set"]
    C --> E["Candidate option set<br/>+ list of trimmed options"]
    D --> E

    E --> F{"Approval prompt<br/>forced?"}
    F -- "forced (default)" --> G["Web UI: user reviews,<br/>narrows or overrides,<br/>sees trimmed options"]
    F -- "bypass allowed" --> H["Skip prompt"]
    G --> I["Authorized option set"]
    H --> I

    I --> J["Apply lifetime policy<br/>see diagram 4"]
    J --> K["Sign certificate"]
    K --> L["Confirmation page:<br/>issued details + trimmed options"]

    style B fill:#fff3cd,stroke:#856404
    style K fill:#d4edda,stroke:#155724
```

---

## 3. Service certificate: enrollment, then unattended reissue

A user is involved exactly once. Everything after that runs without a browser.

### 3a. Enrollment

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Client as ssoossh client
    participant Browser
    participant Server as ssoossh server
    participant IdP as OIDC provider

    User->>Client: enroll service account with requested options
    alt bring your own key
        User->>Client: supply existing public key
        Note over Client: private key may live on HSM,<br/>PKCS#11 token, or encrypted file<br/>ssoossh never sees it
    else generated by client
        Client->>Client: generate keypair<br/>optional passphrase protection
    end

    Client->>Server: POST public key + requested options [REST]
    Server-->>Client: authorization URL
    Client->>Server: open SSE stream and wait

    User->>Browser: open authorization URL
    Browser->>Server: authorization request
    Server->>IdP: OIDC authentication
    IdP-->>Server: identity claims
    Server->>Server: intersect requested options with server config
    Server->>Browser: approval prompt with trimmed options shown
    User->>Browser: adjust and approve
    Server->>Browser: confirmation page

    Server-->>Client: enrollment code
    Note over Server,Client: code is bound to this public key<br/>AND to the authorized option set
    Client->>Client: store code alongside keypair
```

### 3b. Unattended reissue

```mermaid
sequenceDiagram
    autonumber
    participant Job as scheduled job
    participant Client as ssoossh client
    participant Server as ssoossh server
    participant Host as target host sshd

    Job->>Client: need certificate
    Client->>Server: POST enrollment code only [REST]
    Server->>Server: look up enrolled public key by code,<br/>apply authorized option set
    Note over Server: the public key is not resubmitted<br/>a stolen code cannot be paired<br/>with an attacker's own keypair
    Server-->>Client: certificate
    Note over Client: same keypair as enrollment<br/>no new key, no browser, no user
    Client->>Host: authenticate with certificate
    Host-->>Job: session
```

---

## 4. Certificate lifetime policy

Design in progress. The server is the policy decision point; the target host's sshd
enforces the result.

```mermaid
flowchart TD
    R["Certificate request"] --> S1["Signal: client source network"]
    R --> S2["Signal: browser/client correlation<br/>source address match + client self-report"]
    R --> S3["Signal: OIDC / LDAP group membership"]
    R --> S4["Signal: certificate type and<br/>requested options"]

    S1 --> M["Match against policy rules<br/>default deny"]
    S2 --> M
    S3 --> M
    S4 --> M

    M --> N{"Any rule matched?"}
    N -- "no" --> X["Deny"]
    N -- "yes" --> P["Intersect all matched rules"]

    P --> P1["Shortest max lifetime wins"]
    P --> P2["Narrowest principal set wins"]
    P --> P3["source-address = client interface IPs<br/>+ server-observed address<br/>capped by permitted zones"]

    P1 --> Q["Validity window + constraints"]
    P2 --> Q
    P3 --> Q
    Q --> Y["Sign, log signals behind the decision"]

    style N fill:#fff3cd,stroke:#856404
    style X fill:#f8d7da,stroke:#721c24
    style Y fill:#d4edda,stroke:#155724
```

Weak correlation shortens a lifetime when absent; it never extends one when present.

---

## 5. pam_ssoossh

Scoped to `sudo` and `su`, `auth` management group only. The module keeps nothing.

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant PAM as pam_ssoossh
    participant Server as ssoossh server
    participant CA as CA public key

    User->>PAM: sudo / su
    PAM->>PAM: generate ephemeral keypair
    PAM->>Server: request certificate with nonce
    Server->>Server: authenticate and authorize user
    Server-->>PAM: certificate, 30-60 second validity
    PAM->>CA: check signer
    PAM->>PAM: validate principals and nonce
    alt valid
        PAM-->>User: auth success
    else invalid or expired
        PAM-->>User: auth failure
    end
    PAM->>PAM: discard keypair and certificate
    Note over PAM: nothing is written to disk
```

> The shape of this module is an open design question — see the separate PAM design
> document. A future console login module using a typed code is a related but distinct
> problem.

---

## 6. Certificate types at a glance

```mermaid
flowchart LR
    CA["ssoossh CA<br/>key held in ssh-agent (v1)<br/>PKCS#11 / HSM / KMS (planned)"]

    CA --> U["User certificate<br/>interactive SSH<br/>principals from OIDC + LDAP"]
    CA --> H["Host certificate<br/>server identity<br/>removes known_hosts churn"]
    CA --> S["Service certificate<br/>non-interactive<br/>enrolled once, reissued unattended"]

    U --> UH["Target hosts trust the CA"]
    H --> UH
    S --> UH

    style CA fill:#d1ecf1,stroke:#0c5460
```
