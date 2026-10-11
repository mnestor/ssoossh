package cmd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/principalsmap"
)

// fakeHostSystem stands in for the machine. The states host check exists to
// catch -- a root-owned file, a group-writable directory above the
// principals command, an sshd that answers -T -- are ones a test cannot
// create for real, so every one of them is a map entry here.
type fakeHostSystem struct {
	sshdOut  string
	sshdErr  error
	sshdSpec string

	files    map[string]string
	stats    map[string]fileStat
	links    map[string]string
	linkErrs map[string]error
	users    map[string]hostUser
	mappings map[string]principalsmap.PrincipalsMap
	badMaps  map[string]error

	clockSynced, clockKnown bool
	privileged              bool
}

func (f *fakeHostSystem) SSHDConfig(_ context.Context, _, spec string) ([]byte, error) {
	f.sshdSpec = spec
	return []byte(f.sshdOut), f.sshdErr
}

func (f *fakeHostSystem) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(data), nil
}

func (f *fakeHostSystem) Stat(path string) (fileStat, error) {
	st, ok := f.stats[path]
	if !ok {
		return fileStat{}, fs.ErrNotExist
	}
	return st, nil
}

func (f *fakeHostSystem) Realpath(path string) (string, error) {
	if err, ok := f.linkErrs[path]; ok {
		return "", err
	}
	if target, ok := f.links[path]; ok {
		return target, nil
	}
	return path, nil
}

func (f *fakeHostSystem) LookupUser(name string) (hostUser, error) {
	u, ok := f.users[name]
	if !ok {
		return hostUser{}, fmt.Errorf("user: unknown user %s", name)
	}
	return u, nil
}

func (f *fakeHostSystem) LoadMapping(path string) (principalsmap.PrincipalsMap, error) {
	if err, ok := f.badMaps[path]; ok {
		return nil, err
	}
	m, ok := f.mappings[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return m, nil
}

func (f *fakeHostSystem) ClockSynchronized(context.Context) (bool, bool) {
	return f.clockSynced, f.clockKnown
}

func (f *fakeHostSystem) Privileged() bool { return f.privileged }

// rootDir, rootFile and rootExec are the ownership and modes a
// package-installed path has: everything owned by root, nothing writable
// by group or other.
func rootDir() fileStat  { return fileStat{mode: fs.ModeDir | 0o755, ownerKnown: true} }
func rootFile() fileStat { return fileStat{mode: 0o644, ownerKnown: true} }
func rootExec() fileStat { return fileStat{mode: 0o755, ownerKnown: true} }

// safeCommandStats is the stat table for /usr/bin/ssoossh installed by the
// package, which sshd's path-safety rule accepts.
func safeCommandStats() map[string]fileStat {
	return map[string]fileStat{
		"/usr/bin/ssoossh": rootExec(),
		"/usr/bin":         rootDir(),
		"/usr":             rootDir(),
		"/":                rootDir(),
	}
}

// newTestCAKey returns a fresh CA public key of the given type, both parsed
// and as the authorized_keys line a CA file or the server would carry.
func newTestCAKey(t *testing.T, kind string) (ssh.PublicKey, string) {
	t.Helper()

	var raw any
	switch kind {
	case "ed25519":
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate ed25519 key: %v", err)
		}
		raw = pub
	case "ecdsa":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate ecdsa key: %v", err)
		}
		raw = &priv.PublicKey
	case "rsa":
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate rsa key: %v", err)
		}
		raw = &priv.PublicKey
	default:
		t.Fatalf("unknown key kind %q", kind)
	}

	key, err := ssh.NewPublicKey(raw)
	if err != nil {
		t.Fatalf("wrap public key: %v", err)
	}
	return key, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

// statusOf returns the status of the first result with the given name, and
// fails the test when there is none.
func statusOf(t *testing.T, results []checkResult, name string) checkStatus {
	t.Helper()
	for _, r := range results {
		if r.name == name {
			return r.status
		}
	}
	t.Fatalf("no %q result in %+v", name, results)
	return checkFail
}

// defaultSSHDOutput is the slice of `sshd -T` output host check reads, as an
// untouched OpenSSH 10.0 prints it, with TrustedUserCAKeys pointed at
// /etc/ssh/ca.pub.
const defaultSSHDOutput = `pubkeyauthentication yes
trustedusercakeys /etc/ssh/ca.pub
authorizedprincipalsfile none
authorizedkeyscommand none
authorizedprincipalscommand none
authorizedprincipalscommanduser none
casignaturealgorithms ssh-ed25519,ecdsa-sha2-nistp256,ecdsa-sha2-nistp384,ecdsa-sha2-nistp521,sk-ssh-ed25519@openssh.com,sk-ecdsa-sha2-nistp256@openssh.com,rsa-sha2-512,rsa-sha2-256
pubkeyacceptedalgorithms ssh-ed25519-cert-v01@openssh.com,ecdsa-sha2-nistp256-cert-v01@openssh.com,ecdsa-sha2-nistp384-cert-v01@openssh.com,ecdsa-sha2-nistp521-cert-v01@openssh.com,rsa-sha2-512-cert-v01@openssh.com,rsa-sha2-256-cert-v01@openssh.com,ssh-ed25519,ecdsa-sha2-nistp256
authorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2
`

func TestParseSSHDConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, out, key, want string
	}{
		{name: "should read a keyword's value", out: "pubkeyauthentication yes\n", key: "pubkeyauthentication", want: "yes"},
		{name: "should keep a value that contains spaces", out: "authorizedkeysfile .ssh/a .ssh/b\n", key: "authorizedkeysfile", want: ".ssh/a .ssh/b"},
		{name: "should lowercase the keyword", out: "TrustedUserCAKeys /etc/ssh/ca.pub\n", key: "trustedusercakeys", want: "/etc/ssh/ca.pub"},
		{name: "should keep the first line when a keyword repeats", out: "hostkey /a\nhostkey /b\n", key: "hostkey", want: "/a"},
		{name: "should skip blank lines", out: "\n\npubkeyauthentication no\n", key: "pubkeyauthentication", want: "no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseSSHDConfig([]byte(tt.out))[tt.key]; got != tt.want {
				t.Errorf("parseSSHDConfig()[%q] = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestCheckPubkeyAuthentication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  checkStatus
	}{
		{name: "should pass when public key authentication is on", value: "yes", want: checkPass},
		// A certificate login is public key authentication, so turning it
		// off refuses every certificate too.
		{name: "should fail when public key authentication is off", value: "no", want: checkFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkPubkeyAuthentication(sshdConfig{"pubkeyauthentication": tt.value})
			if got.status != tt.want {
				t.Errorf("status = %v, want %v (%s)", got.status, tt.want, got.detail)
			}
		})
	}
}

func TestCheckTrustedCAKeys(t *testing.T) {
	t.Parallel()

	_, line := newTestCAKey(t, "ed25519")

	tests := []struct {
		name  string
		value string
		files map[string]string
		stats map[string]fileStat
		check string
		want  checkStatus
	}{
		{name: "should fail when TrustedUserCAKeys is none", value: "none", check: "trusted-ca", want: checkFail},
		{name: "should fail when TrustedUserCAKeys was not printed", value: "", check: "trusted-ca", want: checkFail},
		{name: "should fail when the CA file cannot be read", value: "/etc/ssh/ca.pub", check: "trusted-ca", want: checkFail},
		{
			name: "should fail when the CA file holds no key", value: "/etc/ssh/ca.pub",
			files: map[string]string{"/etc/ssh/ca.pub": "# nothing yet\n"}, check: "trusted-ca", want: checkFail,
		},
		{
			name: "should pass when the CA file holds a key", value: "/etc/ssh/ca.pub",
			files: map[string]string{"/etc/ssh/ca.pub": line + "\n"}, check: "trusted-ca", want: checkPass,
		},
		{
			name: "should warn when a line of the CA file is not a key", value: "/etc/ssh/ca.pub",
			files: map[string]string{"/etc/ssh/ca.pub": line + "\nnot a key\n"}, check: "trusted-ca-lines", want: checkWarn,
		},
		{
			name: "should warn when the CA file is writable by someone other than root", value: "/etc/ssh/ca.pub",
			files: map[string]string{"/etc/ssh/ca.pub": line + "\n"},
			stats: map[string]fileStat{"/etc/ssh/ca.pub": {mode: 0o664, ownerKnown: true}},
			check: "trusted-ca-access", want: checkWarn,
		},
		{
			name: "should warn when the CA file is owned by someone other than root", value: "/etc/ssh/ca.pub",
			files: map[string]string{"/etc/ssh/ca.pub": line + "\n"},
			stats: map[string]fileStat{"/etc/ssh/ca.pub": {mode: 0o644, uid: 1000, ownerKnown: true}},
			check: "trusted-ca-access", want: checkWarn,
		},
		{name: "should skip a per-user CA path", value: "/home/%u/ca.pub", check: "trusted-ca", want: checkSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &hostChecker{sys: &fakeHostSystem{files: tt.files, stats: tt.stats}}
			_, _, results := c.checkTrustedCAKeys(sshdConfig{"trustedusercakeys": tt.value})
			if got := statusOf(t, results, tt.check); got != tt.want {
				t.Errorf("%s status = %v, want %v (%+v)", tt.check, got, tt.want, results)
			}
		})
	}
}

