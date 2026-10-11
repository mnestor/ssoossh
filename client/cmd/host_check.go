package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/bep/simplecobra"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/principalsmap"
)

// defaultSSHDPath is where sshd lives on Linux and macOS, used when sshd is
// not on PATH -- which, for a shell without sbin in it, it often is not.
const defaultSSHDPath = "/usr/sbin/sshd"

// defaultMappingPath is the principal mapping file `host principals` reads
// when its command line names none. The host group's --file default is the
// same path.
const defaultMappingPath = "/etc/ssoossh/principals.yaml"

// compatSSOOSSHPath is where packages before the move to /usr/bin installed
// the client. Linux packages now leave a symlink there to /usr/bin/ssoossh,
// removed two releases after it was introduced (see .goreleaser.yml). On
// macOS it is still the real install path.
const compatSSOOSSHPath = "/usr/local/bin/ssoossh"

// errNoCAReference reports that there is nothing to compare the host's CA
// keys with: no capubkey pinned in config, and no server to ask.
var errNoCAReference = errors.New("no server configured and no capubkey pinned")

func newHostCheckCommand() simplecobra.Commander {
	var account, addr, sshdPath string

	cmd := &simpleCommand{
		name:  "check",
		short: "Check that this host's sshd will accept ssoossh certificates.",
		long: "Reports whether this host's sshd will accept ssoossh certificates. It reads " +
			"sshd's own effective configuration (sshd -T), so Include order, built-in " +
			"defaults and, with --user, Match blocks are resolved by sshd itself rather " +
			"than guessed at. It checks that public key authentication is on; that " +
			"TrustedUserCAKeys names a readable file holding the CA keys the server signs " +
			"with; that sshd allows the CA's signature algorithm and the client's " +
			"certificate types; that an AuthorizedPrincipalsCommand passes sshd's " +
			"path-safety rule and can read its mapping; and that the clock is " +
			"synchronized.\n\n" +
			"Read-only: it changes no file and reloads nothing. The CA keys to compare " +
			"with come from capubkey when one is pinned, otherwise from the server; when " +
			"neither is available that one comparison is a warning. Run it as root, since " +
			"sshd -T reads the host keys. Exits non-zero when any check fails, so " +
			"configuration management can gate on it.",
		// Most of this command is local, and a host whose server is down or
		// that has no ssh-agent must still get the local findings. So
		// PreRun builds nothing for it, and it asks the server for the CA
		// keys itself (referenceCAKeys), as one check among the others.
		serverOptional: true,
		init: func(cd *simplecobra.Commandeer) error {
			flags := cd.CobraCommand.Flags()
			flags.StringVar(&account, "user", "",
				"local account to check: applies sshd's Match blocks for it and lists the principals that may log in as it")
			flags.StringVar(&addr, "addr", "",
				"source address to apply sshd's Match blocks for")
			flags.StringVar(&sshdPath, "sshd", "",
				"path to sshd (default: sshd on PATH, then "+defaultSSHDPath+")")
			return nil
		},
	}
	cmd.run = func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
		checker := &hostChecker{
			sys:       osHostSystem{},
			sshd:      resolveSSHDPath(sshdPath),
			account:   account,
			addr:      addr,
			reference: root.referenceCAKeys,
		}
		return writeHostCheckReport(os.Stdout, checker.run(ctx))
	}
	return cmd
}

// resolveSSHDPath picks the sshd to ask: the one named with --sshd, else
// the one on PATH, else the usual location.
func resolveSSHDPath(flag string) string {
	if flag != "" {
		return flag
	}
	if path, err := exec.LookPath("sshd"); err == nil {
		return path
	}
	return defaultSSHDPath
}

// referenceCAKeys returns the CA keys this host should trust and where they
// came from: a pinned capubkey, the operator's own trust anchor, or else
// the server's live list. Never the CA cache, which is a cache and not a
// trust decision (see caCache).
//
// For a serverOptional command PreRun stops before it overwrites CAPubkey
// with fetched keys, so a non-empty CAPubkey here is one config set.
func (r *RootCommand) referenceCAKeys(ctx context.Context) ([]string, string, error) {
	cfg := r.Config()
	if len(cfg.CAPubkey) > 0 {
		return cfg.CAPubkey, "the pinned capubkey", nil
	}
	if cfg.Server == "" {
		return nil, "", errNoCAReference
	}

	client, err := r.newAPIClient(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("build API client: %w", err)
	}
	fetched, err := client.GetCA(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get CA public key from %s: %w", cfg.Server, err)
	}
	return config.SplitKeyList(fetched), cfg.Server, nil
}

// checkStatus grades one finding. Only checkFail makes the command exit
// non-zero: a warning is something that works today but deserves a look.
type checkStatus int

const (
	checkPass checkStatus = iota
	checkInfo
	checkSkip
	checkWarn
	checkFail
)

