# Email notifications

ssoosshd can email a user when something happens to a credential of theirs.
It is off by default and adds nothing to the certificate flows: every path
that emits a notification behaves identically with mail disabled.

Nothing a browser or an unattended job waits on ever waits on the mail
relay. An approval or a redemption publishes an event to the internal queue
and returns; rendering and SMTP happen on a background consumer. A relay
that is slow, greylisting, or down delays no certificate work — it delays
only the notification.

## What is never emailed

The service enrollment code is not in any message and must not be added to
one. It is a bearer credential that mints certificates unattended, and mail
is stored, forwarded, indexed, and read on devices the server knows nothing
about. It is shown once, in the terminal that ran `ssoossh service enroll`,
and stored nowhere.

The "service enrollment created" notification deliberately carries
everything else that command printed — the service account, the key
fingerprint, the expiry, the `ssh_config` recipe — so the message is
actionable without being redeemable. There is a test asserting the code does
not appear in a rendered message, and another asserting it never reaches the
notification payload; both exist so that adding a field later is a decision
rather than an accident.

## Configuring the relay

The full annotated block is in `server/config/defaults.yaml`. The two
deployments it is written for:

**A local relay** — the mail system already on this host takes it from here.
No TLS and no authentication are reasonable, because the connection never
leaves the machine:

```yaml
mail:
  enabled: true
  from: "ssoossh <no-reply@example.com>"
  smtp:
    host: "localhost"
    port: 25
    tls: "off"
    auth: "none"
```

**A remote submission service** — the connection crosses a network, so both
TLS and authentication are strongly suggested:

```yaml
mail:
  enabled: true
  from: "ssoossh <no-reply@example.com>"
  reply_to: "ssh-admins@example.com"
  subject_prefix: "[ssoossh] "
  smtp:
    host: "smtp.example.com"
    port: 587
    tls: "required"
    auth: "plain"
    username: "ssoossh@example.com"
    password_file: "/run/secrets/ssoossh-smtp-password"
```

### TLS and authentication are optional, and suggested

Neither is mandatory: the local-relay deployment legitimately needs neither,
and forcing them would make the simplest working setup impossible. But the
server warns at startup when it is relaying to a non-loopback host with
`tls: "off"` or `tls: "opportunistic"` (which anyone on the path can strip),
with `auth: "none"`, or with `insecure_skip_verify: true`. Those warnings
are the suggestion; they are not errors, and nothing fails because of them.

`tls` accepts:

| Value | Meaning |
| --- | --- |
| `off` | Never negotiate STARTTLS. Reasonable only for a loopback relay. |
| `opportunistic` | Use STARTTLS when offered, plaintext when not. The default. |
| `required` | Fail the delivery rather than send in plaintext. |
| `implicit` | Dial TLS directly (SMTPS, conventionally port 465). |

`auth` accepts `none` (the default), `auto`, `plain`, `login`, `cram-md5`,
`scram-sha-1`, `scram-sha-256`, and `xoauth2`. `auto` negotiates the
strongest mechanism the relay advertises.

Prefer `password_file` to `password`: it keeps the secret out of the config
file, and out of the effective configuration auditors can read.

Anything wrong here fails the server at startup rather than at first
delivery — an unparseable `from`, an unreadable password file, a template
that does not compile. A notification that never arrives looks exactly like
one that was never triggered, which is the worst thing this feature could
do quietly.

### Multi-instance deployments

The delivery consumer joins a queue group, so exactly one instance sends
each notification. Without that, a recipient would get one copy per running
server. Nothing else about a multi-instance deployment needs configuring for
mail.

## What users control

Each user chooses which notifications they receive at `/preferences` in the
web UI. Choices are stored per (user, kind); a user who has never answered
gets the kind's own default, which is what lets a new notification ship
without a backfill.

The preference is read at delivery rather than at publication, so a
notification queued moments before someone opts out is not delivered
anyway.