func TestCompareCAKeys(t *testing.T) {
	t.Parallel()

	current, _ := newTestCAKey(t, "ed25519")
	next, _ := newTestCAKey(t, "ecdsa")
	old, _ := newTestCAKey(t, "ed25519")

	tests := []struct {
		name      string
		host, ref []ssh.PublicKey
		check     string
		want      checkStatus
	}{
		{name: "should skip when the host trusts no key", ref: []ssh.PublicKey{current}, check: "ca-match", want: checkSkip},
		{name: "should fail when the host trusts none of the signing keys", host: []ssh.PublicKey{old}, ref: []ssh.PublicKey{current}, check: "ca-match", want: checkFail},
		{name: "should warn when the host is missing a key mid-rotation", host: []ssh.PublicKey{current}, ref: []ssh.PublicKey{current, next}, check: "ca-match", want: checkWarn},
		{name: "should pass when the host trusts every signing key", host: []ssh.PublicKey{current}, ref: []ssh.PublicKey{current}, check: "ca-match", want: checkPass},
		{name: "should note a trusted key the server does not announce", host: []ssh.PublicKey{current, old}, ref: []ssh.PublicKey{current}, check: "ca-extra", want: checkInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := compareCAKeys("/etc/ssh/ca.pub", tt.host, tt.ref, "the server")
			if got := statusOf(t, results, tt.check); got != tt.want {
				t.Errorf("%s status = %v, want %v (%+v)", tt.check, got, tt.want, results)
			}
		})
	}
}

func TestHostCheckerReferenceKeys(t *testing.T) {
	t.Parallel()

	_, line := newTestCAKey(t, "ed25519")

	tests := []struct {
		name    string
		lines   []string
		err     error
		wantErr bool
	}{
		{name: "should return the parsed keys when the reference has one", lines: []string{line}},
		{name: "should fail when there is no reference", err: errNoCAReference, wantErr: true},
		{name: "should fail when the reference announces no key", lines: nil, wantErr: true},
		{name: "should fail when a reference key does not parse", lines: []string{"not a key"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &hostChecker{reference: func(context.Context) ([]string, string, error) {
				return tt.lines, "the server", tt.err
			}}
			_, _, err := c.referenceKeys(t.Context())
			if (err != nil) != tt.wantErr {
				t.Errorf("referenceKeys() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCAReferenceUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantHint bool
	}{
		{name: "should say how to name a server when none is configured", err: errNoCAReference, wantHint: true},
		{name: "should report the error when the server could not be asked", err: errors.New("connection refused")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := caReferenceUnavailable(tt.err)
			if got.status != checkWarn || (got.hint != "") != tt.wantHint {
				t.Errorf("caReferenceUnavailable() = %+v, want a warning with hint %v", got, tt.wantHint)
			}
		})
	}
}

func TestCASignatureAlgorithm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind string
		want string
	}{
		{kind: "ed25519", want: ssh.KeyAlgoED25519},
		{kind: "ecdsa", want: ssh.KeyAlgoECDSA256},
		// The signer pins RSA to SHA-512, never the SHA-1 ssh-rsa.
		{kind: "rsa", want: ssh.KeyAlgoRSASHA512},
	}
	for _, tt := range tests {
		t.Run("should name the algorithm the signer uses for an "+tt.kind+" CA", func(t *testing.T) {
			t.Parallel()
			key, _ := newTestCAKey(t, tt.kind)
			if got := caSignatureAlgorithm(key); got != tt.want {
				t.Errorf("caSignatureAlgorithm() = %q, want %q", got, tt.want)
			}
		})
	}
}

// unsignableKey is a public key of a type ssoosshd's signer refuses, which
// therefore never appears as a CA signature algorithm.
type unsignableKey struct{ ssh.PublicKey }

func (unsignableKey) Type() string { return ssh.KeyAlgoDSA } //nolint:staticcheck // DSA is the point: a type the signer refuses

func TestCASignatureAlgorithmShouldNameNothingForATypeTheSignerRefuses(t *testing.T) {
	t.Parallel()
	if got := caSignatureAlgorithm(unsignableKey{}); got != "" {
		t.Errorf("caSignatureAlgorithm() = %q, want \"\"", got)
	}
}

func TestCheckCASignatureAlgorithms(t *testing.T) {
	t.Parallel()

	ed, _ := newTestCAKey(t, "ed25519")
	rsaKey, _ := newTestCAKey(t, "rsa")

	tests := []struct {
		name    string
		sshd    sshdConfig
		signing []ssh.PublicKey
		want    checkStatus
	}{
		{name: "should skip when sshd does not report the setting", sshd: sshdConfig{}, signing: []ssh.PublicKey{ed}, want: checkSkip},
		{name: "should skip when there is no CA key to check", sshd: sshdConfig{"casignaturealgorithms": "ssh-ed25519"}, want: checkSkip},
		{name: "should pass when the CA's algorithm is allowed", sshd: sshdConfig{"casignaturealgorithms": "ssh-ed25519,rsa-sha2-512"}, signing: []ssh.PublicKey{ed}, want: checkPass},
		// A FIPS crypto policy that drops Ed25519 refuses an Ed25519 CA.
		{name: "should fail when the CA's algorithm is not allowed", sshd: sshdConfig{"casignaturealgorithms": "ecdsa-sha2-nistp256,rsa-sha2-512"}, signing: []ssh.PublicKey{ed}, want: checkFail},
		{name: "should fail an RSA CA when only SHA-256 is allowed", sshd: sshdConfig{"casignaturealgorithms": "rsa-sha2-256"}, signing: []ssh.PublicKey{rsaKey}, want: checkFail},
		// Two keys of one type during a rotation are one algorithm to check,
		// and a key the signer would refuse is not a signing algorithm.
		{name: "should check an algorithm shared by two keys once", sshd: sshdConfig{"casignaturealgorithms": "ssh-ed25519"}, signing: []ssh.PublicKey{ed, ed, unsignableKey{}}, want: checkPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := checkCASignatureAlgorithms(tt.sshd, tt.signing); got.status != tt.want {
				t.Errorf("status = %v, want %v (%s)", got.status, tt.want, got.detail)
			}
		})
	}
}