// String renders the status as the report's first column.
func (s checkStatus) String() string {
	switch s {
	case checkPass:
		return "PASS"
	case checkInfo:
		return "INFO"
	case checkSkip:
		return "SKIP"
	case checkWarn:
		return "WARN"
	default:
		return "FAIL"
	}
}

// checkResult is one line of the report: what was checked, what was found,
// and, where there is one, what to do about it.
type checkResult struct {
	status checkStatus
	name   string
	detail string
	hint   string
}

// writeHostCheckReport prints one line per result with its hint indented
// beneath, then a tally. It returns an error when any check failed, which
// is what makes the command exit non-zero.
func writeHostCheckReport(w io.Writer, results []checkResult) error {
	width := 0
	for _, r := range results {
		width = max(width, len(r.name))
	}
	indent := strings.Repeat(" ", len("PASS")+2+width+2)

	counts := map[checkStatus]int{}
	for _, r := range results {
		counts[r.status]++
		fmt.Fprintf(w, "%s  %-*s  %s\n", r.status, width, r.name, r.detail)
		if r.hint != "" {
			fmt.Fprintf(w, "%s%s\n", indent, r.hint)
		}
	}
	fmt.Fprintf(w, "\n%d passed, %d warnings, %d failed\n", counts[checkPass], counts[checkWarn], counts[checkFail])

	if counts[checkFail] > 0 {
		return fmt.Errorf("%d check(s) failed", counts[checkFail])
	}
	return nil
}

// hostSystem is everything host check reads from the machine. Production
// uses osHostSystem; tests substitute a fake, since the states worth
// checking -- a root-owned file, a group-writable directory above the
// principals command, an sshd that answers -T -- are ones a test cannot
// create for real.
type hostSystem interface {
	// SSHDConfig runs `sshd -T`, adding `-C spec` when spec is not empty.
	SSHDConfig(ctx context.Context, sshd, spec string) ([]byte, error)
	ReadFile(path string) ([]byte, error)
	// Stat follows symlinks, as sshd's own checks do.
	Stat(path string) (fileStat, error)
	Realpath(path string) (string, error)
	LookupUser(name string) (hostUser, error)
	LoadMapping(path string) (principalsmap.PrincipalsMap, error)
	// ClockSynchronized reports whether the clock is synchronized, and
	// whether that could be told at all.
	ClockSynchronized(ctx context.Context) (synced, known bool)
	// Privileged reports whether this process can read sshd's host keys.
	Privileged() bool
}

// fileStat is the part of a file's metadata sshd's checks look at.
// ownerKnown is false where the platform has no numeric owner (Windows),
// and every ownership check is then skipped rather than guessed.
type fileStat struct {
	mode       fs.FileMode
	uid, gid   uint32
	ownerKnown bool
}

// hostUser is a local account as file permissions see it.
type hostUser struct {
	name string
	uid  uint32
	gids []uint32
}

// osHostSystem is hostSystem on the real machine. The ownership, account
// and clock methods are per platform (host_check_unix.go and
// host_check_other.go).
type osHostSystem struct{}

// SSHDConfig implements hostSystem. sshd's stderr becomes the error, since
// that is where it says why it would not answer ("no hostkeys available").
func (osHostSystem) SSHDConfig(ctx context.Context, sshd, spec string) ([]byte, error) {
	args := []string{"-T"}
	if spec != "" {
		args = append(args, "-C", spec)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, sshd, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s -T: %s", sshd, msg)
		}
		return nil, fmt.Errorf("%s -T: %w", sshd, err)
	}
	return out, nil
}