Two things stop delivery regardless of the toggle, and the page says so
rather than leaving the user to work it out: `mail.enabled` being false, and
an identity whose provider releases no email claim.

## Notifications

<!-- BEGIN GENERATED NOTIFICATION REFERENCE -->

### Service enrollment created

`service_enrollment_created`

Sent when you approve a service certificate request and an enrollment code is created for it.

Default: **on**.

Templates:

- `service_enrollment_created.subject.tmpl`
- `service_enrollment_created.txt.tmpl`
- `service_enrollment_created.html.tmpl`

| Field | Type | Description |
| --- | --- | --- |
| `.ServiceAccount` | `string` | The service account the enrollment was approved for. It is the sole principal of every certificate the code produces. |
| `.RequestID` | `string` | The certificate request this enrollment came from. |
| `.EnrollmentID` | `string` | The enrollment record's own identifier, as shown in the retrieval log. |
| `.KeyID` | `string` | The SSH certificate key ID fixed at approval time. |
| `.Principals` | `[]string` | The certificate principals fixed at approval time. |
| `.PublicKeyFingerprint` | `string` | SHA256 fingerprint of the enrolled public key. The code only ever produces certificates for this key. |
| `.PublicKeyType` | `string` | SSH algorithm of the enrolled public key, e.g. ssh-ed25519. |
| `.Extensions` | `[]string` | SSH certificate extensions granted, after narrowing against server config. |
| `.ForceCommand` | `string` | The force-command critical option, or empty if none was granted. |
| `.SourceAddresses` | `[]string` | The source-address critical option, or empty if unrestricted. |
| `.NoTouchRequired` | `bool` | Whether the no-touch-required extension was granted (hardware-backed sk- keys only). |
| `.RequestSourceIP` | `string` | The address the enrollment request was submitted from. |
| `.ApprovedAt` | `time.Time` | When the request was approved and the code minted. |
| `.ApprovedByUsername` | `string` | The username of the identity that approved the request. |
| `.CodeExpiresAt` | `time.Time` | When the enrollment code stops being redeemable. Re-enroll before this to keep an unattended job running. |
| `.CertificateLifetime` | `time.Duration` | How long each certificate redeemed from this code is valid for, measured from each redemption. |
| `.ServerURL` | `string` | The server's public origin, for links back to the request. |


### Service enrollment redeemed

`service_enrollment_redeemed`

Sent every time one of your enrollment codes is redeemed for a certificate, including failed attempts.

Default: **on**.

Templates:

- `service_enrollment_redeemed.subject.tmpl`
- `service_enrollment_redeemed.txt.tmpl`
- `service_enrollment_redeemed.html.tmpl`

| Field | Type | Description |
| --- | --- | --- |
| `.ServiceAccount` | `string` | The service account the redeemed certificate is for. |
| `.RequestID` | `string` | The certificate request the enrollment came from, or empty for an enrollment with no linked request. |
| `.EnrollmentID` | `string` | The enrollment whose code was redeemed. |
| `.RetrievalID` | `string` | This redemption's own identifier, matching the row in the retrieval log. |
| `.SourceIP` | `string` | The address the redemption was made from. |
| `.RetrievedAt` | `time.Time` | When the code was redeemed. |
| `.CertificateSerial` | `uint64` | Serial of the certificate issued for this redemption. |
| `.CertificateExpiresAt` | `time.Time` | When the issued certificate stops being valid. |
| `.KeyID` | `string` | The SSH certificate key ID carried by the issued certificate. |
| `.Principals` | `[]string` | The issued certificate's principals. |
| `.Succeeded` | `bool` | False when the code was valid but signing failed; the failure detail is in the server log. |
| `.FirstRedemption` | `bool` | True when this was the first time the code was redeemed. |
| `.CodeExpiresAt` | `time.Time` | When the code itself stops being redeemable. |
| `.ServerURL` | `string` | The server's public origin, for links back to the retrieval log. |