func TestCheckCertificateAlgorithms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sshd sshdConfig
		want checkStatus
	}{
		{name: "should pass when every client key type is accepted", sshd: parseSSHDConfig([]byte(defaultSSHDOutput)), want: checkPass},
		{name: "should read the keyword older OpenSSH prints", sshd: sshdConfig{"pubkeyacceptedkeytypes": strings.Join([]string{
			ssh.CertAlgoED25519v01, ssh.CertAlgoECDSA256v01, ssh.CertAlgoECDSA384v01, ssh.CertAlgoECDSA521v01, ssh.CertAlgoRSASHA512v01,
		}, ",")}, want: checkPass},
		{name: "should warn when some client key types are refused", sshd: sshdConfig{"pubkeyacceptedalgorithms": ssh.CertAlgoECDSA256v01}, want: checkWarn},
		{name: "should fail when no client key type is accepted", sshd: sshdConfig{"pubkeyacceptedalgorithms": "ssh-ed25519,ecdsa-sha2-nistp256"}, want: checkFail},
		{name: "should skip when sshd does not report the setting", sshd: sshdConfig{}, want: checkSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := checkCertificateAlgorithms(tt.sshd); got.status != tt.want {
				t.Errorf("status = %v, want %v (%s)", got.status, tt.want, got.detail)
			}
		})
	}
}

func TestCheckPlainKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sshd sshdConfig
		want checkStatus
	}{
		// Running both side by side is the documented way to adopt
		// ssoossh, so it is worth a question but never a warning.
		{name: "should ask when plain keys are still accepted", sshd: parseSSHDConfig([]byte(defaultSSHDOutput)), want: checkInfo},
		{name: "should ask when an AuthorizedKeysCommand is still set", sshd: sshdConfig{"authorizedkeysfile": "none", "authorizedkeyscommand": "/usr/bin/fetch-keys %u"}, want: checkInfo},
		{name: "should pass when only certificates are accepted", sshd: sshdConfig{"authorizedkeysfile": "none", "authorizedkeyscommand": "none"}, want: checkPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := checkPlainKeys(tt.sshd); got.status != tt.want {
				t.Errorf("status = %v, want %v (%s)", got.status, tt.want, got.detail)
			}
		})
	}
}

func TestParsePrincipalsCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want principalsInvocation
	}{
		{
			name: "should recognize the packaged command line",
			line: "/usr/bin/ssoossh host principals %u",
			want: principalsInvocation{ours: true, passesAccount: true, mappingPath: defaultMappingPath},
		},
		{
			name: "should read a mapping file given as a separate value",
			line: "/usr/bin/ssoossh host principals --file /srv/map.yaml %u",
			want: principalsInvocation{ours: true, passesAccount: true, mappingPath: "/srv/map.yaml"},
		},
		{
			name: "should read a mapping file given with an equals sign",
			line: "/usr/bin/ssoossh --file=/srv/map.yaml host principals %u",
			want: principalsInvocation{ours: true, passesAccount: true, mappingPath: "/srv/map.yaml"},
		},
		{
			name: "should skip flags and their values before the subcommand",
			line: "/usr/bin/ssoossh -v -c /etc/ssoossh/ssoossh.yaml host principals %u",
			want: principalsInvocation{ours: true, passesAccount: true, mappingPath: defaultMappingPath},
		},
		{
			name: "should notice a command line that passes no account",
			line: "/usr/bin/ssoossh host principals",
			want: principalsInvocation{ours: true, mappingPath: defaultMappingPath},
		},
		{
			name: "should not claim another program",
			line: "/usr/local/bin/principals-from-ldap %u",
			want: principalsInvocation{mappingPath: defaultMappingPath},
		},
		{
			name: "should not claim another ssoossh subcommand",
			line: "/usr/bin/ssoossh host mapping list",
			want: principalsInvocation{passesAccount: true, mappingPath: defaultMappingPath},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parsePrincipalsCommand(strings.Fields(tt.line)); got != tt.want {
				t.Errorf("parsePrincipalsCommand(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

// principalsSSHD is sshd -T output with the packaged principals lines
// uncommented.
func principalsSSHD(command string) sshdConfig {
	return sshdConfig{
		"authorizedprincipalscommand":     command,
		"authorizedprincipalscommanduser": "ssoossh-principals",
		"authorizedprincipalsfile":        "none",
	}
}

// principalsHost is a host with the client package installed as shipped:
// /usr/bin/ssoossh, the ssoossh-principals account, and a mapping file
// that account can read.
func principalsHost() *fakeHostSystem {
	stats := safeCommandStats()
	stats[defaultMappingPath] = fileStat{mode: 0o640, gid: 990, ownerKnown: true}
	return &fakeHostSystem{
		stats:    stats,
		users:    map[string]hostUser{"ssoossh-principals": {uid: 990, gids: []uint32{990}}},
		mappings: map[string]principalsmap.PrincipalsMap{defaultMappingPath: {"deploy": {"alice"}}},
	}
}

func TestCheckPrincipals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sshd  sshdConfig
		host  func() *fakeHostSystem
		check string
		want  checkStatus
	}{
		{
			name: "should note when no principals mapping is configured",
			sshd: sshdConfig{"authorizedprincipalscommand": "none", "authorizedprincipalsfile": "none"},
			host: principalsHost, check: "principals", want: checkInfo,
		},
		{
			name: "should note an AuthorizedPrincipalsFile it does not read",
			sshd: sshdConfig{"authorizedprincipalscommand": "none", "authorizedprincipalsfile": "/etc/ssh/auth_principals/%u"},
			host: principalsHost, check: "principals", want: checkInfo,
		},
		{
			name: "should pass the packaged command",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: principalsHost, check: "principals-command", want: checkPass,
		},
		{
			name: "should fail a relative command path",
			sshd: principalsSSHD("ssoossh host principals %u"),
			host: principalsHost, check: "principals-command", want: checkFail,
		},
		{
			name: "should fail a command under a group-writable directory",
			sshd: principalsSSHD("/usr/local/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats["/usr/local/bin/ssoossh"] = rootExec()
				h.stats["/usr/local/bin"] = fileStat{mode: fs.ModeDir | fs.ModeSetgid | 0o775, gid: 50, ownerKnown: true}
				h.stats["/usr/local"] = rootDir()
				return h
			},
			check: "principals-command", want: checkFail,
		},
		{
			// sshd resolves the path before judging it, so a symlink to a
			// safe binary is safe wherever the link itself lives.
			name: "should judge a symlinked command by its target",
			sshd: principalsSSHD("/opt/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.links = map[string]string{"/opt/bin/ssoossh": "/usr/bin/ssoossh"}
				return h
			},
			check: "principals-command", want: checkPass,
		},
		{
			// Measured on Debian 13's sshd: the link in root:staff 2775
			// /usr/local/bin is accepted. It breaks only when the link goes.
			name: "should warn about the compatibility link that is scheduled for removal",
			sshd: principalsSSHD("/usr/local/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.links = map[string]string{"/usr/local/bin/ssoossh": "/usr/bin/ssoossh"}
				return h
			},
			check: "principals-command", want: checkWarn,
		},
		{
			// macOS installs the real binary there; nothing will remove it.
			name: "should pass a real binary at the old path",
			sshd: principalsSSHD("/usr/local/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats["/usr/local/bin/ssoossh"] = rootExec()
				h.stats["/usr/local/bin"] = rootDir()
				h.stats["/usr/local"] = rootDir()
				return h
			},
			check: "principals-command", want: checkPass,
		},
		{
			name: "should fail a command whose path does not resolve",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.linkErrs = map[string]error{"/usr/bin/ssoossh": fs.ErrNotExist}
				return h
			},
			check: "principals-command", want: checkFail,
		},
		{
			name: "should fail a command that is not a regular file",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats["/usr/bin/ssoossh"] = rootDir()
				return h
			},
			check: "principals-command", want: checkFail,
		},
		{
			name: "should fail a command that cannot be examined",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				delete(h.stats, "/usr/bin/ssoossh")
				return h
			},
			check: "principals-command", want: checkFail,
		},
		{
			name: "should fail a command under a directory that cannot be examined",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				delete(h.stats, "/usr")
				return h
			},
			check: "principals-command", want: checkFail,
		},
		{
			name: "should fail a command file not owned by root",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats["/usr/bin/ssoossh"] = fileStat{mode: 0o755, uid: 1000, ownerKnown: true}
				return h
			},
			check: "principals-command", want: checkFail,
		},
		{
			name: "should skip the ownership check where the platform has no owners",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats["/usr/bin/ssoossh"] = fileStat{mode: 0o755}
				return h
			},
			check: "principals-command", want: checkSkip,
		},
		{
			name: "should fail when the command's account does not exist",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.users = nil
				return h
			},
			check: "principals-user", want: checkFail,
		},
		{
			name: "should fail when no command account is set",
			sshd: sshdConfig{"authorizedprincipalscommand": "/usr/bin/ssoossh host principals %u", "authorizedprincipalscommanduser": "none"},
			host: principalsHost, check: "principals-user", want: checkFail,
		},
		{
			name: "should fail a command line that passes no account",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals"),
			host: principalsHost, check: "principals", want: checkFail,
		},
		{
			name: "should note a principals command it does not evaluate",
			sshd: principalsSSHD("/usr/bin/ssoossh-other %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats["/usr/bin/ssoossh-other"] = rootExec()
				return h
			},
			check: "principals", want: checkInfo,
		},
		{
			name: "should pass a mapping that loads",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: principalsHost, check: "principals-mapping", want: checkPass,
		},
		{
			name: "should note a missing mapping file",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.mappings = nil
				return h
			},
			check: "principals-mapping", want: checkInfo,
		},
		{
			name: "should fail a mapping that will not load",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.badMaps = map[string]error{defaultMappingPath: errors.New("line 3: bad indent")}
				return h
			},
			check: "principals-mapping", want: checkFail,
		},
		{
			name: "should pass a mapping the command's account can read",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: principalsHost, check: "mapping-access", want: checkPass,
		},
		{
			name: "should fail a mapping the command's account cannot read",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats[defaultMappingPath] = fileStat{mode: 0o600, ownerKnown: true}
				return h
			},
			check: "mapping-access", want: checkFail,
		},
		{
			name: "should skip mapping access when the command's account did not resolve",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.users = nil
				return h
			},
			check: "mapping-access", want: checkSkip,
		},
		{
			name: "should fail mapping access when the file cannot be examined",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				delete(h.stats, defaultMappingPath)
				return h
			},
			check: "mapping-access", want: checkFail,
		},
		{
			name: "should skip mapping access where the platform has no owners",
			sshd: principalsSSHD("/usr/bin/ssoossh host principals %u"),
			host: func() *fakeHostSystem {
				h := principalsHost()
				h.stats[defaultMappingPath] = fileStat{mode: 0o640}
				return h
			},
			check: "mapping-access", want: checkSkip,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &hostChecker{sys: tt.host()}
			results := c.checkPrincipals(tt.sshd)
			if got := statusOf(t, results, tt.check); got != tt.want {
				t.Errorf("%s status = %v, want %v (%+v)", tt.check, got, tt.want, results)
			}
		})
	}
}

func TestHostCheckerPrincipalsFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		account string
		mapping principalsmap.PrincipalsMap
		want    string
	}{
		{name: "should list the mapped principals and the account itself", account: "deploy", mapping: principalsmap.PrincipalsMap{"deploy": {"alice", "bob"}}, want: "alice, bob, deploy"},
		{name: "should not repeat an account the mapping already lists", account: "alice", mapping: principalsmap.PrincipalsMap{"alice": {"alice"}}, want: "any of alice may"},
		{name: "should fall back to the account alone without a mapping", account: "carol", want: "any of carol may"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := (&hostChecker{account: tt.account}).principalsFor(tt.mapping)
			if len(results) != 1 || !strings.Contains(results[0].detail, tt.want) {
				t.Errorf("principalsFor() = %+v, want a detail containing %q", results, tt.want)
			}
		})
	}

	t.Run("should say nothing without --user", func(t *testing.T) {
		t.Parallel()
		if got := (&hostChecker{}).principalsFor(nil); len(got) != 0 {
			t.Errorf("principalsFor() = %+v, want nothing", got)
		}
	})
}

func TestReadableBy(t *testing.T) {
	t.Parallel()

	reader := hostUser{uid: 990, gids: []uint32{990}}

	tests := []struct {
		name string
		st   fileStat
		u    hostUser
		want bool
	}{
		{name: "should allow root regardless of mode", st: fileStat{mode: 0o000}, u: hostUser{uid: 0}, want: true},
		{name: "should allow the owner when the owner may read", st: fileStat{mode: 0o400, uid: 990}, u: reader, want: true},
		// The owner class applies alone: the group and other bits do not
		// rescue an owner whose own read bit is off.
		{name: "should refuse the owner when only others may read", st: fileStat{mode: 0o044, uid: 990}, u: reader, want: false},
		{name: "should allow a group member when the group may read", st: fileStat{mode: 0o640, gid: 990}, u: reader, want: true},
		{name: "should refuse a group member when the group may not read", st: fileStat{mode: 0o604, gid: 990}, u: reader, want: false},
		{name: "should allow anyone when others may read", st: fileStat{mode: 0o644}, u: reader, want: true},
		{name: "should refuse anyone else when others may not read", st: fileStat{mode: 0o640}, u: reader, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := readableBy(tt.st, tt.u); got != tt.want {
				t.Errorf("readableBy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHostCheckerCheckClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		synced, known bool
		want          checkStatus
	}{
		{name: "should pass a synchronized clock", synced: true, known: true, want: checkPass},
		{name: "should warn about an unsynchronized clock", known: true, want: checkWarn},
		{name: "should skip when synchronization cannot be told", want: checkSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &hostChecker{sys: &fakeHostSystem{clockSynced: tt.synced, clockKnown: tt.known}}
			if got := c.checkClock(t.Context()); got.status != tt.want {
				t.Errorf("status = %v, want %v (%s)", got.status, tt.want, got.detail)
			}
		})
	}
}

func TestHostCheckerConnectionSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		account, addr string
		want          string
	}{
		{name: "should apply no Match blocks without --user or --addr", want: ""},
		{name: "should name the account", account: "deploy", want: "user=deploy"},
		{name: "should name the account and the source address", account: "deploy", addr: "10.0.0.5", want: "user=deploy,addr=10.0.0.5,host=10.0.0.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &hostChecker{account: tt.account, addr: tt.addr}
			if got := c.connectionSpec(); got != tt.want {
				t.Errorf("connectionSpec() = %q, want %q", got, tt.want)
			}
		})
	}
}