// ReadFile implements hostSystem.
func (osHostSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Realpath implements hostSystem.
func (osHostSystem) Realpath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// LoadMapping implements hostSystem, with the loader `host principals`
// itself uses, so a file that loads here loads there.
func (osHostSystem) LoadMapping(path string) (principalsmap.PrincipalsMap, error) {
	return principalsmap.LoadFromFile(path)
}

// hostChecker runs every check against one host.
type hostChecker struct {
	sys     hostSystem
	sshd    string
	account string
	addr    string
	// reference returns the CA keys the host should trust, as
	// authorized_keys lines, and where they came from.
	reference func(ctx context.Context) ([]string, string, error)
}

// run checks the host and returns every finding in report order. Without
// sshd's effective configuration nothing else can be judged, so a failure
// to get it is the only finding.
func (c *hostChecker) run(ctx context.Context) []checkResult {
	out, err := c.sys.SSHDConfig(ctx, c.sshd, c.connectionSpec())
	if err != nil {
		r := checkResult{status: checkFail, name: "sshd", detail: fmt.Sprintf("could not read sshd's effective configuration: %v", err)}
		if !c.sys.Privileged() {
			r.hint = "run as root: sshd -T reads the host keys"
		}
		return []checkResult{r}
	}
	sshd := parseSSHDConfig(out)

	results := []checkResult{c.checkScope(), checkPubkeyAuthentication(sshd)}

	caPath, hostCAs, caResults := c.checkTrustedCAKeys(sshd)
	results = append(results, caResults...)

	signing := hostCAs
	reference, source, err := c.referenceKeys(ctx)
	if err != nil {
		results = append(results, caReferenceUnavailable(err))
	} else {
		results = append(results, compareCAKeys(caPath, hostCAs, reference, source)...)
		signing = reference
	}

	results = append(results,
		checkCASignatureAlgorithms(sshd, signing),
		checkCertificateAlgorithms(sshd),
		checkPlainKeys(sshd),
	)
	results = append(results, c.checkPrincipals(sshd)...)
	return append(results, c.checkClock(ctx))
}

// connectionSpec is the -C argument that makes sshd -T apply Match blocks
// for --user and --addr. sshd needs a host to go with an address, and the
// address stands in for it, as it does for a client with no reverse DNS.
func (c *hostChecker) connectionSpec() string {
	var parts []string
	if c.account != "" {
		parts = append(parts, "user="+c.account)
	}
	if c.addr != "" {
		parts = append(parts, "addr="+c.addr, "host="+c.addr)
	}
	return strings.Join(parts, ",")
}

// checkScope says which configuration was checked: sshd -T without -C
// prints the global settings and ignores every Match block.
func (c *hostChecker) checkScope() checkResult {
	if c.account == "" {
		return checkResult{
			status: checkInfo, name: "scope",
			detail: "checked sshd's global configuration; Match blocks were not applied",
			hint:   "pass --user (and --addr) to check an account the way sshd sees it",
		}
	}
	return checkResult{status: checkInfo, name: "scope", detail: "checked sshd's configuration for " + c.connectionSpec()}
}

// sshdConfig is sshd's effective configuration as `sshd -T` prints it: a
// lowercase keyword and its value per line.
type sshdConfig map[string]string

// parseSSHDConfig reads `sshd -T` output. A keyword that repeats (hostkey,
// listenaddress) keeps its first line; none of those are read here.
func parseSSHDConfig(out []byte) sshdConfig {
	cfg := sshdConfig{}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		if key == "" {
			continue
		}
		key = strings.ToLower(key)
		if _, seen := cfg[key]; !seen {
			cfg[key] = strings.TrimSpace(value)
		}
	}
	return cfg
}

// isSet reports whether an sshd -T value names something, as opposed to
// sshd's "none" or a keyword this sshd did not print.
func isSet(v string) bool { return v != "" && v != "none" }

// checkPubkeyAuthentication checks the switch certificates depend on: a
// certificate login is public key authentication, so with it off sshd
// refuses every certificate along with every plain key.
func checkPubkeyAuthentication(sshd sshdConfig) checkResult {
	if v := sshd["pubkeyauthentication"]; v != "yes" {
		return checkResult{
			status: checkFail, name: "pubkey-auth",
			detail: fmt.Sprintf("PubkeyAuthentication is %q: certificates are public key authentication, so sshd refuses every one", v),
			hint:   "set PubkeyAuthentication yes",
		}
	}
	return checkResult{status: checkPass, name: "pubkey-auth", detail: "PubkeyAuthentication yes"}
}

// checkTrustedCAKeys reads the CA keys the host trusts, returning the
// file's path for later checks' hints. Without them sshd refuses every
// certificate and, at the default LogLevel, logs only "Failed publickey"
// with no reason, which is what makes this the check most worth having.
func (c *hostChecker) checkTrustedCAKeys(sshd sshdConfig) (string, []ssh.PublicKey, []checkResult) {
	const name = "trusted-ca"
	path := sshd["trustedusercakeys"]

	switch {
	case !isSet(path):
		return "", nil, []checkResult{{
			status: checkFail, name: name,
			detail: "TrustedUserCAKeys is none: sshd trusts no CA and refuses every ssoossh certificate",
			hint:   "ssoossh ca > /etc/ssh/ca.pub, then uncomment TrustedUserCAKeys in /etc/ssh/sshd_config.d/50-ssoossh.conf and reload sshd",
		}}
	case strings.Contains(path, "%"):
		// sshd expands these per connection; there is no one file to read.
		return path, nil, []checkResult{{status: checkSkip, name: name, detail: fmt.Sprintf("TrustedUserCAKeys %s is per-connection; not read", path)}}
	}

	data, err := c.sys.ReadFile(path)
	if err != nil {
		return path, nil, []checkResult{{
			status: checkFail, name: name,
			detail: fmt.Sprintf("cannot read TrustedUserCAKeys %s: %v", path, err),
			hint:   "ssoossh ca > " + path,
		}}
	}

	keys, badLines := parseKeyLines(string(data))
	var results []checkResult
	if len(keys) == 0 {
		results = append(results, checkResult{
			status: checkFail, name: name,
			detail: fmt.Sprintf("TrustedUserCAKeys %s holds no public key, so sshd trusts no CA", path),
			hint:   "ssoossh ca >> " + path,
		})
	} else {
		results = append(results, checkResult{status: checkPass, name: name, detail: fmt.Sprintf("TrustedUserCAKeys %s holds %d key(s)", path, len(keys))})
	}
	if len(badLines) > 0 {
		results = append(results, checkResult{
			status: checkWarn, name: "trusted-ca-lines",
			detail: fmt.Sprintf("%s lines %v are not public keys; sshd skips them", path, badLines),
		})
	}
	if r, ok := c.checkCAFileAccess(path); ok {
		results = append(results, r)
	}
	return path, keys, results
}