<!-- END GENERATED NOTIFICATION REFERENCE -->

## Overriding a template

Every message ships as a built-in template compiled into the binary. Point
`mail.template_dir` at a directory to replace any of them:

```yaml
mail:
  template_dir: "/etc/ssoossh/mail-templates"
```

A file there replaces the built-in template of the same name. Anything
absent falls back to the built-in one, so overriding a single message does
not mean vendoring the whole set — and a message added in a later release
keeps working without you touching the directory.

Files are named `<kind>.<part>.tmpl`, where `<part>` is one of:

| Part | Engine | Purpose |
| --- | --- | --- |
| `subject` | `text/template` | The subject line. Rendered to one line: any line break is folded to a space, so nothing interpolated into it can inject headers. `mail.subject_prefix` is prepended afterwards. |
| `txt` | `text/template` | The plain-text body. |
| `html` | `html/template` | The HTML body, sent as an alternative. Interpolated values are HTML-escaped. |

Both bodies are always sent. A text-only reader gets the whole message
rather than a notice telling them to view it elsewhere.

The field tables above list what each template may reference. In addition,
these functions are available in every template:

| Function | Example | Renders |
| --- | --- | --- |
| `datetime` | `{{ datetime .ApprovedAt }}` | `2026-08-24 15:04:05 CEST`, or `not set` for a zero time |
| `date` | `{{ date .CodeExpiresAt }}` | `2026-11-22`, or `not set` |
| `approx` | `{{ approx .CertificateLifetime }}` | `8 hours`, `90 days` — the largest unit that still says something, truncated |
| `until` | `{{ until .CodeExpiresAt }}` | `89 days`, or `already elapsed` |
| `join` | `{{ join .Principals ", " }}` | `deploy-bot, deploy-bot-2` |

Timestamps render in the server's local zone, with the zone named.

### Overrides are checked at startup

Every override is parsed **and executed against an empty payload** when the
server starts. A syntax error, or a reference to a field that does not
exist, stops the server rather than producing mail that silently stops
arriving. A file in the directory whose name matches no notification is also
a startup error — a misspelled override would otherwise appear to do
nothing. Files that are not `*.tmpl` are ignored, so a README or an editor
backup beside your overrides is fine.

The built-in templates live in `server/mail/templates/` and are the best
starting point for an override: copy one out, edit it, and drop it in the
override directory under the same name.

## Adding a notification kind

The registry in `server/notify` is what the preferences page, the generated
reference above, and delivery all read, so adding a kind is a local change
in four steps:

1. **A `Kind` constant** in `server/notify/notify.go`.
2. **A payload struct** in `server/notify/payloads.go`, holding exactly what
   the templates need. Every exported field is a template variable.
3. **A `Definition`** appended to the registry: title, description,
   `DefaultEnabled`, `NewPayload`, and one `Field` entry per payload field.
   `DefaultEnabled` decides whether existing deployments start sending
   something nobody asked for, so it is a real decision rather than a
   default.
4. **Three templates** in `server/mail/templates/`, named
   `<kind>.subject.tmpl`, `<kind>.txt.tmpl`, and `<kind>.html.tmpl`.

Then publish the event from wherever the thing happens:

```go
s.notifier.Notify(ctx, notify.KindYourNewThing, userID, &notify.YourNewThing{ /* … */ })
```

`Notify` never blocks and never returns an error — it queues and returns, so
no caller's outcome depends on the mail relay.

Nothing else changes. Preferences storage, the preferences page, the API,
the delivery consumer, and the reference table above are all driven off the
registry.

The tests hold you to the parts that are easy to half-finish:

- `server/notify` fails if a `Definition` is incomplete, if a documented
  field is not on the payload struct, or if a payload field is undocumented
  — in either direction.
- `server/mail` fails if any registered kind is missing a template, or if a
  template will not render.
- `server/notify`'s doc test fails when the reference above is stale. Run
  `go test ./server/notify/ -update` to regenerate it.
