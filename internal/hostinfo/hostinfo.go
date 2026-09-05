// Package hostinfo collects what the ssoossh client can honestly say about
// the process and machine asking for a certificate.
//
// It is the Go client's half of the same report pam_ssoossh sends from a
// host (see https://mnestor.github.io/ssoossh/internals/host-context/).
// Until this existed a user certificate carried only local_username and
// local_hostname, so an approver deciding a `ssh login` saw two strings
// where an approver deciding a `sudo` saw a dozen, and the certificate's
// audit trail was thinner for the type a person uses every day.
//
// Every value here is self-reported by an unauthenticated caller and the
// server bounds and renders each as a claim. Nothing in this package feeds
// a decision: principals come from the approver's held accounts and the
// lifetime from policy. It is context for a human and join keys for a log.
//
// Collect never fails. A field the platform cannot answer is left empty,
// which is the same "not reported" the C module produces where it has no
// source -- an approver reading a blank row learns something true, and a
// login must not fail over audit metadata.
package hostinfo

import (
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/internal/version"
)

// maxFieldLen mirrors service.maxContextFieldLen, the server's bound on
// every one of these strings. Applied here as well so what the client sends
// is what the approver sees: a value silently halved on arrival is worse
// than one this side chose to cut.
const maxFieldLen = 256

// HostContext is one client's report about itself. Pointers on the numeric
// fields for the same reason the wire types use them: absent has to be
// distinguishable from uid 0, and from pid 0 on a platform that has no
// answer at all.
type HostContext struct {
	Username       string
	Hostname       string
	RequestingUser string
	Process        string
	TTY            string
	RemoteHost     string
	CallerUID      *int64
	CallerGID      *int64
	CallerPID      *int64
	CallerPPID     *int64
	MachineID      string
	OS             string
	Client         string
	ClientTime     *time.Time
}

// Collect reads everything this platform can answer. Best-effort
// throughout: see the package comment.
func Collect() HostContext {
	now := time.Now().UTC()
	hc := HostContext{
		Username:       currentUsername(),
		Hostname:       hostname(),
		RequestingUser: requestingUser(),
		Process:        commandLine(),
		TTY:            trunc(ttyName()),
		RemoteHost:     remoteHost(),
		MachineID:      trunc(machineID()),
		OS:             trunc(osName()),
		// The same shape the C module uses ("pam_ssoossh-c/0.3.0"), so one
		// log can tell the two implementations and their builds apart.
		Client:     trunc(version.Name + "/" + version.Version),
		ClientTime: &now,
	}

	// Windows has no uid or gid: os.Getuid and os.Getgid return -1 there,
	// which is not a value an approver should be shown as one. The process
	// ids are real on every supported platform.
	if uid := os.Getuid(); uid >= 0 {
		hc.CallerUID = int64p(int64(uid))
	}
	if gid := os.Getgid(); gid >= 0 {
		hc.CallerGID = int64p(int64(gid))
	}
	hc.CallerPID = int64p(int64(os.Getpid()))
	hc.CallerPPID = int64p(int64(os.Getppid()))

	return hc
}

func int64p(v int64) *int64 { return &v }

// trunc bounds a field to what the server will keep.
func trunc(s string) string {
	if len(s) <= maxFieldLen {
		return s
	}
	return s[:maxFieldLen]
}

// currentUsername is the local account running the client -- PAM_USER's
// analogue for a user certificate, and what the server stores as
// local_username.
func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		// not covered: user.Current fails only where the process has no
		// passwd entry and no USER in the environment, which a test cannot
		// produce without replacing the runner's own environment for every
		// other test in the package.
		return ""
	}
	return trunc(u.Username)
}

// hostname is the machine the client is running on, stored as
// local_hostname.
func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		// not covered: gethostname(2) fails only on a bad buffer, which
		// the standard library does not hand it.
		return ""
	}
	return trunc(name)
}

// requestingUser is PAM_RUSER's analogue: who invoked this, as opposed to
// the account it runs as. sudo is the case that makes the two differ, and
// it is the case worth reporting -- "alice ran ssoossh as root" is only
// readable with both halves.
//
// Reported only when it actually differs from the account we are running
// as, matching the approval page's own rule for the PAM field: a value
// equal to the account adds a row that explains nothing.
func requestingUser() string {
	invoker := os.Getenv("SUDO_USER")
	if invoker == "" || invoker == currentUsername() {
		return ""
	}
	return trunc(invoker)
}

// commandLine is this process's own argv, the analogue of the PAM host
// process's /proc/self/cmdline. `ssoossh ssh login --force` and an
// invocation from a ProxyCommand are different asks, and the second is the
// one an approver would otherwise have no way to recognise.
//
// argv[0] is the path the shell resolved, which on a normal install is
// noise; the base name is what a person reads.
func commandLine() string {
	if len(os.Args) == 0 {
		// not covered: a process always has argv[0]. The guard is here
		// because indexing os.Args without one would panic, and a panic in
		// audit metadata would take down a login.
		return ""
	}
	parts := make([]string, 0, len(os.Args))
	parts = append(parts, baseName(os.Args[0]))
	parts = append(parts, os.Args[1:]...)
	return trunc(strings.Join(parts, " "))
}

// baseName is filepath.Base without importing path/filepath for one call
// that has to treat both separators alike: the client runs on Windows, and
// argv[0] there arrives with backslashes.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// remoteHost is PAM_RHOST's analogue: the peer address when this client is
// itself running inside an SSH session. Empty at a local terminal, which is
// the same distinction that makes a non-empty remote host on a console
// request worth flagging.
//
// SSH_CONNECTION is "<client ip> <client port> <server ip> <server port>",
// so the first field is the peer. SSH_CLIENT is the older spelling of the
// same thing and is read when the newer one is absent.
func remoteHost() string {
	if conn := os.Getenv("SSH_CONNECTION"); conn != "" {
		if peer, _, found := strings.Cut(conn, " "); found || peer != "" {
			return trunc(peer)
		}
	}
	if client := os.Getenv("SSH_CLIENT"); client != "" {
		if peer, _, found := strings.Cut(client, " "); found || peer != "" {
			return trunc(peer)
		}
	}
	return ""
}