// checkCAFileAccess warns when someone other than root can change the CA
// file: whoever can write it can add a CA this host then trusts. sshd
// itself does not insist on this, so it is a warning, not a failure.
func (c *hostChecker) checkCAFileAccess(path string) (checkResult, bool) {
	st, err := c.sys.Stat(path)
	if err != nil || !st.ownerKnown {
		return checkResult{}, false
	}
	if st.uid == 0 && st.mode.Perm()&0o022 == 0 {
		return checkResult{}, false
	}
	return checkResult{
		status: checkWarn, name: "trusted-ca-access",
		detail: fmt.Sprintf("%s can be changed by someone other than root (owner uid %d, mode %04o), and whoever writes it chooses which CA this host trusts",
			path, st.uid, uint32(st.mode.Perm())),
		hint: fmt.Sprintf("chown root %s && chmod go-w %s", path, path),
	}, true
}

// parseKeyLines parses authorized_keys-format lines, skipping blanks and
// comments, and returns the 1-based numbers of lines that are not keys.
func parseKeyLines(text string) ([]ssh.PublicKey, []int) {
	var keys []ssh.PublicKey
	var bad []int
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			bad = append(bad, i+1)
			continue
		}
		keys = append(keys, key)
	}
	return keys, bad
}

// referenceKeys fetches and parses the CA keys the host should trust. A
// key that does not parse, or an empty list, is an error rather than
// something to compare against: it would fail every host.
func (c *hostChecker) referenceKeys(ctx context.Context) ([]ssh.PublicKey, string, error) {
	lines, source, err := c.reference(ctx)
	if err != nil {
		return nil, "", err
	}
	keys, bad := parseKeyLines(strings.Join(lines, "\n"))
	if len(bad) > 0 {
		return nil, "", fmt.Errorf("%s gave a CA key that does not parse", source)
	}
	if len(keys) == 0 {
		return nil, "", fmt.Errorf("%s announced no CA key", source)
	}
	return keys, source, nil
}

// caReferenceUnavailable reports a comparison that could not be made. It
// is a warning, not a failure: the host may be fine, and the check that
// cannot be run is the only thing known to be wrong.
func caReferenceUnavailable(err error) checkResult {
	r := checkResult{
		status: checkWarn, name: "ca-match",
		detail: fmt.Sprintf("not compared with the CA that signs: %v", err),
	}
	if errors.Is(err, errNoCAReference) {
		r.hint = "pass --server, or set server in the system ssoossh.yaml"
	}
	return r
}

// compareCAKeys checks the host trusts the keys the CA signs with. Missing
// all of them is the silent failure this command exists for; missing some
// is a rotation in progress, which breaks at cutover; trusting extra keys
// is another CA or a rotation's leftover, and only worth a note.
func compareCAKeys(path string, host, reference []ssh.PublicKey, source string) []checkResult {
	const name = "ca-match"
	if len(host) == 0 {
		return []checkResult{{status: checkSkip, name: name, detail: "nothing to compare: the host trusts no CA key"}}
	}

	trusted := fingerprints(host)
	announced := fingerprints(reference)
	var missing, extra []string
	for _, k := range reference {
		if fp := ssh.FingerprintSHA256(k); !trusted[fp] {
			missing = append(missing, fp)
		}
	}
	for _, k := range host {
		if fp := ssh.FingerprintSHA256(k); !announced[fp] {
			extra = append(extra, fp)
		}
	}

	var results []checkResult
	switch {
	case len(missing) == len(reference):
		results = append(results, checkResult{
			status: checkFail, name: name,
			detail: fmt.Sprintf("%s trusts none of the CA keys %s signs with (%s)", path, source, strings.Join(missing, ", ")),
			hint:   "add the output of `ssoossh ca` to " + path,
		})
	case len(missing) > 0:
		results = append(results, checkResult{
			status: checkWarn, name: name,
			detail: fmt.Sprintf("%s is missing %d of the CA keys %s announces (%s); certificates signed with those are refused. Expected only mid-rotation",
				path, len(missing), source, strings.Join(missing, ", ")),
			hint: "add the output of `ssoossh ca` to " + path,
		})
	default:
		results = append(results, checkResult{status: checkPass, name: name, detail: fmt.Sprintf("%s trusts every CA key %s announces", path, source)})
	}
	if len(extra) > 0 {
		results = append(results, checkResult{
			status: checkInfo, name: "ca-extra",
			detail: fmt.Sprintf("%s also trusts %s, which %s does not announce: another CA, or a key left from a rotation", path, strings.Join(extra, ", "), source),
		})
	}
	return results
}

