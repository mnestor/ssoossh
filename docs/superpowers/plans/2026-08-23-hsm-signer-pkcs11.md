# HSM (PKCS#11) CA Key Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let ssoosshd sign SSH certificates with a CA private key held in a PKCS#11 HSM (developed and CI-tested against SoftHSM2), so the key never exists in config or process memory.

**Architecture:** Phase 0 first: a database-backed CA public key registry — signers announce their public key over pubsub (startup + every 5 min + on a server's "request keys" broadcast), the API/full server persists them with a 15-minute expiry, a scheduler job sweeps expired rows, and `CAService` serves all active keys (sshd `TrustedUserCAKeys` accepts multiple). This removes `CAService`'s dependency on parsing `ssh_key`, which HSM mode makes impossible and split mode makes wrong. Phase 1: a new `HSMKeySource` implements the existing `CAKeySource` seam in `server/signer` (`server/signer/keysource.go:32`), backed by `github.com/eclipse-keypont/crypto11` (maintained successor of ThalesGroup/crypto11). crypto11 returns a `crypto.Signer` whose private-key operations happen inside the HSM; we wrap it into an `ssh.Signer` with `ssh.NewSignerFromSigner`. Bootstrap picks the key source from config: exactly one of `ssh_key` (existing inline PEM) or the new `hsm:` block for signing modes. Single server build: ssoosshd flips to `CGO_ENABLED=1` (it ships linux-only), the Docker runtime image moves from distroless/static to distroless/base.

**Tech Stack:** Go 1.26, cgo, github.com/eclipse-keypont/crypto11, golang.org/x/crypto/ssh v0.54.0, zig cc (release cross-compiler, pins glibc 2.28), SoftHSM2 + opensc (pkcs11-tool) for tests.

**Spec:** No separate spec doc; the Context section below records the decisions made with the user (2026-08-23 session).

## Context

- The signer already has the exact seam for this: `CAKeySource` in `server/signer/keysource.go:32-34`, documented as the place PKCS#11 would plug in. `Sign()` (`server/signer/sign.go`) and the handler never touch key material directly.
- User decisions:
  - **Binding:** cgo + crypto11, **single build** — no build tags, no server variants. ssoosshd is linux-only in releases (`.goreleaser.yml` `server-linux-build`).
  - **glibc compatibility:** cgo binaries demand the versioned glibc symbols of the build host, and the user has been burned by this before (custom old-glibc build images). Decision: release builds of ssoosshd use **zig cc targeting glibc 2.28** (`x86_64-linux-gnu.2.28` / `aarch64-linux-gnu.2.28`), so artifacts run on distros as old as RHEL 8 / Ubuntu 20.04 / Debian 11 without a custom build image. zig covers both arches from one toolchain. CI test/lint builds (not shipped) use plain gcc. Alternatives considered and rejected: purego custom FFI binding (~450-700 lines owned unsafe code), old-glibc builder image (another image to maintain), ssh-agent bridge (extra runtime process).
  - **musl/Alpine:** the glibc artifact cannot run on musl even in PEM-key mode (linkage is a binary property, not a config one). Decision: ship **one additional musl server artifact** — `zig cc -target x86_64/aarch64-linux-musl`, dynamically linked so `dlopen` (and therefore HSM mode) works on Alpine — as a tar.gz plus an `apk` package via nfpm. Client binaries stay static and unaffected.
  - **Algorithms:** ECDSA (P-256/P-384/P-521) + RSA (>= 2048, signing as rsa-sha2). Ed25519 is NOT supported on the HSM path (crypto11 limitation); document the gap. `ssh_key` PEM path keeps supporting Ed25519 unchanged.
- Consequences accepted by the user: ssoosshd becomes dynamically linked (glibc); dev/CI toolchains that compile server packages need a C compiler; Docker base becomes `gcr.io/distroless/base-debian12:nonroot`.
  - **CA public key distribution (Phase 0, must land first):** `server/service.CAService` serves the CA public key by parsing `ssh_key` — impossible in HSM mode and wrong in split mode. User's design: a database-backed registry of signer public keys with expiration, refreshed periodically by signers and swept by a periodic job; plus a "request keys" broadcast so startup order between API server and signers doesn't matter; sshd's `TrustedUserCAKeys` accepts multiple keys, so all registered signer keys are served together. One adjustment to the sketch, forced by the signer's zero-DB constraint (`server/signer` package doc, `zerodb_test.go`): the signer never writes the database — it *announces* its public key over pubsub, and the API/full server persists it. Full mode additionally seeds the registry synchronously at boot from its in-process key source, so there is no first-boot gap. Side benefit: key rotation naturally serves old + new keys during the overlap window.
- crypto11 facts verified 2026-08-23: module `github.com/eclipse-keypont/crypto11` (v1.6.8, ThalesGroup/crypto11 is deprecated in its favor), requires cgo, `Configure(&crypto11.Config{Path, TokenLabel, Pin, ...})`, `FindKeyPair(id, label []byte)`, supports RSA/ECDSA/DSA, **not Ed25519**. Gotcha: `FindKeyPair` returns `nil, nil` when no key matches.

## Global Constraints

- Go 1.26+; standard library preferred; wrap errors with `%w`; no `init()`; no package-level state; `context.Context` first param for I/O; interfaces defined in the implementing package.
- `server/signer` must keep **zero database access** and must NOT import `server/config` (see package doc `server/signer/keysource.go:1-17`); bootstrap passes raw values.
- Table-driven tests, colocated `_test.go`, descriptive "should X when Y" names; untestable code documented at the function + exact line ranges added to `exclude-from-coverage.txt`.
- Conventional commits; branch already created: `worktree-feat+hsm-signer` (worktree `.claude/worktrees/feat+hsm-signer`).
- Prefix all shell commands with `rtk`.
- Before pushing: `golangci-lint run` and `go test ./...` (now with `CGO_ENABLED=1` for server packages).
- Import grouping: stdlib / external / `github.com/mnestor/ssoossh` (goimports local-prefixes).

---

## Phase 0: CA public key registry (must land before the HSM tasks)

Independent of HSM — it fixes split-mode public key serving for today's PEM signers too, and Task 5 depends on it. Timing constants (announce every 5 min, registry TTL 15 min, sweep hourly) are defaults; make them consts in one place.

### Task 0.1: Announce/request messages and topics in certmsg

**Files:**
- Modify: `server/certmsg/certmsg.go`
- Test: `server/certmsg/certmsg_test.go`

**Interfaces:**
- Produces:
  ```go
  const CAKeyAnnounceTopic = "ca.key.announce" // signers -> servers: here is my CA public key
  const CAKeyRequestTopic  = "ca.key.request"  // servers -> signers: (re)announce now
  type CAKeyAnnounce struct {
      PublicKey   string    // authorized_keys format, single line
      AnnouncedAt time.Time
  }
  ```
  Deliberately no fingerprint field: the listener computes it server-side from
  the parsed key (Task 0.3), so a mismatched or differently-formatted announce
  can never create a duplicate registry row. Two signers configured with the
  same CA key (a supported HA setup) must collapse to one row.

  plus Marshal/Unmarshal helpers mirroring however `SigningJob`/`SignedReply` serialize (read `certmsg.go` and copy the pattern exactly). The request message carries no payload — an empty body is fine; mirror how existing topics handle it.

- [ ] Write failing tests mirroring `certmsg_test.go`'s style (round-trip marshal, topic constant pins), implement, run `rtk go test ./server/certmsg/`, commit `feat(certmsg): add ca key announce and request messages`.

### Task 0.2: Signer-side announcer (no DB — zerodb stays intact)

**Files:**
- Create: `server/signer/announce.go`
- Test: `server/signer/announce_test.go`

**Interfaces:**
- Consumes: `CAKeySource` (existing), `certmsg.CAKeyAnnounce` (Task 0.1), watermill publisher/subscriber (same types `Handler` uses — read `server/signer/handler.go`).
- Produces:
  ```go
  // NewAnnouncer builds the component that tells servers this signer's CA
  // public key. Register subscribes to CAKeyRequestTopic (re-announce on
  // demand); Run announces at startup then every interval until ctx ends —
  // shaped to sit in bootstrap's serviceRunners next to pubSub.Run.
  func NewAnnouncer(ks CAKeySource, pub message.Publisher, interval time.Duration) *Announcer
  func (an *Announcer) Register(router *message.Router, sub message.Subscriber)
  func (an *Announcer) Run(ctx context.Context) error
  ```
  Announce body: `ssh.MarshalAuthorizedKey(signer.PublicKey())` (trimmed). Works identically for PEM and (later) HSM key sources.

- [ ] Tests first (staticKeySource fake from `sign_test.go` + whatever in-memory pubsub the existing handler tests use): "should announce on request message", "should announce at startup", "should announce marshaled authorized_keys form of the source key". Implement, verify `rtk go test ./server/signer/` and that `zerodb_test.go` still passes. Commit `feat(signer): announce ca public key over pubsub`.

### Task 0.3: Registry persistence, listener, and expiry sweep

**Files:**
- Create: `server/model/ca_signer_key.go` (+ migrations for sqlite and postgres under `server/resources/` — copy the newest migration pair's naming)
- Create: `server/service/cakeyregistry.go`
- Test: `server/service/cakeyregistry_test.go` (use the same test-DB harness other service tests use)
- Modify: `server/bootstrap/scheduler.go` (new sweep job, following the `sweepJobName`/`RegisterJob` pattern at `server/bootstrap/scheduler.go:13,57`)

**Interfaces:**
- Produces:
  ```go
  // model: table ca_signer_keys
  type CASignerKey struct {
      Fingerprint string    `gorm:"primaryKey"` // SHA256, computed server-side — the dedup key
      PublicKey   string    // canonical authorized_keys form (re-marshaled after parse)
      ExpiresAt   time.Time // refreshed on every announce
      CreatedAt   time.Time
      UpdatedAt   time.Time
  }
  // service
  func NewCAKeyRegistry(db *gorm.DB, ttl time.Duration) *CAKeyRegistry
  // Upsert parses ann.PublicKey (ssh.ParseAuthorizedKey — reject unparseable
  // announces), re-marshals it to canonical form, computes the SHA256
  // fingerprint itself, and upserts keyed on that fingerprint with
  // ExpiresAt = now + ttl. Canonicalize-then-fingerprint is what guarantees
  // dedup: N signers sharing one CA key (HA setup), or the same key with
  // whitespace/comment differences, always land on the same single row.
  func (r *CAKeyRegistry) Upsert(ctx context.Context, ann certmsg.CAKeyAnnounce) error
  func (r *CAKeyRegistry) ActiveKeys(ctx context.Context) ([]string, error)              // PublicKey where ExpiresAt > now, ordered stably
  func (r *CAKeyRegistry) DeleteExpired(ctx context.Context) (int64, error)
  // listener registered on API/full router: CAKeyAnnounceTopic -> Upsert
  func NewCAKeyListener(reg *CAKeyRegistry) *CAKeyListener
  func (l *CAKeyListener) Register(router *message.Router, sub message.Subscriber)
  ```
- Consumes: Task 0.1 messages. TTL default 15m (3 missed announces before a key drops out).

- [ ] TDD the registry (upsert refreshes expiry; expired keys excluded from ActiveKeys; DeleteExpired removes only expired; **"should keep one row when two signers announce the same key"** — same key announced twice with different comment/whitespace yields exactly one row whose expiry advanced; "should reject unparseable public key"), then the listener (announce message → row exists), then register the hourly sweep job in `scheduler.go`. `ActiveKeys` never returns expired rows, so the sweep is hygiene, not correctness — safe at any cadence. Commit `feat(server): persist announced ca signer keys with expiry sweep`.

### Task 0.4: CAService reads the registry; wire both directions in bootstrap

**Files:**
- Modify: `server/service/ca.go` (`NewCAService` at line 31, `GetCAPublicKey` at line 47)
- Modify: `server/bootstrap/pipeline.go`, `server/bootstrap/bootstrap.go`
- Modify: `server/config/types_signer.go` (mode-aware key requirement — see below)
- Test: colocated with each

**Interfaces:**
- Consumes: `CAKeyRegistry.ActiveKeys` (Task 0.3), `Announcer` (Task 0.2).
- Produces: `GetCAPublicKey` returns all active registry keys joined with `"\n"` — sshd's `TrustedUserCAKeys` file is one key per line, so multi-key output is drop-in for consumers writing that file. Check every `CAPublicKeyProvider` consumer (`server/service/ca.go:15`) for single-key assumptions before changing.

- [ ] Steps:
  1. `CAService` takes the registry (drop the private-key parse entirely — its doc comment at `server/signer/keysource.go:39-43` already wanted the split). `GetCAPublicKey` = `strings.Join(ActiveKeys(ctx), "\n")`; error when no active keys ("no signer has registered a CA key yet").
  2. Bootstrap full/signer modes: add the `Announcer` to the router + serviceRunners. Full mode also seeds synchronously: `registry.Upsert` directly from the in-process key source before serving, so a fresh full-mode boot never serves an empty key list.
  3. Bootstrap API/full modes: register `CAKeyListener`; publish one `CAKeyRequestTopic` message at startup so already-running signers re-announce immediately (startup order stops mattering).
  4. Config validation: the key-source requirement (exactly one of `ssh_key`/`hsm`) applies to modes that *sign* — full and signer-only. API mode needs neither (its keys come from the registry). `SignerConfig.Validate` is called from `NewConfig` for every mode without knowing the mode — read how `BootstrapServe`/`BootstrapSigner`/API mode differ (`server/bootstrap/bootstrap.go`, `modes_test.go`) and either pass the mode into validation or move the key-source check into the mode inits. Pin behavior with tests: API mode boots with no key source; full/signer modes still fail fast without one.
  5. Commit `feat(server): serve ca public keys from the signer registry`.

---

## Phase 1: HSM key source

### Task 1: HSM config block and validation

**Files:**
- Modify: `server/config/types_signer.go`
- Test: `server/config/types_signer_test.go` (create if absent; a `types_*_test.go` may exist — colocate either way)

**Interfaces:**
- Consumes: existing `SignerConfig` (squashed into `Config`, YAML keys top-level).
- Produces: `HSMConfig` struct with `Enabled() bool`, `ResolvePIN() (string, error)`, `KeyIDBytes() ([]byte, error)`; `SignerConfig.HSM HSMConfig` under YAML key `hsm:`; updated `SignerConfig.Validate()` enforcing exactly one key source.

- [ ] **Step 1: Write failing table-driven validation tests**

```go
// server/config/types_signer_test.go (add to existing file if present)
func TestSignerConfigValidate_HSM(t *testing.T) {
	valid := HSMConfig{
		Module: "/usr/lib/softhsm/libsofthsm2.so", TokenLabel: "ca",
		PIN: "1234", KeyLabel: "ssoossh-ca",
	}
	tests := []struct {
		name    string
		sshKey  string
		hsm     HSMConfig
		wantErr string
	}{
		{"should accept hsm block alone when complete", "", valid, ""},
		{"should reject when both ssh_key and hsm set", "PEM", valid, "exactly one"},
		{"should reject when neither ssh_key nor hsm set", "", HSMConfig{}, "ssh_key"},
		{"should reject hsm without token_label", "", HSMConfig{Module: "m", PIN: "p", KeyLabel: "k"}, "token_label"},
		{"should reject hsm with both pin and pin_file", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p", PINFile: "/f", KeyLabel: "k"}, "one of pin"},
		{"should reject hsm with neither pin nor pin_file", "", HSMConfig{Module: "m", TokenLabel: "t", KeyLabel: "k"}, "pin"},
		{"should reject hsm without key_label or key_id", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p"}, "key_label"},
		{"should reject non-hex key_id", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p", KeyID: "zz"}, "key_id"},
		{"should accept key_id alone as hex", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p", KeyID: "0a1b"}, ""},
	}
	// each case: build SignerConfig{SSHKey: tt.sshKey, HSM: tt.hsm, PubSub: <valid pubsub>},
	// call Validate(), assert err nil or contains wantErr.
}

func TestHSMConfigResolvePIN(t *testing.T) {
	// "should return inline pin when set"
	// "should read and trim pin_file when set" (t.TempDir file with "1234\n")
	// "should error when pin_file unreadable"
}
```

Copy a valid `PubSubConfig` literal from existing tests in `server/config` so `PubSub.Validate()` passes.

- [ ] **Step 2: Run to verify failure**

Run: `rtk go test ./server/config/ -run 'TestSignerConfigValidate_HSM|TestHSMConfigResolvePIN'`
Expected: FAIL — `HSMConfig` undefined.

- [ ] **Step 3: Implement HSMConfig + Validate changes**

```go
// server/config/types_signer.go
// HSMConfig configures a PKCS#11 (HSM) backed CA key. When Module is set the
// signer loads the CA key from the token instead of ssh_key. Developed and
// tested against SoftHSM2; any PKCS#11 module should work. Ed25519 CA keys
// are not supported on this path (Go PKCS#11 limitation) — use ECDSA or RSA.
type HSMConfig struct {
	// Module is the absolute path to the PKCS#11 shared library,
	// e.g. /usr/lib/softhsm/libsofthsm2.so. Setting it enables HSM mode.
	Module string `mapstructure:"module"`
	// TokenLabel selects the token (softhsm2-util --init-token --label ...).
	TokenLabel string `mapstructure:"token_label"`
	// PIN is the user PIN. Mutually exclusive with PINFile.
	PIN string `mapstructure:"pin"`
	// PINFile is a path whose trimmed contents are the user PIN. Preferred
	// over inline PIN so the config file can stay world-readable-ish.
	PINFile string `mapstructure:"pin_file"`
	// KeyLabel selects the key pair by CKA_LABEL. At least one of KeyLabel
	// or KeyID is required; when both are set both must match.
	KeyLabel string `mapstructure:"key_label"`
	// KeyID selects the key pair by CKA_ID, hex-encoded (pkcs11-tool --id).
	KeyID string `mapstructure:"key_id"`
}

// Enabled reports whether an HSM-backed CA key is configured.
func (h *HSMConfig) Enabled() bool { return h.Module != "" }

// ResolvePIN returns the user PIN, reading PINFile if configured. Validate
// has already enforced that exactly one of PIN/PINFile is set.
func (h *HSMConfig) ResolvePIN() (string, error) {
	if h.PINFile == "" {
		return h.PIN, nil
	}
	b, err := os.ReadFile(h.PINFile)
	if err != nil {
		return "", fmt.Errorf("read hsm pin_file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// KeyIDBytes decodes the hex KeyID; empty when unset.
func (h *HSMConfig) KeyIDBytes() ([]byte, error) {
	if h.KeyID == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(h.KeyID)
	if err != nil {
		return nil, fmt.Errorf("hsm key_id is not valid hex: %w", err)
	}
	return b, nil
}

// validate rejects an HSM block that cannot select and unlock a key.
func (h *HSMConfig) validate() error {
	if h.TokenLabel == "" {
		return fmt.Errorf("hsm token_label is required")
	}
	if (h.PIN == "") == (h.PINFile == "") {
		return fmt.Errorf("exactly one of hsm pin or pin_file is required")
	}
	if h.KeyLabel == "" && h.KeyID == "" {
		return fmt.Errorf("at least one of hsm key_label or key_id is required")
	}
	if _, err := h.KeyIDBytes(); err != nil {
		return err
	}
	return nil
}
```

Add to `SignerConfig`:

```go
	// HSM optionally sources the CA key from a PKCS#11 token instead of
	// ssh_key. Exactly one of the two must be configured.
	HSM HSMConfig `mapstructure:"hsm"`
```

Rework `Validate()` (replacing the bare `SSHKey == ""` check at `server/config/types_signer.go:30`):

```go
	hsmEnabled := s.HSM.Enabled()
	switch {
	case s.SSHKey == "" && !hsmEnabled:
		return fmt.Errorf("ssh_key or hsm is required: one CA private key source is needed, and no mode can issue certificates without it")
	case s.SSHKey != "" && hsmEnabled:
		return fmt.Errorf("exactly one of ssh_key and hsm may be set: two CA key sources would be ambiguous")
	case hsmEnabled:
		if err := s.HSM.validate(); err != nil {
			return err
		}
	}
	return nil
```

- [ ] **Step 4: Run tests to verify pass**

Run: `rtk go test ./server/config/`
Expected: PASS (including pre-existing tests — if an existing test asserts the old "ssh_key is required" message, update it to the new message).

- [ ] **Step 5: Commit**

```bash
rtk git add server/config/ && rtk git commit -m "feat(config): add hsm pkcs11 block to signer config"
```

---

### Task 2: Algorithm-gating CA signer wrapper (pure Go, fully unit-tested)

**Files:**
- Create: `server/signer/hsmkeysource.go` (wrapper part only; crypto11 glue is Task 3)
- Test: `server/signer/hsmkeysource_test.go`

**Interfaces:**
- Consumes: `crypto.Signer` (stdlib).
- Produces: `func wrapCASigner(s crypto.Signer) (ssh.Signer, error)` — converts a crypto.Signer into an ssh.Signer, allowing ECDSA P-256/P-384/P-521 and RSA >= 2048 (restricted to rsa-sha2-512/rsa-sha2-256 signatures), rejecting everything else (notably Ed25519) with a clear error. Task 3's `HSMKeySource` and Task 4's integration tests call it.

- [ ] **Step 1: Write failing table-driven tests**

```go
// server/signer/hsmkeysource_test.go
func TestWrapCASigner(t *testing.T) {
	// generate throwaway keys with stdlib: ecdsa P-256/P-384/P-521,
	// rsa 2048, rsa 1024, ed25519
	tests := []struct {
		name     string
		key      crypto.Signer
		wantType string // expected ssh public key type, "" if error
		wantErr  string
	}{
		{"should wrap ecdsa p256", ecP256, "ecdsa-sha2-nistp256", ""},
		{"should wrap ecdsa p384", ecP384, "ecdsa-sha2-nistp384", ""},
		{"should wrap ecdsa p521", ecP521, "ecdsa-sha2-nistp521", ""},
		{"should wrap rsa 2048 restricted to rsa-sha2", rsa2048, "ssh-rsa", ""},
		{"should reject rsa below 2048", rsa1024, "", "at least 2048"},
		{"should reject ed25519 with hsm guidance", edKey, "", "not supported"},
	}
	// assert: wrapped.PublicKey().Type() == wantType; for the rsa case also
	// assert the signer implements ssh.MultiAlgorithmSigner and
	// Algorithms()[0] == ssh.KeyAlgoRSASHA512 (SignCert signs with
	// Algorithms()[0], so this is what keeps rsa CA signatures off SHA-1).
	// One more test: sign an *ssh.Certificate with the wrapped p256 signer
	// via cert.SignCert(rand.Reader, wrapped) and verify with
	// cert.CheckSignature? Simplest end check: ssh.NewCertChecker is
	// overkill — call cert.Verify? Use the same verification helper style
	// as server/signer/sign_test.go (read it and mirror its approach).
}
```

- [ ] **Step 2: Run to verify failure**

Run: `rtk go test ./server/signer/ -run TestWrapCASigner`
Expected: FAIL — `wrapCASigner` undefined.

- [ ] **Step 3: Implement wrapCASigner**

```go
// server/signer/hsmkeysource.go
// wrapCASigner converts an HSM-backed crypto.Signer into the ssh.Signer the
// pipeline signs certificates with. It gates algorithms to what the HSM path
// supports: ECDSA P-256/384/521 and RSA >= 2048 bits. RSA signers are
// restricted to rsa-sha2-512/256 — ssh.Certificate.SignCert uses
// MultiAlgorithmSigner.Algorithms()[0], and an unrestricted RSA signer would
// produce legacy SHA-1 ssh-rsa signatures. Ed25519 is rejected: the Go
// PKCS#11 stack (crypto11) cannot sign with it; use the ssh_key PEM source
// for Ed25519 CAs.
func wrapCASigner(s crypto.Signer) (ssh.Signer, error) {
	switch pub := s.Public().(type) {
	case *ecdsa.PublicKey:
		switch pub.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
		default:
			return nil, fmt.Errorf("unsupported ECDSA curve %q for HSM CA key", pub.Curve.Params().Name)
		}
		return ssh.NewSignerFromSigner(s)
	case *rsa.PublicKey:
		if pub.N.BitLen() < 2048 {
			return nil, fmt.Errorf("HSM CA RSA key is %d bits, must be at least 2048", pub.N.BitLen())
		}
		signer, err := ssh.NewSignerFromSigner(s)
		if err != nil {
			return nil, fmt.Errorf("wrap HSM RSA key: %w", err)
		}
		as, ok := signer.(ssh.AlgorithmSigner)
		if !ok {
			return nil, fmt.Errorf("HSM RSA signer does not support algorithm selection")
		}
		return ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256})
	default:
		return nil, fmt.Errorf("key type %T is not supported for HSM CA keys (ECDSA P-256/384/521 or RSA >= 2048; Ed25519 requires the ssh_key source)", pub)
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `rtk go test ./server/signer/ -run TestWrapCASigner`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add server/signer/ && rtk git commit -m "feat(signer): add algorithm-gated ca signer wrapper for hsm keys"
```

---

### Task 3: HSMKeySource backed by crypto11

**Files:**
- Modify: `server/signer/hsmkeysource.go` (add the crypto11-backed type)
- Modify: `go.mod` / `go.sum` (new dependency)
- Modify: `exclude-from-coverage.txt` (crypto11 glue lines, see Step 4)

**Interfaces:**
- Consumes: `wrapCASigner` (Task 2); `github.com/eclipse-keypont/crypto11`.
- Produces:
  ```go
  type HSMParams struct {
      Module     string // path to PKCS#11 .so
      TokenLabel string
      PIN        string
      KeyID      []byte // nil when selecting by label only
      KeyLabel   string // "" when selecting by id only
  }
  func NewHSMKeySource(p HSMParams) (*HSMKeySource, error)
  func (s *HSMKeySource) Signer(ctx context.Context) (ssh.Signer, error)
  func (s *HSMKeySource) Close() error
  ```
  `HSMKeySource` implements `CAKeySource` (`server/signer/keysource.go:32`). Bootstrap (Task 5) constructs it and registers `Close` on shutdown.

- [ ] **Step 1: Add the dependency (verify the version, do not guess)**

```bash
rtk go get github.com/eclipse-keypont/crypto11@latest
rtk go mod tidy
```

Confirm what resolves (expected around v1.6.8 as of 2026-07-29; if a `/v2` module exists, read its changelog before choosing it). Note: from this commit on, `go build ./...` needs `CGO_ENABLED=1` and a C compiler for server packages.

- [ ] **Step 2: Implement HSMKeySource**

The constructor mirrors `NewConfigKeySource`'s shape (parse/connect once at construction, fail at boot, cache the ssh.Signer):

```go
// HSMKeySource is a CAKeySource whose private key lives in a PKCS#11 token.
// The key never enters process memory: crypto11 hands back a crypto.Signer
// that performs each signature inside the HSM. Construction connects, logs
// in, and locates the key so a misconfigured HSM fails at boot, matching
// ConfigKeySource's fail-at-startup behavior. Close releases the PKCS#11
// context; bootstrap runs it on shutdown.
type HSMKeySource struct {
	signer ssh.Signer
	ctx11  *crypto11.Context
}

// NewHSMKeySource opens the PKCS#11 module and resolves the CA key pair.
//
// No unit test exercises the crypto11 calls below — they require a real
// PKCS#11 module. hsmkeysource_softhsm_test.go covers them against SoftHSM2
// behind the softhsm build tag (run in CI by .github/workflows/hsm.yaml);
// the pure logic (algorithm gating) is unit-tested via wrapCASigner. The
// crypto11 call lines are listed in exclude-from-coverage.txt.
func NewHSMKeySource(p HSMParams) (*HSMKeySource, error) {
	ctx11, err := crypto11.Configure(&crypto11.Config{
		Path:       p.Module,
		TokenLabel: p.TokenLabel,
		Pin:        p.PIN,
	})
	if err != nil {
		return nil, fmt.Errorf("open PKCS#11 module %s: %w", p.Module, err)
	}
	kp, err := ctx11.FindKeyPair(p.KeyID, []byte(p.KeyLabel))
	if err != nil {
		_ = ctx11.Close()
		return nil, fmt.Errorf("find CA key pair in HSM: %w", err)
	}
	if kp == nil { // crypto11 returns nil, nil when nothing matches
		_ = ctx11.Close()
		return nil, fmt.Errorf("no key pair found in HSM token %q matching label %q / id %x", p.TokenLabel, p.KeyLabel, p.KeyID)
	}
	signer, err := wrapCASigner(kp)
	if err != nil {
		_ = ctx11.Close()
		return nil, err
	}
	return &HSMKeySource{signer: signer, ctx11: ctx11}, nil
}

// Signer implements CAKeySource.
func (s *HSMKeySource) Signer(context.Context) (ssh.Signer, error) {
	return s.signer, nil
}

// Close releases the PKCS#11 sessions and unloads the module.
func (s *HSMKeySource) Close() error {
	return s.ctx11.Close()
}
```

Check crypto11's actual API when writing this (`FindKeyPair(id, label []byte)` signature, whether `[]byte(p.KeyLabel)` must be nil when empty — pass `nil` for empty label/id, not `[]byte{}`).

- [ ] **Step 3: Build and run existing tests with cgo**

Run: `CGO_ENABLED=1 rtk go build ./... && CGO_ENABLED=1 rtk go test ./server/signer/`
Expected: compiles; existing signer tests still PASS (nothing constructs HSMKeySource yet).

- [ ] **Step 4: Record coverage exclusions**

Add the `NewHSMKeySource` crypto11-call line ranges to `exclude-from-coverage.txt` following its existing format (the function comment in Step 2 already documents why, per `.claude/rules/test-go.md`).

- [ ] **Step 5: Commit**

```bash
rtk git add go.mod go.sum server/signer/ exclude-from-coverage.txt
rtk git commit -m "feat(signer): add pkcs11-backed hsm ca key source via crypto11"
```

---

### Task 4: SoftHSM2 integration tests (build tag `softhsm`)

**Files:**
- Create: `server/signer/hsmkeysource_softhsm_test.go` (`//go:build softhsm`)

**Interfaces:**
- Consumes: `NewHSMKeySource`, `HSMParams` (Task 3); `softhsm2-util` and `pkcs11-tool` binaries; module at `SSOOSSH_TEST_PKCS11_MODULE` or common paths.
- Produces: nothing for later tasks; CI job (Task 6) runs `go test -tags=softhsm ./server/signer/`.

- [ ] **Step 1: Write the integration test**

```go
//go:build softhsm

package signer

// findSofthsmModule returns the SoftHSM2 PKCS#11 module path or skips.
// Checks SSOOSSH_TEST_PKCS11_MODULE, then Debian/Ubuntu locations.
func findSofthsmModule(t *testing.T) string {
	if p := os.Getenv("SSOOSSH_TEST_PKCS11_MODULE"); p != "" { return p }
	for _, p := range []string{
		"/usr/lib/softhsm/libsofthsm2.so",
		"/usr/lib/x86_64-linux-gnu/softhsm/libsofthsm2.so",
		"/usr/lib/aarch64-linux-gnu/softhsm/libsofthsm2.so",
	} {
		if _, err := os.Stat(p); err == nil { return p }
	}
	t.Skip("softhsm2 module not found; install softhsm2 or set SSOOSSH_TEST_PKCS11_MODULE")
	return ""
}

// provisionToken creates an isolated SoftHSM2 token store in t.TempDir and
// generates one key of the given type, returning the module path. Each test
// gets its own token dir via SOFTHSM2_CONF so tests stay parallel-safe and
// leave no state behind.
func provisionToken(t *testing.T, keyType, label, id string) string {
	dir := t.TempDir()
	tokens := filepath.Join(dir, "tokens")
	os.MkdirAll(tokens, 0o755)
	conf := filepath.Join(dir, "softhsm2.conf")
	os.WriteFile(conf, []byte("directories.tokendir = "+tokens+"\nobjectstore.backend = file\n"), 0o644)
	t.Setenv("SOFTHSM2_CONF", conf)
	module := findSofthsmModule(t)
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "SOFTHSM2_CONF="+conf)
		out, err := cmd.CombinedOutput()
		if err != nil { t.Fatalf("%s %v: %v\n%s", name, args, err, out) }
	}
	run("softhsm2-util", "--init-token", "--free", "--label", "test-token", "--pin", "1234", "--so-pin", "123456")
	run("pkcs11-tool", "--module", module, "--login", "--pin", "1234",
		"--keypairgen", "--key-type", keyType, "--label", label, "--id", id)
	return module
}

// Tests (each: provision, NewHSMKeySource, Signer(ctx), sign a minimal
// *ssh.Certificate with SignCert, verify the signature against
// signer.PublicKey() — mirror the cert construction used in sign_test.go):
//   TestHSMKeySource_ShouldSignWithECDSAKeyFromSoftHSM   (EC:prime256v1)
//   TestHSMKeySource_ShouldSignRSAWithSHA2FromSoftHSM    (rsa:2048; assert
//       cert.Signature.Format == "rsa-sha2-512")
//   TestHSMKeySource_ShouldFailWhenKeyLabelMissing        (provision EC key,
//       ask for label "nope"; expect error containing "no key pair found")
//   TestHSMKeySource_ShouldFailWhenPINWrong               (PIN "9999";
//       expect error from Configure)
```

Note `t.Setenv` disallows `t.Parallel()` in the same test — accept serial execution here; the suite is 4 short tests.

- [ ] **Step 2: Run locally (requires softhsm2 + opensc installed)**

```bash
sudo apt-get install -y softhsm2 opensc   # once, on the dev machine
CGO_ENABLED=1 rtk go test -tags=softhsm ./server/signer/ -run TestHSMKeySource -v
```
Expected: PASS (or Skip on machines without softhsm2 — verify the skip path works too by unsetting the module env and hiding the paths... skip-path verification is manual, not automated).

- [ ] **Step 3: Verify default test runs still exclude these**

Run: `CGO_ENABLED=1 rtk go test ./server/signer/`
Expected: PASS, HSM tests not run (tag absent).

- [ ] **Step 4: Commit**

```bash
rtk git add server/signer/ && rtk git commit -m "test(signer): exercise hsm key source against softhsm2 behind softhsm tag"
```

---

### Task 5: Bootstrap wiring and shutdown

**Files:**
- Modify: `server/bootstrap/pipeline.go` (`initSignerHandler`, lines 44-69)
- Modify: `server/bootstrap/bootstrap.go` (register key source Close on the two `shutdowns` managers, following the `a.stopSessionCleanup` precedent at line 137)
- Test: `server/bootstrap/modes_test.go` or a new `pipeline_test.go` colocated with existing bootstrap tests

**Interfaces:**
- Consumes: `config.Signer.HSM` (Task 1), `signer.NewHSMKeySource`/`HSMParams` (Task 3), existing `signer.NewConfigKeySource`, `fipsmode` checks.
- Produces: `(a *app) newCAKeySource() (signer.CAKeySource, error)` plus an `a.closeCAKeySource func() error` field (nil-safe) registered with `shutdowns.Add` in both `BootstrapServe` and `BootstrapSigner`.

- [ ] **Step 1: Write failing test for source selection**

```go
// "should build hsm key source when hsm module configured" — hard to
// assert without a real module; instead test the failure shape:
// config with HSM.Module=/nonexistent.so → newCAKeySource returns error
// mentioning "PKCS#11".
// "should build config key source when ssh_key configured" — valid PEM
// (reuse the test key helper existing bootstrap/config tests use) →
// returns *signer.ConfigKeySource, closeCAKeySource stays nil.
// "should fail when pin_file unreadable" — HSM block with PINFile
// pointing into an empty t.TempDir → error mentioning "pin_file".
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=1 rtk go test ./server/bootstrap/ -run TestNewCAKeySource`
Expected: FAIL — method undefined.

- [ ] **Step 3: Implement selection in initSignerHandler**

Replace the direct `signer.NewConfigKeySource` call (`server/bootstrap/pipeline.go:49`):

```go
// newCAKeySource builds the CAKeySource the signer handler signs with:
// PKCS#11-backed when the hsm block is configured, otherwise the inline
// ssh_key PEM. Config validation has already enforced exactly one source.
func (a *app) newCAKeySource() (signer.CAKeySource, error) {
	if !a.config.Signer.HSM.Enabled() {
		return signer.NewConfigKeySource(a.config.Signer.SSHKey)
	}
	pin, err := a.config.Signer.HSM.ResolvePIN()
	if err != nil {
		return nil, err
	}
	keyID, err := a.config.Signer.HSM.KeyIDBytes()
	if err != nil {
		return nil, err
	}
	ks, err := signer.NewHSMKeySource(signer.HSMParams{
		Module:     a.config.Signer.HSM.Module,
		TokenLabel: a.config.Signer.HSM.TokenLabel,
		PIN:        pin,
		KeyID:      keyID,
		KeyLabel:   a.config.Signer.HSM.KeyLabel,
	})
	if err != nil {
		return nil, err
	}
	a.closeCAKeySource = ks.Close
	return ks, nil
}
```

In `initSignerHandler`, call `a.newCAKeySource()`; the FIPS block stays as-is (it only uses `caSigner.PublicKey().Type()`, which works for HSM signers — and the rsa-sha2 restriction from `wrapCASigner` means RSA HSM keys report a FIPS-approvable algorithm; confirm `fipsmode.FromSSHAlgorithm` handles the `rsa-sha2-512` public key type string vs `ssh-rsa` — `PublicKey().Type()` still returns `ssh-rsa` for MultiAlgorithmSigner, so behavior is unchanged from PEM RSA keys). Update the stale coverage-exclusion comment on `pipeline.go:58` if line numbers shift.

In `BootstrapServe` and `BootstrapSigner` (`server/bootstrap/bootstrap.go`), after pipeline init, register cleanup following the existing pattern at line 137:

```go
	shutdowns.Add(a.closeKeySourceIfSet) // nil-safe wrapper matching servicerunner.Service's signature
```

Match `shutdownManager`/`servicerunner.Service`'s actual function signature (read `bootstrap.go:44-46` — likely `func(ctx context.Context) error`); wrap `ks.Close` accordingly.

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=1 rtk go test ./server/bootstrap/ ./server/config/ ./server/signer/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add server/bootstrap/ && rtk git commit -m "feat(server): wire hsm key source selection and shutdown into bootstrap"
```

---

### Task 6: Build pipeline — cgo single build, CI, Docker

**Files:**
- Modify: `.goreleaser.yml` (server-linux-build gets cgo + per-arch CC)
- Modify: `Dockerfile` (build stage CGO_ENABLED=1; runtime base → distroless/base)
- Modify: `Dockerfile.ubuntu-build` (add `softhsm2 opensc gcc-aarch64-linux-gnu`; it's currently untracked — coordinate with its owner/commit)
- Modify: `.github/workflows/codecover.yaml` (CGO_ENABLED=0 → 1 for the coverage run)
- Modify: `.github/workflows/lint.yaml` (ensure CGO_ENABLED=1 so golangci-lint can typecheck crypto11 imports, mirroring how lint-pam already does)
- Modify: `.github/workflows/build.yaml` (install cross gcc for the goreleaser arm64 server build)
- Modify: `Makefile` (targets that build/test server packages get CGO_ENABLED=1; add `test-hsm`)
- Create: `.github/workflows/hsm.yaml`

**Interfaces:**
- Consumes: `softhsm` build tag (Task 4).
- Produces: green CI; single dynamically-linked ssoosshd artifact.

- [ ] **Step 1: goreleaser — cgo via zig cc for the server build only**

In `.goreleaser.yml` `server-linux-build` (client builds keep global `CGO_ENABLED=0`), cross-compile with zig pinned to glibc 2.28 so release artifacts run on distros a few years old (RHEL 8 / Ubuntu 20.04 / Debian 11):

```yaml
  - id: server-linux-build
    binary: ssoosshd
    dir: ./cmd/ssoosshd
    flags: *flags
    env:
      - CGO_ENABLED=1
      - >-
        {{- if eq .Arch "amd64" }}CC=zig cc -target x86_64-linux-gnu.2.28{{- end }}
        {{- if eq .Arch "arm64" }}CC=zig cc -target aarch64-linux-gnu.2.28{{- end }}
    goos: [linux]
    goarch: [amd64, arm64]
```

Verify against goreleaser v2 docs: per-build `env` templating syntax (the commented-out macos example at `.goreleaser.yml:86-90` shows the intended pattern), and whether `CC` with spaces needs a wrapper script (`zcc-amd64.sh` invoking `exec zig cc -target ... "$@"`) — Go's `CC` handles spaces, but write the wrapper if goreleaser env templating mangles it. Check `GOFIPS140=v1.0.0` (global env, `.goreleaser.yml:33`) still builds with cgo — it's a toolchain crypto-module selector, unrelated to cgo, but confirm.

- [ ] **Step 1b: musl server artifact for Alpine**

Add a second server build (same zig toolchain, musl target, dynamically linked so dlopen/HSM works on Alpine):

```yaml
  - id: server-linux-musl-build
    binary: ssoosshd
    dir: ./cmd/ssoosshd
    flags: *flags
    env:
      - CGO_ENABLED=1
      - >-
        {{- if eq .Arch "amd64" }}CC=zig cc -target x86_64-linux-musl{{- end }}
        {{- if eq .Arch "arm64" }}CC=zig cc -target aarch64-linux-musl{{- end }}
    goos: [linux]
    goarch: [amd64, arm64]
```

Verify zig does NOT static-link here — a static musl binary cannot dlopen, which would silently kill HSM mode. `zig cc -target ...-musl` defaults to static for musl; force dynamic (`-dynamic` flag or check `file`/`ldd` on the output — must show a musl interpreter). Add:
- an archive entry `ssoossh-server_{{ .Version }}_{{ .Os }}_{{ .Arch }}_musl` for this build id,
- an nfpm entry (`package_name: ssoosshd`, `formats: [apk]`, `ids: [server-linux-musl-build]`) so Alpine gets a native package,
- a CI smoke step: `docker run --rm -v <artifact>:/ssoosshd alpine:latest /ssoosshd --version` must succeed.

The glibc-floor objdump assertion (Step 2b) applies to the gnu build only.

- [ ] **Step 2: Build workflow — install zig**

In `.github/workflows/build.yaml` (or wherever goreleaser runs — read it first), install zig before the goreleaser step (use a pinned zig release, e.g. via `mlugg/setup-zig` or a versioned tarball download — copy the pinning style of the workflow's other tool installs). If the release job runs in the `ghcr.io/mnestor/ubuntu-build` container, add zig to `Dockerfile.ubuntu-build` instead.

- [ ] **Step 2b: Verify the glibc floor**

After a snapshot build, assert no symbol newer than 2.28 leaked in:

```bash
objdump -T dist/server-linux-build_linux_amd64_v1/ssoosshd | grep -o 'GLIBC_[0-9.]*' | sort -Vu | tail -1
```
Expected: `GLIBC_2.28` or lower. Consider adding this as a release-workflow assertion step so a toolchain change can't silently raise the floor.

- [ ] **Step 3: Dockerfile**

```dockerfile
# server build stage: cgo needed for the PKCS#11 (HSM) CA key support
RUN CGO_ENABLED=1 go build -tags=nomsgpack -o /out/ssoosshd ./cmd/ssoosshd
...
# base-debian12 (not static-) because ssoosshd is now dynamically linked
# (cgo, dlopen for PKCS#11 modules). To use an HSM in-container, mount the
# PKCS#11 module and its deps into the image (see docs/hsm.md).
FROM gcr.io/distroless/base-debian12:nonroot
```

Update the comment block at `Dockerfile:32-39` which currently explains the static linking rationale. Plain gcc (no zig) is fine here: the builder and runtime are both debian12, so glibc versions match — the 2.28 floor only matters for goreleaser artifacts installed on arbitrary distros.

- [ ] **Step 4: Makefile + codecover + lint**

- Makefile: every target compiling server packages (`build`, `test`, `test-server`, `cover-ci`, …) either sets `CGO_ENABLED=1` or stops forcing 0; add:
  ```make
  # HSM integration tests; needs softhsm2 + opensc installed
  test-hsm:
  	CGO_ENABLED=1 go test -tags=softhsm ./server/signer/ -run TestHSMKeySource -v
  ```
- `codecover.yaml:80`: `CGO_ENABLED=0` → `CGO_ENABLED=1` (container image must have gcc — verify `ghcr.io/mnestor/ubuntu-build` has build-essential; it builds the PAM module already, so a C compiler is almost certainly present — confirm by reading Dockerfile.ubuntu-build).
- `lint.yaml`: export `CGO_ENABLED=1` for the golangci-lint step.
- Devcontainer/docs note: anywhere `CGO_ENABLED=0` is documented as the default (Makefile comment line ~77), update to reflect that server packages now need cgo.

- [ ] **Step 5: HSM CI workflow**

```yaml
# .github/workflows/hsm.yaml
name: hsm
on:
  pull_request:
    paths:
      - "server/signer/**"
      - "server/config/**"
      - "server/bootstrap/**"
      - "go.mod"
      - ".github/workflows/hsm.yaml"
  workflow_dispatch:
jobs:
  softhsm:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4   # match the pinning style used by existing workflows — copy the exact action refs from codecover.yaml
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: sudo apt-get update && sudo apt-get install -y softhsm2 opensc
      - run: CGO_ENABLED=1 go test -tags=softhsm ./server/signer/ -run TestHSMKeySource -v
```

Copy the exact action versions/pins from an existing workflow rather than the ones sketched above.

- [ ] **Step 6: Verify everything builds and runs**

```bash
CGO_ENABLED=1 rtk go build ./... && CGO_ENABLED=1 rtk go test ./...
rtk golangci-lint run
make test-hsm
```
Expected: all PASS. Also `rtk go vet ./client/...` with CGO_ENABLED=0 still passes (client stays cgo-free).

- [ ] **Step 7: Commit**

```bash
rtk git add .goreleaser.yml Dockerfile Dockerfile.ubuntu-build .github/workflows/ Makefile
rtk git commit -m "build: enable cgo for ssoosshd and add softhsm ci for hsm signing"
```

---

### Task 7: Documentation

**Files:**
- Create: `docs/hsm.md`
- Modify: `docs/ssoosshd.yaml.default` (commented `hsm:` block next to the `ssh_key` docs at line 116)
- Modify: `docs/man/ssoosshd.yaml.5` source (check whether man pages are generated or hand-written before editing)
- Modify: `server/CLAUDE.md` (the "CA key lives in an ssh-agent (v1)" bullet — PKCS#11 now exists behind the interface)

**Interfaces:** none; prose only.

- [ ] **Step 1: docs/hsm.md**

Cover, with exact commands:
1. What it does: CA private key held in a PKCS#11 token; ssoosshd signs via the token; key never in config or process memory. Supported: ECDSA P-256/384/521, RSA >= 2048 (signed rsa-sha2-512). Not supported: Ed25519 (Go PKCS#11 limitation) — keep `ssh_key` for Ed25519 CAs.
2. SoftHSM2 quickstart:
   ```bash
   sudo apt-get install softhsm2 opensc
   sudo softhsm2-util --init-token --free --label ssoossh-ca --pin <pin> --so-pin <so-pin>
   sudo pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so --login --pin <pin> \
     --keypairgen --key-type EC:prime256v1 --label ssoossh-ca --id 01
   ```
   Plus importing an existing PEM CA key (`softhsm2-util --import` needs PKCS#8: show the `openssl pkcs8 -topk8` conversion), and the file-permission note that the ssoosshd user needs read/write on the softhsm token dir.
3. Config example (mirroring Task 1 fields) and the "exactly one of ssh_key / hsm" rule.
4. Getting the CA public key for sshd `TrustedUserCAKeys`: the server's CA public key endpoint serves all registered signer keys (Phase 0 registry), HSM or PEM alike — document that this is the canonical source and that multiple lines may appear during key rotation or with multiple signers.
5. Real HSM note: any PKCS#11 module should work (YubiHSM, CloudHSM, TPM via libtpm2_pkcs11); SoftHSM2 is what CI tests.
6. Docker note: image is distroless/base; mount the module + token store, or run the signer split on the host near the HSM (`ssoosshd sign` mode).
7. Platform matrix note: glibc artifact (deb/rpm/tar.gz, glibc >= 2.28) vs musl artifact (apk/tar.gz, Alpine); client binaries remain static everywhere.

- [ ] **Step 2: ssoosshd.yaml.default block**

```yaml
# hsm - source the CA private key from a PKCS#11 token (HSM) instead of
# ssh_key. Exactly one of ssh_key / hsm may be set. See docs/hsm.md.
# hsm:
#   module: /usr/lib/softhsm/libsofthsm2.so
#   token_label: ssoossh-ca
#   pin_file: /etc/ssoossh/hsm-pin      # or pin: "1234"
#   key_label: ssoossh-ca
#   # key_id: "01"                      # hex CKA_ID, alternative to key_label
```

- [ ] **Step 3: Commit**

```bash
rtk git add docs/ server/CLAUDE.md && rtk git commit -m "docs: document pkcs11 hsm ca key configuration and softhsm2 setup"
```

---

## Self-Review Notes (open items surfaced while planning — resolve during implementation)

1. **CAService public key** — RESOLVED by Phase 0 (Tasks 0.1–0.4): the signer key registry replaces CAService's `ssh_key` parse. Phase 0 must merge before Task 5 wires HSM mode.
2. **crypto11 v2**: pkg.go.dev hinted a v2 may exist for eclipse-keypont/crypto11. Task 3 Step 1 resolves it; prefer whatever line is actively maintained.
3. **goreleaser per-build env templating**: syntax sketched in Task 6 Step 1 must be checked against goreleaser v2 docs.
4. **`fipsmode.FromSSHAlgorithm`** mapping for `ssh-rsa`/`rsa-sha2-*` and `ecdsa-sha2-*`: read `internal/fipsmode` during Task 5; the FIPS boot check must accept HSM ECDSA/RSA keys.
5. **Resilience/e2e workflows** (`.github/workflows/e2e.yaml`, `resilience.yaml`) may also set CGO_ENABLED — grep all workflows for `CGO_ENABLED=0` in Task 6 and flip the ones that compile server packages.

## Verification (end-to-end)

1. `CGO_ENABLED=1 go test ./...` and `golangci-lint run` clean.
2. `make test-hsm` green locally (softhsm2 + opensc installed).
3. Manual smoke: provision a SoftHSM2 token with an EC P-256 key (docs/hsm.md commands), write a minimal `ssoosshd.yaml` with the `hsm:` block, boot `ssoosshd` — it must start (proving module open + login + key lookup) and log the signer registered; then drive one certificate issuance through the normal flow (or the e2e harness) and `ssh-keygen -L -f` the issued cert to confirm the CA signature and `Signing CA` fingerprint match the token key.
4. Negative smoke: wrong PIN in config → ssoosshd must fail at boot with a clear error (fail-closed).
5. Registry smoke (split mode, NATS): start the API server first with no signer → CA key endpoint reports no keys; start `ssoosshd sign` → within seconds (request/announce round-trip) the endpoint serves the signer's key; kill the signer → key persists until TTL expiry, then drops from `ActiveKeys`. Also confirm full mode serves its key immediately on a fresh database (synchronous seed).
6. CI: hsm.yaml, codecover, lint, build-release snapshot all green on the PR.