// healthyHost is a host set up the way the docs describe: CA file in place
// and matching the server, principals command uncommented, clock in sync.
func healthyHost(t *testing.T) (*fakeHostSystem, string) {
	t.Helper()
	_, line := newTestCAKey(t, "ed25519")
	h := principalsHost()
	h.sshdOut = strings.Replace(defaultSSHDOutput,
		"authorizedprincipalscommand none\nauthorizedprincipalscommanduser none\n",
		"authorizedprincipalscommand /usr/bin/ssoossh host principals %u\nauthorizedprincipalscommanduser ssoossh-principals\n", 1)
	h.files = map[string]string{"/etc/ssh/ca.pub": line + "\n"}
	h.stats["/etc/ssh/ca.pub"] = rootFile()
	h.clockSynced, h.clockKnown, h.privileged = true, true, true
	return h, line
}

func TestHostCheckerRun(t *testing.T) {
	t.Parallel()

	t.Run("should report no failure for a host set up as documented", func(t *testing.T) {
		t.Parallel()
		h, line := healthyHost(t)
		c := &hostChecker{sys: h, reference: func(context.Context) ([]string, string, error) {
			return []string{line}, "the server", nil
		}}
		for _, r := range c.run(t.Context()) {
			if r.status == checkFail {
				t.Errorf("unexpected failure: %+v", r)
			}
		}
	})

	t.Run("should fail the CA check for a host that trusts no CA", func(t *testing.T) {
		t.Parallel()
		h, line := healthyHost(t)
		h.sshdOut = strings.Replace(h.sshdOut, "trustedusercakeys /etc/ssh/ca.pub", "trustedusercakeys none", 1)
		c := &hostChecker{sys: h, reference: func(context.Context) ([]string, string, error) {
			return []string{line}, "the server", nil
		}}
		if got := statusOf(t, c.run(t.Context()), "trusted-ca"); got != checkFail {
			t.Errorf("trusted-ca status = %v, want %v", got, checkFail)
		}
	})

	t.Run("should warn rather than fail when the CA cannot be compared", func(t *testing.T) {
		t.Parallel()
		h, _ := healthyHost(t)
		c := &hostChecker{sys: h, reference: func(context.Context) ([]string, string, error) {
			return nil, "", errNoCAReference
		}}
		if got := statusOf(t, c.run(t.Context()), "ca-match"); got != checkWarn {
			t.Errorf("ca-match status = %v, want %v", got, checkWarn)
		}
	})

	t.Run("should hand sshd the connection spec for --user", func(t *testing.T) {
		t.Parallel()
		h, line := healthyHost(t)
		c := &hostChecker{sys: h, account: "deploy", reference: func(context.Context) ([]string, string, error) {
			return []string{line}, "the server", nil
		}}
		c.run(t.Context())
		if h.sshdSpec != "user=deploy" {
			t.Errorf("sshd -C spec = %q, want %q", h.sshdSpec, "user=deploy")
		}
	})

	t.Run("should stop with one failure when sshd -T fails", func(t *testing.T) {
		t.Parallel()
		h := &fakeHostSystem{sshdErr: errors.New("sshd: no hostkeys available -- exiting.")}
		results := (&hostChecker{sys: h}).run(t.Context())
		if len(results) != 1 || results[0].status != checkFail {
			t.Errorf("run() = %+v, want a single failure", results)
		}
	})

	t.Run("should tell an unprivileged caller to run as root when sshd -T fails", func(t *testing.T) {
		t.Parallel()
		h := &fakeHostSystem{sshdErr: errors.New("sshd: no hostkeys available -- exiting.")}
		results := (&hostChecker{sys: h}).run(t.Context())
		if !strings.Contains(results[0].hint, "root") {
			t.Errorf("hint = %q, want it to mention root", results[0].hint)
		}
	})
}

func TestHostCheckerCheckScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		account  string
		wantHint bool
	}{
		{name: "should suggest --user when Match blocks were not applied", wantHint: true},
		{name: "should name the account when Match blocks were applied", account: "deploy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := (&hostChecker{account: tt.account}).checkScope()
			if (got.hint != "") != tt.wantHint {
				t.Errorf("checkScope() = %+v, want hint %v", got, tt.wantHint)
			}
		})
	}
}