// fingerprints returns the SHA-256 fingerprints of keys as a set.
func fingerprints(keys []ssh.PublicKey) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[ssh.FingerprintSHA256(k)] = true
	}
	return set
}

// caSignatureAlgorithm returns the signature algorithm ssoosshd's signer
// uses with a CA key of this type, or "" for a type it refuses. It mirrors
// gateCASigner in server/signer/casigner.go: RSA is pinned to SHA-512,
// since SignCert takes the first algorithm offered and that is the first,
// and every other type signs with its own key algorithm.
func caSignatureAlgorithm(key ssh.PublicKey) string {
	switch t := key.Type(); t {
	case ssh.KeyAlgoRSA:
		return ssh.KeyAlgoRSASHA512
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return t
	default:
		return ""
	}
}

// checkCASignatureAlgorithms checks sshd allows the algorithm the CA signs
// with. On a default sshd it always does; it matters where a hardening
// guide or a FIPS crypto policy narrowed the list, for example dropping
// ssh-ed25519 under an Ed25519 CA.
func checkCASignatureAlgorithms(sshd sshdConfig, signing []ssh.PublicKey) checkResult {
	const name = "ca-signature"
	list, ok := sshd["casignaturealgorithms"]
	switch {
	case !ok:
		return checkResult{status: checkSkip, name: name, detail: "sshd -T printed no CASignatureAlgorithms"}
	case len(signing) == 0:
		return checkResult{status: checkSkip, name: name, detail: "no CA key to check"}
	}

	allowed := strings.Split(list, ",")
	var used, refused []string
	for _, k := range signing {
		alg := caSignatureAlgorithm(k)
		if alg == "" || slices.Contains(used, alg) {
			continue
		}
		used = append(used, alg)
		if !slices.Contains(allowed, alg) {
			refused = append(refused, alg)
		}
	}
	if len(refused) > 0 {
		return checkResult{
			status: checkFail, name: name,
			detail: fmt.Sprintf("CASignatureAlgorithms does not allow %s, so sshd refuses certificates the CA signs", strings.Join(refused, ", ")),
			hint:   "add it to CASignatureAlgorithms, or move the CA to a key type this host allows",
		}
	}
	return checkResult{status: checkPass, name: name, detail: "CASignatureAlgorithms allows " + strings.Join(used, ", ")}
}

// certFamily is one key type the client can be configured to generate,
// with the certificate algorithms sshd may see its certificate offered as.
type certFamily struct {
	name       string
	algorithms []string
}

// clientCertificateFamilies lists every sshkey.type and size the client
// generates. An RSA certificate is offered as either SHA-2 form, so either
// being accepted is enough.
func clientCertificateFamilies() []certFamily {
	return []certFamily{
		{name: "ed25519", algorithms: []string{ssh.CertAlgoED25519v01}},
		{name: "ecdsa P-256", algorithms: []string{ssh.CertAlgoECDSA256v01}},
		{name: "ecdsa P-384", algorithms: []string{ssh.CertAlgoECDSA384v01}},
		{name: "ecdsa P-521", algorithms: []string{ssh.CertAlgoECDSA521v01}},
		{name: "rsa", algorithms: []string{ssh.CertAlgoRSASHA512v01, ssh.CertAlgoRSASHA256v01}},
	}
}

// checkCertificateAlgorithms checks sshd accepts certificates on the key
// types clients generate. A host that refuses some is only a problem for
// users whose client is configured with one of those, so that is a warning;
// refusing all of them is a failure.
func checkCertificateAlgorithms(sshd sshdConfig) checkResult {
	const name = "cert-types"
	list, ok := sshd["pubkeyacceptedalgorithms"]
	if !ok {
		// Older OpenSSH printed the same setting under its former name.
		list, ok = sshd["pubkeyacceptedkeytypes"]
	}
	if !ok {
		return checkResult{status: checkSkip, name: name, detail: "sshd -T printed no PubkeyAcceptedAlgorithms"}
	}

	accepted := strings.Split(list, ",")
	families := clientCertificateFamilies()
	var refused []string
	for _, f := range families {
		if !slices.ContainsFunc(f.algorithms, func(a string) bool { return slices.Contains(accepted, a) }) {
			refused = append(refused, f.name)
		}
	}
	switch {
	case len(refused) == len(families):
		return checkResult{
			status: checkFail, name: name,
			detail: "PubkeyAcceptedAlgorithms allows no certificate type the client generates",
			hint:   "add the *-cert-v01@openssh.com algorithms for your clients' key types",
		}
	case len(refused) > 0:
		return checkResult{
			status: checkWarn, name: name,
			detail: fmt.Sprintf("certificates on %s keys are refused; a client whose sshkey.type is one of those cannot log in here", strings.Join(refused, ", ")),
		}
	}
	return checkResult{status: checkPass, name: name, detail: "certificates on every key type the client generates are accepted"}
}

