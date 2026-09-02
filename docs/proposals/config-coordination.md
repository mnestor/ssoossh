# Coordinated configuration across instances

**Status: designed, nothing built.** No code has been written for this. The
`file:line` anchors below were verified against `5d23809` (2026-08-24) and
will drift.

> **Before planning from this document**, re-run the checks in
> [Provenance](#provenance-what-was-verified-and-how). Two of the load-bearing
> facts (that leader election does not exist, and that `ssoosshd sign` has no
> database) shape the whole design, and either could change independently.

Read [dev/multi-instance-safety-plan.md](../dev/multi-instance-safety-plan.md)
first. It established the shared-database, shared-NATS deployment this design
assumes, and it is where the one existing cross-instance config requirement
(`http.cookie_key`) came from.

## What this proposes

A multi-instance deployment shares a database and a NATS cluster. It shares
nothing else. Every instance reads its own `/etc/ssoossh/ssoosshd.yaml`, and
nothing notices when those files disagree.

Most disagreements are harmless: `http.address` is supposed to differ. Some
are not. Two instances with different `cert_options.user.valid_duration`
issue certificates whose lifetime depends on which one the load balancer
picked. Two instances with different `admin.require_group` grant admin to
different people. Two instances with different `ssh_key` sign with different
CAs, and every host trusting one CA rejects half the certificates issued.
None of these fail loudly. They present as intermittent, load-balancer-
dependent behaviour that looks like a bug in ssoossh rather than a
difference between two files on two hosts.

This proposes four things:

1. A **classification** of every configuration key as coordinated or
   per-instance, that a new key joins automatically.
2. A **fingerprint** of the coordinated subset, computed from the effective
   configuration rather than the file text.
3. A **rendezvous** where running instances exchange fingerprints.
4. **Reporting** of divergence: structured logs, the auditor UI, a metric,
   and a CLI check.

Item 1 is the requirement that shapes everything else. A hand-maintained
list of coordinated keys is wrong the first time someone adds a field
without thinking about multi-instance, and it is wrong silently. The
mechanism has to include new fields by default, and a test has to fail when
a new field's classification is missing.

### What this does not propose

**Runtime reconfiguration.** Configuration comes from the file the operator
edits and from nowhere else. `server/model/server_secret.go:13` states the
position for the one table that comes closest to a settings table:
"Deliberately not a general settings table. Server configuration comes from
the config file only and is never reconfigurable at runtime". Nothing here
changes that. Every mechanism below is read-only with respect to
configuration: it observes, compares, and complains.

**Automatic remediation.** No instance ever adopts another instance's value.
The output is a report an admin acts on by editing a file and restarting.

## What exists today (verified)

| Fact | Anchor |
| --- | --- |
| Config is `defaults.yaml` layered under the operator's file, unmarshalled once at startup | `server/config/config.go:33` (`NewConfig`) |
| The only existing cross-instance requirement is enforced as a startup error | `server/config/config.go:108` |
| `SignerConfig` is squashed, so its keys are top-level in YAML | `server/config/types.go:41` |
| `FIPS` and `HTTPSettings.CookieSecure` are `*bool`, so unset differs from false | `server/config/types.go:65`, `server/config/types_http.go:105` |
| `Config.FIPSEnabled()` resolves unset against the runtime's own FIPS mode | `server/config/types.go`, `internal/fipsmode` |
| Three startup modes exist, and `ssoosshd sign` has **no database** | `docs/configuration.md:154-158` |
| Auditors already have a redacted effective-config endpoint | `server/controller/admin.go:37` (route), `:61` (handler) |
| Its response shape is a hand-written subset of the config | `server/webtypes/webtypes.go:349` (`EffectiveConfigResponse`) |
| Jobs are registered on a shared scheduler with no leader gating | `server/job/scheduler.go:87`, `server/service/scheduler.go:11` |
| Leader election does **not** exist; it is named as future work | `server/bootstrap/scheduler.go:83` |
| NATS queue groups are derived from the topic by a `switch`; unlisted topics get fan-out | `server/pubsub/pubsub.go:279-301` |
| Notifications already use a queue group so only one instance sends each mail | `server/pubsub/pubsub.go:298`, `server/notify/event.go:18` |
| A golden test already guards config values against silent change | `server/config/defaults_golden_test.go`, `test/configgolden/configgolden.go` |
| `configgolden.Flatten` renders YAML as sorted `dotted.key = value` lines | `test/configgolden/configgolden.go` |
| Migrations are per-dialect SQL pairs | `server/resources/migrations/{postgres,sqlite}/`, e.g. `20260824000000_retrieval_serial_index.up.sql` |

Two of these deserve emphasis, because they constrain the design more than
the rest.

**`ssoosshd sign` has no database.** A signer-only instance cannot
participate in any database rendezvous. It holds the CA key, which is the
single most damaging thing to have wrong, so excluding it is not acceptable.
Whatever the primary mechanism is, there has to be a path that reaches
signer instances, and NATS is the only one they are on.

**Leader election does not exist.** `server/bootstrap/scheduler.go:83` calls
the sweep "a candidate for leader election once multiple instances are
supported", which is a statement of intent, not of code. Any design that
says "the leader reports the divergence" is proposing leader election as a
prerequisite. There is an existing alternative that does the same job for
notifications: the `notifiers` queue group on `notify.Topic`, which already
guarantees exactly one instance delivers each mail
(`server/pubsub/pubsub.go:298`). Divergence notifications should ride that
rather than wait for leader election.

## The classification model

### Default-coordinated, opt-out local

Every leaf key is coordinated unless it is explicitly marked per-instance.
The inverse design, an allowlist of coordinated keys, is the one that
forgets: a new key added without thought is absent from the list and
therefore silently unchecked. With default-coordinated, a new key added
without thought is compared, and if it legitimately differs per host the
first multi-instance deployment reports it as a divergence. That is a false
positive, which is noisy; the allowlist's failure mode is a false negative,
which is invisible. Noise is the correct failure direction here.

### The classification lives on the struct field

A separate registry file mapping keys to classes is a second place to
forget. The classification belongs next to the field's doc comment, which
already explains what the field is for, because the person deciding whether
it is per-instance is the person writing that comment:

```go
// Address is the interface and address to bind. Per-host by nature.
Address string `mapstructure:"address" coord:"local"`

// CookieKey signs and encrypts session cookies. Must be identical on
// every instance or sessions break across the load balancer.
CookieKey string `mapstructure:"cookie_key" coord:"strict,secret"`

// RateLimit is the per-client request budget.
RateLimit int `mapstructure:"rate_limit" coord:"warn"`
```

An absent `coord` tag means `warn`, which is the safe default in the sense
established above: it produces a report rather than silence.

### What makes it un-forgettable: a golden test

The tag alone is not enough, because a field added with no tag inherits the
default and nobody is prompted to think. The enforcement is a golden test
built exactly like the two the repo already has
(`server/config/defaults_golden_test.go`, `server/webtypes/golden_test.go`):

```go
// should list every configuration key with its coordination class, so a
// new field cannot join the config without someone classifying it.
func TestCoordinationClasses_ShouldMatchGolden(t *testing.T) {
	t.Parallel()

	configgolden.Assert(t, "./server/config/", "coordination.golden", ClassManifest())
}
```

`ClassManifest` reflects over `config.Config` and emits one sorted line per
leaf:

```
admin.auditor_group = strict
admin.require_group = strict
branding.logo_path = local
branding.org_name = warn
cert_options.user.valid_duration = strict
http.address = local
http.cookie_key = strict,secret
http.port = local
ssh_key = local
```

Add a field, the test fails with a diff naming the new key, and the author
either accepts the default by running `-update` or picks a class first.
Either way the key is now visible in a checked-in file that a reviewer
reads. That is the whole mechanism: it does not prevent a wrong
classification, it prevents an absent one.

This test is worth landing on its own, before any of the runtime machinery.
It is small, it has two precedents in the tree, and once it exists every
subsequent config field arrives pre-classified whether or not anything
compares fingerprints yet.

### The classes

| Class | Meaning | Reported as |
| --- | --- | --- |
| `local` | Legitimately differs per instance. Excluded from the fingerprint entirely. | nothing |
| `warn` | Should match. Divergence degrades consistency but not correctness. | log at warn, shown in the UI |
| `strict` | Must match. Divergence is a correctness fault. | log at error, UI, metric, readiness degradation, admin mail |
| `secret` | A modifier, not a class. The value is never stored or displayed; only its presence or absence and a keyed digest. | as its base class, values redacted |

Starting assignments, by inspection of the current struct:

**`local`**: `http.address` (`types_http.go:26`), `http.port` (`:31`),
`http.unix_socket` (`:37`), `http.tls.*` leaf certificate and key paths,
`http.trusted_proxies` (`:57`, see the caveat below),
`branding.logo_path` (`types.go:96`), `logging` file destinations,
`pubsub.nats.cert_file` / `key_file` / `ca_file`, `ssh_key`
(`types_signer.go:88`), `hsm.pin` and `hsm.pin_file`, `db.connection_string`,
`db.max_open_conns` / `max_idle_conns`.

Three of those need their reasoning recorded, because they look wrong:

- **`ssh_key` is `local`, but the CA identity is `strict`.** The path is a
  per-host filesystem detail and two hosts may legitimately store the same
  key at different paths. What must match is the key itself, which is
  covered by a derived probe (below). Comparing the path would be both a
  false positive (same key, different path) and a false negative (different
  key, same path), and the false negative is the catastrophic one.
- **`hsm.pin` is `local` rather than `strict,secret`.** A wrong PIN fails
  on that host at startup, so cross-instance comparison adds nothing, and
  a PIN is low-entropy enough that storing any digest of it is a real
  disclosure risk. What matters about the HSM is which key it yields, and
  that is the same derived probe as above.
- **`db.connection_string` is `local`.** Instances may legitimately reach
  the same database through different poolers, hosts, or credentials.
  `db.provider` stays `strict`, since two instances on different backends
  are not sharing a database at all.

**`strict`**: `multi_instance`, `http.cookie_key`, `http.public_url`,
`http.cookie_same_site`, `http.cookie_max_age`,
`http.cookie_idle_timeout`, `authentication.*` except any per-host callback
detail, `admin.require_group`, `admin.auditor_group`, all of
`cert_options.*`, `max_cert_lifetime`, `max_service_cert_lifetime`,
`db.provider`, `pubsub.backend`, `pubsub.nats.url`, and the resolved FIPS
mode.

**`warn`**: rate limits, `logging` levels, `production`, `traces`,
`metrics`, `branding.org_name`, `branding.login_notice`, `mail.*` other
than credentials, `db` pool timings.

`http.trusted_proxies` is the uncomfortable one. It is genuinely per-host
when instances sit behind different proxy chains, and it is genuinely
security-relevant: `docs/proposals/service-retrieval-anomaly-policy.md`
already notes that a misconfigured proxy chain collapses every client to
one address. It is classified `local` here, with the note that a divergence
in it changes which source address every downstream policy sees. If the
source-address proposals land, revisit this as `warn`.

### Walking the struct

Four details the walker has to get right, all present in the current config:

1. **`mapstructure:",squash"`** (`types.go:41`). `SignerConfig`'s keys are
   top-level in YAML, so `SSHKey` must render as `ssh_key`, not
   `signer.ssh_key`. Getting this wrong makes the manifest disagree with
   both `defaults.golden` and what the operator sees in their file.
2. **Tag modifiers.** `mapstructure:"max_cert_lifetime,string"`
   (`types_signer.go:102`) carries a `,string` modifier. Split on the comma
   and take the first element, or the key comes out as
   `max_cert_lifetime,string`.
3. **Pointers.** `FIPS *bool` and `CookieSecure *bool` distinguish unset
   from false. Render unset as a distinct token rather than as `false`, or
   two instances that resolve to different behaviour compare equal.
4. **Values, not text.** Reflect over the unmarshalled struct, not over the
   YAML. `time.Duration` then compares as an int64 and renders canonically,
   so `60m` on one host and `1h` on another are correctly equal. Comparing
   file text would report that as a divergence.

Render the result in the same `dotted.key = value` form
`configgolden.Flatten` uses, sorted, one leaf per line. The digest is a hash
over that text. Reusing the format means the fingerprint, the class
manifest, and `defaults.golden` all look alike, which matters when someone
is reading three of them side by side at 2am.

## Derived probes

Some of the worst divergences are not values in the file at all. They are
properties of what the values point at, and a literal key comparison misses
every one of them:

| Probe | Why | Class |
| --- | --- | --- |
| CA public key fingerprint | Two instances both say `ssh_key: /etc/ssoossh/ca` and hold different keys. Invisible in a config diff, catastrophic in effect. Covers the HSM case too, since it is derived from the resolved signer rather than from the file. | `strict` |
| Resolved FIPS mode | `FIPS` unset resolves against the Go runtime's own mode (`types.go:65`), so two hosts running different builds resolve differently from identical config. Compare `Config.FIPSEnabled()`, not the pointer. | `strict` |
| TLS issuer subject | Leaf certificates legitimately differ per host; the issuing CA usually should not. | `warn` |
| Logo file digest | The path is per-host, the image should not be. | `warn` |
| Mail template override digest | Same reasoning: local overrides that differ produce different mail from different instances. | `warn` |
| Build version | Not config, but it changes the meaning of every other comparison. See the rolling-restart trap below. | reported, never a fault on its own |

So the fingerprint is not purely reflective. It is the reflected leaves plus
a small registry of `name -> func(*Config) (string, error)` probes. The
registry is a hand-maintained list, which contradicts the principle above,
but only in one direction: forgetting to add a probe leaves the fingerprint
where it is today, and there is no way to make "notice that a new field
points at a file whose contents matter" automatic.

## Where instances meet

### Option A: deploy-time preflight

`ssoosshd config digest` prints the coordinated digest and per-section
digests. Deploy tooling runs it on every host and refuses to roll if they
disagree.

Cheapest by a wide margin: no schema, no runtime cost, no new failure mode,
and it works for signer-only instances because it never touches the
database. It catches drift before it is live, which is the only option here
that does. It does not catch a hand-edited `/etc/ssoossh/ssoosshd.yaml`
after the fact, and it gives the admin UI nothing.

### Option B: database rendezvous

A `server_instances` table. Each instance upserts its row at startup and
refreshes it on a scheduler job, then reads the live rows and diffs.

Durable, queryable after the fact, survives NATS being down, and feeds the
auditor UI directly with no new transport. Detection is heartbeat-latent,
and it cannot see `ssoosshd sign` at all.

### Option C: NATS announce

An instance publishes its fingerprint on a `config.announce` topic at
startup and on an interval; every instance compares what it receives against
its own. `subjectCalculator` (`server/pubsub/pubsub.go:279`) gives unlisted
topics no queue group, which is fan-out, which is exactly right here and
needs no change to that function.

Immediate, reaches signer-only instances, no schema change. Nothing persists,
so an operator debugging after a restart has nothing to read, and an
instance that has just started has heard from nobody yet.

### Option D: B plus C (recommended)

The database is the durable record and the UI's source. The announce makes
detection immediate rather than heartbeat-latent, and is the only channel
that includes signer instances.

Concretely: API instances write their row and also announce; signer
instances only announce. An API instance receiving an announce from an
instance it has no row for records it as a transient peer, compared and
reported but not persisted as a heartbeat. That keeps the signer visible in
the report without giving it a database dependency it deliberately does not
have.

### Rejected: per-instance HTTP endpoints polled by the UI

Each instance exposes `/api/admin/config/digest`, the UI polls them all. A
load balancer hides individual instances, so there is nothing to address.
This only works in a deployment that already has per-instance DNS, which is
not the deployment shape `docs/configuration.md:129` describes.

### Rejected: a shared config source

Put the config in the database and have every instance read it. This
dissolves the problem entirely and is ruled out by
`server/model/server_secret.go:13`. Recorded here so the next person to
propose it finds the answer.

### Rejected: leave it to configuration management

Ansible, or a k8s ConfigMap checksum annotation. Free, and legitimate for a
fully managed deployment. It is not a reason to skip the in-app check: it
cannot see a hand-edited file, and the check is most valuable exactly where
configuration management is weakest.

## Severity, and the rolling-restart trap

**During any rolling upgrade that changes a key, instances legitimately
disagree for the length of the rollout.** A design that hard-fails startup
on divergence turns every config change into an outage. A design that pages
on it teaches people to ignore the page, which is worse than not having it.

Two mitigations, both worth having:

1. **Version-aware reporting.** Record the build version per instance. When
   the diverging instances also differ in version, report it as "upgrade in
   progress" rather than as a fault. Only same-version instances with
   different config are unambiguously a misconfiguration.
2. **A grace window.** Only escalate divergence that has persisted past a
   configured interval, defaulting to something longer than a normal
   rollout.

So the escalation ladder is:

- Divergence is **never fatal by default.** Startup does not fail. The one
  existing startup error (`config.go:108`) stays as it is: it is checkable
  from a single instance's own config, which is a different thing entirely.
- `warn` divergence: structured log at warn, listed in the UI.
- `strict` divergence, inside the grace window or across versions: log at
  warn, shown in the UI as "reconciling".
- `strict` divergence, past the grace window and same-version: log at error,
  metric gauge set, admin mail sent once (via the `notifiers` queue group,
  not leader election), and optionally a readiness degradation so the load
  balancer can act.
- Fatal-on-divergence exists only behind an explicit opt-in
  (`config_coordination.on_strict_divergence: fail`), for operators who
  would rather an instance refuse to serve than serve differently from its
  peers.

## Mode scoping

`ssoosshd sign` has no HTTP configuration. Comparing a signer against an
API instance on `http.*` is pure noise. Record the mode on each instance and
scope comparison to the intersection of what the two modes actually use:

| Pair | Compared |
| --- | --- |
| `serve` vs `serve` | everything coordinated |
| `serve api` vs `serve api` | everything coordinated |
| `sign` vs `sign` | the squashed signer subset, plus the CA probe |
| `sign` vs any API mode | the signer subset that both consume: `pubsub.*`, `max_cert_lifetime`, `max_service_cert_lifetime`, plus the CA probe |
| `serve` vs `serve api` | everything coordinated, plus a warning that mixing an in-process signer with a broker-based one is probably unintended |

The signer subset is already delimited in the tree:
`server/config/types.go:38` says "`ssoosshd sign` is configured entirely by
this subset". That comment is the definition to implement against, and if it
stops being true the mode scoping breaks quietly, so it is worth a test that
asserts the squashed subset is exactly what the signer path reads.

## Config shape

```yaml
# Cross-instance configuration coordination. Compares this instance's
# coordinated settings against its peers and reports divergence. Purely
# advisory: no instance ever adopts another's value, and configuration
# still comes only from this file.
config_coordination:
  # Off unless multi_instance is true, in which case it defaults on.
  enabled: auto

  # How often to refresh this instance's row and re-compare. Also the
  # staleness bound: a peer whose row has not been refreshed in three
  # intervals is treated as gone rather than as diverging.
  interval: 60s

  # Strict divergence is not escalated until it has persisted this long,
  # so a rolling restart does not page anyone. Set longer than a rollout.
  grace: 15m

  # What to do about a strict divergence that outlives the grace window.
  #   report - log at error, set the metric, mail the admins (default)
  #   degrade - also fail the readiness probe
  #   fail    - also exit
  on_strict_divergence: report

  # Mail the admin group when strict divergence is escalated. Delivery
  # rides the existing notification queue group, so exactly one instance
  # sends, regardless of how many notice.
  notify_admins: true
```

Every key here is itself coordinated, `strict`, which is a small piece of
self-consistency worth having: an operator who disables the check on one
instance should see that reported by the others.

`enabled: auto` rather than a bool because the useful default differs by
deployment and `multi_instance` (`types.go:73`) already tells us which one
we are in.

## Schema

```sql
CREATE TABLE server_instances (
    id                TEXT PRIMARY KEY,          -- generated per process
    hostname          TEXT NOT NULL,
    mode              TEXT NOT NULL,             -- serve | serve-api | sign
    build_version     TEXT NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL,
    last_seen_at      TIMESTAMPTZ NOT NULL,      -- database time, not the instance's
    config_digest     TEXT NOT NULL,             -- over the whole coordinated set
    config_entries    JSONB NOT NULL             -- key -> rendered value or digest
);

CREATE INDEX idx_server_instances_last_seen ON server_instances (last_seen_at);
```

Following the tree's conventions: a per-dialect pair under
`server/resources/migrations/{postgres,sqlite}/`, named
`<timestamp>_server_instances.{up,down}.sql`, and a GORM model with an
explicit `TableName()` as in `server/model/server_secret.go`.

Notes on the columns:

- **`id` is generated per process, not per host.** Two instances on one host
  is a legitimate configuration, and the instance identity has to survive
  neither more nor less than the process does.
- **`last_seen_at` is written with database time.** Instance clocks differ,
  and a staleness bound computed against a skewed clock either evicts live
  instances or keeps dead ones.
- **`config_entries` stores rendered values, not raw config.** Non-secret
  coordinated leaves in the clear, `secret`-class leaves as a keyed digest,
  `local` leaves absent entirely. Storing the per-key values rather than
  only the digest is what lets the report name the diverging keys instead of
  saying "the configs differ", which is the difference between a useful
  report and an annoying one.
- **The keyed digest needs a key.** A new `server_secrets` row
  (`config_digest_key`, following `ServerSecretSessionKey` at
  `server/model/server_secret.go:7`) works for every instance that has a
  database. The signer does not, which is precisely why `hsm.pin` is
  classified `local` above: with the PIN excluded, no `secret`-class leaf
  remains in the signer's subset, and the signer's most important
  comparison, the CA key, is a public-key fingerprint that needs no key at
  all. The problem is dissolved rather than solved.
- **Rows are not deleted on shutdown.** A clean shutdown could delete its
  row, but an unclean one cannot, so the staleness bound has to exist
  regardless and deletion just adds a second mechanism doing the same job.
  Reap rows older than some large multiple of the interval in the same job.

## Where it surfaces

**Structured logs.** At startup after the first comparison, and on each
escalation. The log names the diverging keys, not just the fact of
divergence:

```
level=ERROR msg="configuration diverges from peer instance"
  peer=b3f1... peer_host=api-2 peer_version=1.4.2 local_version=1.4.2
  strict=["cert_options.user.valid_duration","admin.require_group"]
  warn=["http.rate_limit"]
```

**The auditor UI.** `EffectiveConfigResponse`
(`server/webtypes/webtypes.go:349`) and its handler
(`server/controller/admin.go:61`) are already the screen an auditor opens to
ask what policy is in effect. In a multi-instance deployment that screen is
currently answering a subtly wrong question: it shows what policy is in
effect *on whichever instance the load balancer picked*. Extending it is
therefore a correction as much as a feature.

Add a sibling endpoint, `GET /api/admin/instances`, on the same auditor
group, returning one entry per live instance plus a computed divergence
list. The UI renders a matrix: rows are keys that differ, columns are
instances, cells are values, with `secret`-class rows showing "differs" or
"matches" rather than values. Keys that agree everywhere are not rows;
nobody needs to scroll past two hundred matching keys to find the two that
do not.

Wire types go in `server/webtypes` and flow to TypeScript through the
existing tygo generation and golden test (`docs/wire-types.md`,
`server/webtypes/golden_test.go`).

**A metric.** A gauge, labelled by class, that alerting can watch. This is
the only surface an operator sees without logging in, and for a
`strict` divergence that has outlived the grace window it is the one that
matters most.

**Mail.** Through the existing notification path (`server/notify/event.go`),
which already delivers exactly once per deployment because of the
`notifiers` queue group (`server/pubsub/pubsub.go:298`). Without that, N
instances noticing the same divergence would send N copies, which is the
failure mode that trains people to filter the alert.

**A CLI check.** `ssoosshd config-check` prints the same matrix from the
command line, and `ssoosshd config digest` prints this host's digest for
option A's preflight use. Both are useful before the runtime machinery
exists, and the digest subcommand is the smallest useful thing in this
entire document.

## Failure modes and edge cases

- **Rolling restarts.** Covered above. The single most likely reason for
  this feature to be turned off in anger.
- **The comparison must not be leader-gated.** Every instance needs to know
  it is the odd one out, and an instance that cannot reach the leader still
  needs to report. Only the mail is deduplicated, and by queue group rather
  than by leadership.
- **A newly started instance has heard from nobody.** The database read
  covers this for API instances. For a signer, the first announce is a
  request as much as a statement: peers reply with their own fingerprint.
- **Single-instance deployments.** Skip reporting when `multi_instance` is
  false, but still write the row, so enabling multi-instance is not a cold
  start and so `config-check` has something to show.
- **List ordering.** `http.proxy_protocol` order is semantic;
  `cert_options.*.extensions` order is not. Compare as-is by default and
  report an ordering-only difference distinctly from a membership
  difference, so an operator can tell which one they are looking at.
- **Keys in the file that map to no struct field.** Viper keeps them. A
  typo (`cert_option:` for `cert_options:`) on one host only is exactly the
  kind of divergence this feature exists to catch, and the reflective walk
  cannot see it because there is no struct field. Comparing
  `v.AllSettings()` alongside the struct walk catches it, at the cost of
  reporting unknown keys as divergences when only one host carries a
  vendor-specific extra. Worth doing, reported at `warn` and clearly
  labelled as an unknown key.
- **Secrecy of the report.** `config_entries` is as sensitive as the rest of
  the database, which is to say it is already inside the trust boundary. The
  API response is not: it goes to auditors, who by design see a redacted
  view (`webtypes.go:343-348`). The divergence endpoint has to inherit that
  redaction discipline, not reinvent it.
- **A diverging instance that is also unreachable.** Divergence and death
  look similar from a distance. The staleness bound resolves it: no
  heartbeat, no comparison, and the instance is reported as gone rather than
  as agreeing.

## Suggested order

1. **The class manifest and its golden test.** No runtime behaviour, no
   schema, no config. It is small, it has two precedents, and it satisfies
   the "cannot be forgotten" requirement on its own. Every config field
   added after this point arrives classified regardless of when the rest
   lands.
2. **The fingerprint function and the derived probes**, with
   `ssoosshd config digest` as the only consumer. This makes option A
   available immediately: deploy tooling can compare digests across hosts
   with nothing else built.
3. **The `server_instances` table, the heartbeat job, and the comparison**,
   reporting only to logs. Real detection, no new API surface.
4. **The NATS announce**, bringing signer instances in and making detection
   immediate.
5. **The auditor endpoint and the UI matrix**, plus the metric and the mail.

Steps 1 and 2 are worth doing even if nothing after them is ever built.

## Provenance: what was verified and how

| Claim | Re-check |
| --- | --- |
| Config is loaded once, from file plus embedded defaults | `grep -n "func NewConfig" -A 40 server/config/config.go` |
| `cookie_key` is the only cross-instance rule today | `grep -n "multi_instance is enabled" server/config/config.go` |
| Signer config is squashed to top-level keys | `grep -n "squash" server/config/types.go` |
| `ssoosshd sign` runs without a database | `sed -n '154,159p' docs/configuration.md` |
| Leader election does not exist | `grep -rn "leader" server/ --include=*.go` |
| Notifications already deduplicate by queue group | `grep -n "notifiers" -B 6 server/pubsub/pubsub.go` |
| Unlisted topics get fan-out, not a queue group | `grep -n "func subjectCalculator" -A 22 server/pubsub/pubsub.go` |
| A config golden test already exists | `cat server/config/defaults_golden_test.go` |
| `Flatten` renders sorted dotted-key lines | `grep -n "func Flatten" -A 40 test/configgolden/configgolden.go` |
| The auditor config endpoint and its redaction | `grep -n "effectiveConfigHandler" -A 45 server/controller/admin.go` |
| `FIPS` is a pointer resolved against the runtime | `grep -n "FIPS \*bool" -B 10 server/config/types.go` |
| The settings-table position | `sed -n '9,20p' server/model/server_secret.go` |

## Open questions

1. **Does `enabled: auto` earn its complexity?** A plain bool defaulting to
   `multi_instance`'s value is simpler to explain and one fewer state to
   test. `auto` was chosen so that turning on `multi_instance` does not
   quietly leave the check off, but that is an argument about defaults, not
   about tri-states.
2. **Should `strict` divergence degrade readiness by default?** Argued
   above for opt-in, on the grounds that removing an instance from the pool
   because it disagrees can turn a two-instance disagreement into a
   zero-instance outage if both sides degrade. The counter-argument is that
   an instance issuing certificates with the wrong lifetime is worse than
   an instance that is out of the pool. This is the decision most worth
   re-opening.
3. **Does the announce need authentication beyond the NATS mTLS?** Anything
   on the broker is already a trusted peer, so probably not, but the
   announce is the one message in the system whose content is entirely
   about configuration, and it would be the obvious thing to spoof if the
   broker's trust boundary ever widened.
4. **How is `http.trusted_proxies` classified once the source-address
   proposals land?** It is `local` here on deployment-shape grounds, and
   those proposals make it security-relevant. Cross-reference
   [service-retrieval-anomaly-policy.md](service-retrieval-anomaly-policy.md)
   before settling it.
5. **Should the client's configuration participate?** `client/config` has
   its own `defaults.yaml` and its own golden. Client settings enforcement
   is a separate mechanism (`docs/client-settings-enforcement.md`) and this
   document is scoped to servers, but the reflective walk and the class tag
   would work identically there.
6. **What happens on a config change that is intentionally staged?** An
   operator rolling out a longer certificate lifetime deliberately runs
   divergent config for the length of the rollout. The grace window covers
   the common case; an explicit "expected divergence until <time>" would
   cover the deliberate one, and it is not obvious that it is worth the
   machinery.