func TestWriteHostCheckReport(t *testing.T) {
	t.Parallel()

	t.Run("should return an error when a check failed", func(t *testing.T) {
		t.Parallel()
		err := writeHostCheckReport(&bytes.Buffer{}, []checkResult{{status: checkFail, name: "trusted-ca", detail: "none"}})
		if err == nil {
			t.Error("writeHostCheckReport() error = nil, want an error so the command exits non-zero")
		}
	})

	t.Run("should return no error when only warnings were found", func(t *testing.T) {
		t.Parallel()
		err := writeHostCheckReport(&bytes.Buffer{}, []checkResult{{status: checkWarn, name: "clock"}, {status: checkPass, name: "pubkey-auth"}})
		if err != nil {
			t.Errorf("writeHostCheckReport() error = %v, want nil", err)
		}
	})

	t.Run("should print the hint beneath its finding", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		_ = writeHostCheckReport(&out, []checkResult{{status: checkFail, name: "trusted-ca", detail: "TrustedUserCAKeys is none", hint: "ssoossh ca > /etc/ssh/ca.pub"}})
		want := "FAIL  trusted-ca  TrustedUserCAKeys is none\n                  ssoossh ca > /etc/ssh/ca.pub\n"
		if !strings.HasPrefix(out.String(), want) {
			t.Errorf("report =\n%s\nwant it to start with\n%s", out.String(), want)
		}
	})

	t.Run("should end with a tally", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		_ = writeHostCheckReport(&out, []checkResult{{status: checkPass, name: "a"}, {status: checkWarn, name: "b"}, {status: checkFail, name: "c"}})
		if !strings.HasSuffix(out.String(), "1 passed, 1 warnings, 1 failed\n") {
			t.Errorf("report =\n%s\nwant it to end with the tally", out.String())
		}
	})
}

func TestCheckStatusString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status checkStatus
		want   string
	}{
		{checkPass, "PASS"}, {checkInfo, "INFO"}, {checkSkip, "SKIP"}, {checkWarn, "WARN"}, {checkFail, "FAIL"},
	}
	for _, tt := range tests {
		t.Run("should render "+tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.status.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// getCAClient is an api.Client whose GetCA answers with fixed text, for
// the server half of referenceCAKeys.
type getCAClient struct {
	fakeAPIClient
	ca  string
	err error
}

func (c *getCAClient) GetCA(context.Context) (string, error) { return c.ca, c.err }

func TestRootCommandReferenceCAKeys(t *testing.T) {
	t.Parallel()

	_, pinned := newTestCAKey(t, "ed25519")
	_, served := newTestCAKey(t, "ecdsa")

	tests := []struct {
		name       string
		cfg        *config.Config
		client     api.Client
		clientErr  error
		wantKeys   []string
		wantSource string
		wantErr    error
	}{
		{
			name: "should prefer a pinned capubkey over the server",
			cfg:  &config.Config{CAPubkey: []string{pinned}, Server: "https://ssh.example.com"},
			// A client that would answer differently proves it was not asked.
			client:   &getCAClient{ca: served},
			wantKeys: []string{pinned}, wantSource: "the pinned capubkey",
		},
		{
			name:     "should ask the server when no capubkey is pinned",
			cfg:      &config.Config{Server: "https://ssh.example.com"},
			client:   &getCAClient{ca: served + "\n"},
			wantKeys: []string{served}, wantSource: "https://ssh.example.com",
		},
		{name: "should report that there is nothing to compare with", cfg: &config.Config{}, wantErr: errNoCAReference},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := &RootCommand{
				cfg: tt.cfg,
				newAPIClient: func(*config.Config) (api.Client, error) {
					return tt.client, tt.clientErr
				},
			}
			keys, source, err := root.referenceCAKeys(t.Context())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("referenceCAKeys() error = %v, want %v", err, tt.wantErr)
			}
			if strings.Join(keys, "\n") != strings.Join(tt.wantKeys, "\n") || source != tt.wantSource {
				t.Errorf("referenceCAKeys() = %q from %q, want %q from %q", keys, source, tt.wantKeys, tt.wantSource)
			}
		})
	}

	t.Run("should fail when the server cannot be asked", func(t *testing.T) {
		t.Parallel()
		root := &RootCommand{
			cfg:          &config.Config{Server: "https://ssh.example.com"},
			newAPIClient: func(*config.Config) (api.Client, error) { return &getCAClient{err: errors.New("refused")}, nil },
		}
		if _, _, err := root.referenceCAKeys(t.Context()); err == nil {
			t.Error("referenceCAKeys() error = nil, want the server's error")
		}
	})

	t.Run("should fail when no API client can be built", func(t *testing.T) {
		t.Parallel()
		root := &RootCommand{
			cfg:          &config.Config{Server: "https://ssh.example.com"},
			newAPIClient: func(*config.Config) (api.Client, error) { return nil, errors.New("bad TLS config") },
		}
		if _, _, err := root.referenceCAKeys(t.Context()); err == nil {
			t.Error("referenceCAKeys() error = nil, want the build error")
		}
	})
}

// Not parallel: the PATH cases change the process environment.
func TestResolveSSHDPath(t *testing.T) {
	t.Run("should use the path given with --sshd", func(t *testing.T) {
		if got := resolveSSHDPath("/opt/openssh/sbin/sshd"); got != "/opt/openssh/sbin/sshd" {
			t.Errorf("resolveSSHDPath() = %q, want the flag's value", got)
		}
	})

	t.Run("should fall back to the usual location when sshd is not on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got := resolveSSHDPath(""); got != defaultSSHDPath {
			t.Errorf("resolveSSHDPath() = %q, want %q", got, defaultSSHDPath)
		}
	})
}