// checkPlainKeys asks whether plain public keys are meant to keep working
// beside certificates. Running both is how the docs suggest adopting
// ssoossh, so this is a question, never a warning.
func checkPlainKeys(sshd sshdConfig) checkResult {
	const name = "plain-keys"
	var active []string
	if v := sshd["authorizedkeysfile"]; isSet(v) {
		active = append(active, "AuthorizedKeysFile "+v)
	}
	if v := sshd["authorizedkeyscommand"]; isSet(v) {
		active = append(active, "AuthorizedKeysCommand "+v)
	}
	if len(active) == 0 {
		return checkResult{status: checkPass, name: name, detail: "plain public keys are not accepted: certificates only"}
	}
	return checkResult{
		status: checkInfo, name: name,
		detail: fmt.Sprintf("plain public keys are still accepted beside certificates (%s). Intended?", strings.Join(active, "; ")),
		hint:   "set AuthorizedKeysFile none once everyone has moved to certificates",
	}
}

// checkPrincipals checks how sshd decides which principals may assume an
// account. Only `ssoossh host principals` is checked in depth; another
// command or an AuthorizedPrincipalsFile is noted and left alone.
func (c *hostChecker) checkPrincipals(sshd sshdConfig) []checkResult {
	command, file := sshd["authorizedprincipalscommand"], sshd["authorizedprincipalsfile"]
	switch {
	case isSet(command):
		return c.checkPrincipalsCommand(sshd, command)
	case isSet(file):
		return []checkResult{{
			status: checkInfo, name: "principals",
			detail: fmt.Sprintf("principals come from AuthorizedPrincipalsFile %s, which this check does not read", file),
		}}
	}
	results := []checkResult{{
		status: checkInfo, name: "principals",
		detail: "no principals mapping: a certificate logs in only as an account its principals name",
	}}
	return append(results, c.principalsFor(nil)...)
}

// checkPrincipalsCommand checks the AuthorizedPrincipalsCommand sshd will
// run: that sshd will agree to run it, as which account, and, for `ssoossh
// host principals`, that it is handed an account and can read its mapping.
func (c *hostChecker) checkPrincipalsCommand(sshd sshdConfig, command string) []checkResult {
	argv := strings.Fields(command)
	results := []checkResult{c.checkCommandPath(argv[0])}
	runAs, userResult := c.checkCommandUser(sshd["authorizedprincipalscommanduser"])
	results = append(results, userResult)

	inv := parsePrincipalsCommand(argv)
	switch {
	case !inv.ours:
		return append(results, checkResult{
			status: checkInfo, name: "principals",
			detail: fmt.Sprintf("principals come from %s, which this check does not evaluate", argv[0]),
		})
	case !inv.passesAccount:
		return append(results, checkResult{
			status: checkFail, name: "principals",
			detail: "AuthorizedPrincipalsCommand passes no account to look up, so it answers nothing and sshd refuses every certificate",
			hint:   "end the command with %u: " + argv[0] + " host principals %u",
		})
	}
	return append(results, c.checkMapping(inv.mappingPath, runAs)...)
}

// sshdPathAbsolute is the test the sshd built for goos applies to a
// command's path: path_absolute() in OpenSSH's misc.c. Upstream wants a
// leading '/'. Win32-OpenSSH (is_absolute_path) skips one leading quote and
// also takes '\', a drive letter or its __PROGRAMDATA__ token, which Go's
// filepath.IsAbs would reject for a path like /usr/bin/ssoossh.
func sshdPathAbsolute(goos, path string) bool {
	if goos != "windows" {
		return strings.HasPrefix(path, "/")
	}
	if path != "" && (path[0] == '"' || path[0] == '\'') {
		path = path[1:]
	}
	switch {
	case strings.HasPrefix(path, "/"), strings.HasPrefix(path, `\`), strings.HasPrefix(path, "__PROGRAMDATA__"):
		return true
	case len(path) >= 2 && path[1] == ':':
		c := path[0] | 0x20
		return c >= 'a' && c <= 'z'
	}
	return false
}

// checkCommandPath applies sshd's rule for running a command: an absolute
// path whose resolved file, and every directory above it, is owned by root
// and writable by neither group nor other (safe_path in OpenSSH's misc.c).
// Anything else, and sshd refuses to run it with the reason only in its log.
func (c *hostChecker) checkCommandPath(path string) checkResult {
	const name = "principals-command"
	if !sshdPathAbsolute(runtime.GOOS, path) {
		return checkResult{status: checkFail, name: name, detail: fmt.Sprintf("AuthorizedPrincipalsCommand %s is not an absolute path, and sshd refuses to run it", path)}
	}
	real, err := c.sys.Realpath(path)
	if err != nil {
		return checkResult{status: checkFail, name: name, detail: fmt.Sprintf("cannot resolve AuthorizedPrincipalsCommand %s: %v", path, err)}
	}

	problem, checked := c.unsafePath(real)
	switch {
	case problem != "":
		return checkResult{
			status: checkFail, name: name,
			detail: fmt.Sprintf("sshd refuses to run %s: %s", path, problem),
			hint:   "the file and every directory above it must be owned by root and writable by neither group nor other; the package installs /usr/bin/ssoossh that way",
		}
	case !checked:
		return checkResult{status: checkSkip, name: name, detail: fmt.Sprintf("ownership of %s is not checked on this platform", real)}
	case path == compatSSOOSSHPath && real != path:
		// sshd judges the link's target, so this works today; it is the
		// link's scheduled removal that will break it.
		return checkResult{
			status: checkWarn, name: name,
			detail: fmt.Sprintf("AuthorizedPrincipalsCommand names the compatibility link %s; sshd follows it to %s and runs it today, "+
				"but the link is removed in a later release and every certificate login then fails", path, real),
			hint: "point AuthorizedPrincipalsCommand at " + real,
		}
	}
	return checkResult{status: checkPass, name: name, detail: fmt.Sprintf("sshd's path-safety rule allows %s", real)}
}

// unsafePath returns why sshd would refuse to run the resolved path, or ""
// when it would not; checked is false where the platform has no owners to
// check. This is upstream's rule, and it can only over-report against two
// relaxations: sshd accepts a few platform system uids for a directory, and
// Debian's and Ubuntu's sshd (user-group-modes.patch) accept a
// group-writable file or directory whose group has the owner as its only
// member, such as root:root 0775. A root:staff 2775 directory with no staff
// members is refused by both.
func (c *hostChecker) unsafePath(real string) (problem string, checked bool) {
	st, err := c.sys.Stat(real)
	if err != nil {
		return fmt.Sprintf("cannot stat %s: %v", real, err), true
	}
	if !st.ownerKnown {
		return "", false
	}
	if !st.mode.IsRegular() {
		return real + " is not a regular file", true
	}
	if st.uid != 0 || st.mode.Perm()&0o022 != 0 {
		return fmt.Sprintf("bad ownership or modes for file %s (owner uid %d, mode %04o)", real, st.uid, uint32(st.mode.Perm())), true
	}

	// path.Dir, not filepath.Dir: the walk runs only where owners are known,
	// and there paths are slash-separated as sshd's own dirname() sees them.
	// filepath.Dir would turn the fake hosts' /usr/bin into \usr\bin when the
	// tests run on Windows.
	for dir := path.Dir(real); ; dir = path.Dir(dir) {
		st, err := c.sys.Stat(dir)
		if err != nil {
			return fmt.Sprintf("cannot stat directory %s: %v", dir, err), true
		}
		if st.uid != 0 || st.mode.Perm()&0o022 != 0 {
			return fmt.Sprintf("bad ownership or modes for directory %s (owner uid %d, mode %04o)", dir, st.uid, uint32(st.mode.Perm())), true
		}
		if path.Dir(dir) == dir {
			return "", true
		}
	}
}

// checkCommandUser checks the account sshd runs the command as exists, and
// returns it for the mapping's read check.
func (c *hostChecker) checkCommandUser(name string) (*hostUser, checkResult) {
	const check = "principals-user"
	if !isSet(name) {
		return nil, checkResult{
			status: checkFail, name: check,
			detail: "AuthorizedPrincipalsCommand is set without AuthorizedPrincipalsCommandUser, so sshd will not run it",
			hint:   "set AuthorizedPrincipalsCommandUser ssoossh-principals",
		}
	}
	u, err := c.sys.LookupUser(name)
	if err != nil {
		return nil, checkResult{status: checkFail, name: check, detail: fmt.Sprintf("AuthorizedPrincipalsCommandUser %s does not resolve: %v", name, err)}
	}
	u.name = name
	return &u, checkResult{status: checkPass, name: check, detail: "the command runs as " + name}
}

// principalsInvocation is what an AuthorizedPrincipalsCommand line says
// about `ssoossh host principals`: whether it is that command at all,
// whether sshd hands it an account, and which mapping file it reads.
type principalsInvocation struct {
	ours          bool
	passesAccount bool
	mappingPath   string
}

// parsePrincipalsCommand reads the command line sshd will run. Flags that
// take a value are skipped along with it, so a line carrying -v, -c or
// --file is recognized as well as the plain form.
func parsePrincipalsCommand(argv []string) principalsInvocation {
	inv := principalsInvocation{mappingPath: defaultMappingPath}
	if strings.TrimSuffix(filepath.Base(argv[0]), ".exe") != "ssoossh" {
		return inv
	}

	var words []string
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--file" && i+1 < len(argv):
			i++
			inv.mappingPath = argv[i]
		case strings.HasPrefix(arg, "--file="):
			inv.mappingPath = strings.TrimPrefix(arg, "--file=")
		case arg == "-c" || arg == "--config" || arg == "--server":
			i++
		case strings.HasPrefix(arg, "-"):
			// A flag with no value, or one carrying it after "=".
		default:
			words = append(words, arg)
		}
	}
	inv.ours = len(words) >= 2 && words[0] == "host" && words[1] == "principals"
	inv.passesAccount = len(words) >= 3
	return inv
}

// checkMapping checks the mapping file the command reads loads, and that
// the command's account can read it. Both fail silently in production:
// `host principals` still answers, with the account name alone, so every
// mapped principal is dropped without an error anywhere sshd shows.
func (c *hostChecker) checkMapping(path string, runAs *hostUser) []checkResult {
	const name = "principals-mapping"
	mapping, err := c.sys.LoadMapping(path)
	if errors.Is(err, fs.ErrNotExist) {
		results := []checkResult{{
			status: checkInfo, name: name,
			detail: fmt.Sprintf("no mapping file at %s: every account admits only certificates naming it", path),
		}}
		return append(results, c.principalsFor(nil)...)
	}
	if err != nil {
		return []checkResult{{
			status: checkFail, name: name,
			detail: fmt.Sprintf("%s will not load (%v); host principals answers with the account name alone, so every mapped principal is dropped", path, err),
		}}
	}

	results := []checkResult{
		{status: checkPass, name: name, detail: fmt.Sprintf("%s loads: %d account(s) mapped", path, len(mapping))},
		c.checkMappingReadable(path, runAs),
	}
	return append(results, c.principalsFor(mapping)...)
}

// checkMappingReadable checks the command's account can read the mapping.
// Loading it above ran as root, which proves nothing about the account sshd
// runs the command as.
func (c *hostChecker) checkMappingReadable(path string, runAs *hostUser) checkResult {
	const name = "mapping-access"
	if runAs == nil {
		return checkResult{status: checkSkip, name: name, detail: "not checked: the command's account did not resolve"}
	}
	st, err := c.sys.Stat(path)
	switch {
	case err != nil:
		return checkResult{status: checkFail, name: name, detail: fmt.Sprintf("cannot stat %s: %v", path, err)}
	case !st.ownerKnown:
		return checkResult{status: checkSkip, name: name, detail: "file permissions are not checked on this platform"}
	case !readableBy(st, *runAs):
		return checkResult{
			status: checkFail, name: name,
			detail: fmt.Sprintf("%s cannot read %s (owner %d:%d, mode %04o), so host principals answers with the account name alone",
				runAs.name, path, st.uid, st.gid, uint32(st.mode.Perm())),
			hint: fmt.Sprintf("make %s readable by %s; the package ships it root:ssoossh-principals, mode 0640", path, runAs.name),
		}
	}
	return checkResult{status: checkPass, name: name, detail: fmt.Sprintf("%s can read %s", runAs.name, path)}
}

// readableBy applies Unix permission classes: the owner's bits alone when
// the account owns the file, else the group's for a member, else other's.
// ACLs are not consulted.
func readableBy(st fileStat, u hostUser) bool {
	switch {
	case u.uid == 0:
		return true
	case st.uid == u.uid:
		return st.mode&0o400 != 0
	case slices.Contains(u.gids, st.gid):
		return st.mode&0o040 != 0
	default:
		return st.mode&0o004 != 0
	}
}

// principalsFor lists, for --user, the certificate principals that may log
// in as that account: whatever the mapping lists, plus the account itself,
// the same floor `host principals` guarantees.
func (c *hostChecker) principalsFor(mapping principalsmap.PrincipalsMap) []checkResult {
	if c.account == "" {
		return nil
	}
	principals := slices.Clone(mapping[c.account])
	if !slices.Contains(principals, c.account) {
		principals = append(principals, c.account)
	}
	return []checkResult{{
		status: checkInfo, name: "principals",
		detail: fmt.Sprintf("a certificate naming any of %s may log in as %s", strings.Join(principals, ", "), c.account),
	}}
}

// checkClock warns about an unsynchronized clock. Certificates last
// minutes, so a drift sshd would shrug off for anything else refuses them
// as expired or not yet valid.
func (c *hostChecker) checkClock(ctx context.Context) checkResult {
	const name = "clock"
	synced, known := c.sys.ClockSynchronized(ctx)
	switch {
	case !known:
		return checkResult{status: checkSkip, name: name, detail: "could not tell whether the system clock is synchronized"}
	case !synced:
		return checkResult{
			status: checkWarn, name: name,
			detail: "the system clock is not synchronized; certificates last minutes, so a small drift refuses them",
			hint:   "enable NTP, e.g. timedatectl set-ntp true",
		}
	}
	return checkResult{status: checkPass, name: name, detail: "the system clock is synchronized"}
}
