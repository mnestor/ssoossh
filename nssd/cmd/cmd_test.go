package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/server"
)

// fakeDirectory is an ssoosshd answering the four NSS lookups over TLS,
// requiring the fleet key.
func fakeDirectory(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	home, shell := "/home/alice", "/bin/sh"
	alice := apitypes.NSSUserResponse{
		Name: "alice", UID: 5001, GID: 5001,
		Groups: []apitypes.NSSMembership{{GID: 5001, Name: "alice"}, {GID: 5100, Name: "devs"}, {GID: 2005, Name: "users"}},
		Home:   &home, Shell: &shell, Gecos: "Alice Smith", Status: apitypes.NSSStatusActive, KeyID: "nss_test",
	}
	devs := apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice"}}
	send := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	notFound := apitypes.Envelope[any]{Error: "not found", ErrorCode: apitypes.ErrorCodeNSSNotFound}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/nss/user/name/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != "alice" {
			send(w, http.StatusNotFound, notFound)
			return
		}
		send(w, http.StatusOK, apitypes.Envelope[apitypes.NSSUserResponse]{Data: alice})
	})
	mux.HandleFunc("GET /api/nss/user/uid/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("uid") != "5001" {
			send(w, http.StatusNotFound, notFound)
			return
		}
		send(w, http.StatusOK, apitypes.Envelope[apitypes.NSSUserResponse]{Data: alice})
	})
	mux.HandleFunc("GET /api/nss/group/name/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != "devs" {
			send(w, http.StatusNotFound, notFound)
			return
		}
		send(w, http.StatusOK, apitypes.Envelope[apitypes.NSSGroupResponse]{Data: devs})
	})
	mux.HandleFunc("GET /api/nss/group/gid/{gid}", func(w http.ResponseWriter, r *http.Request) {
		send(w, http.StatusNotFound, notFound)
	})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(apitypes.HeaderNSSKeyID) != "nss_test" || r.Header.Get(apitypes.HeaderNSSKeySecret) != "s3cret" {
			send(w, http.StatusUnauthorized, apitypes.Envelope[any]{Error: "unauthorized", ErrorCode: apitypes.ErrorCodeNSSUnauthorized})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
	return srv, base64.StdEncoding.EncodeToString(sum[:])
}

// host is a temporary host: files, sockets and a configuration.
type host struct {
	dir, config, key, overrides, socket, admin, cache, nsswitch, proc string
	env                                                               Environment
	out, errOut                                                       *syncBuffer
}

// syncBuffer is a bytes.Buffer safe for the daemon's concurrent log
// writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns what was written.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Reset empties the buffer.
func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// newHost writes a host whose configuration points at serverURL with pin.
func newHost(t *testing.T, serverURL, pin, extraConfig string) *host {
	t.Helper()
	dir := t.TempDir()
	if len(dir) > 70 {
		var err error
		if dir, err = os.MkdirTemp("/tmp", "nssd-cmd"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	h := &host{
		dir:       dir,
		config:    filepath.Join(dir, "nss.yaml"),
		key:       filepath.Join(dir, "nss.key"),
		overrides: filepath.Join(dir, "ovr.yaml"),
		socket:    filepath.Join(dir, "userdb.sock"),
		admin:     filepath.Join(dir, "run", "admin.sock"),
		cache:     filepath.Join(dir, "state", "cache.db"),
		nsswitch:  filepath.Join(dir, "nsswitch.conf"),
		proc:      filepath.Join(dir, "proc"),
		out:       &syncBuffer{},
		errOut:    &syncBuffer{},
	}
	files := map[string]string{
		h.config:                           fmt.Sprintf("server: %s\ntls_spki_pin: [%q]\napi_key_file: %s\nsocket_answer_deadline: 2s\nrefresh_rate: 100\n%s", serverURL, pin, h.key, extraConfig),
		h.key:                              "nss_test\ns3cret\n",
		h.nsswitch:                         "passwd: files systemd\ngroup: files systemd\nshadow: files\n",
		filepath.Join(dir, "passwd"):       "root:x:0:0::/root:/bin/sh\nbob:x:1001:1001::/home/bob:/bin/sh\n",
		filepath.Join(dir, "group"):        "root:x:0:\nusers:x:100:\n",
		filepath.Join(dir, "shells"):       "/bin/sh\n/bin/bash\n",
		filepath.Join(h.proc, "1", "comm"): "nscd\n",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.env = Environment{
		Stdout:     h.out,
		Stderr:     h.errOut,
		NSSwitch:   h.nsswitch,
		Proc:       h.proc,
		HostFiles:  policy.Paths{Passwd: filepath.Join(dir, "passwd"), Group: filepath.Join(dir, "group"), Shells: filepath.Join(dir, "shells")},
		Activation: server.Activation{Getenv: func(string) string { return "" }},
	}
	return h
}

// globals are the flags every command gets on this host.
func (h *host) globals() []string {
	return []string{"--config", h.config, "--admin-socket", h.admin, "--overrides", h.overrides}
}

// exec runs one command and returns its stdout.
func (h *host) exec(t *testing.T, args ...string) (string, error) {
	t.Helper()
	h.out.Reset()
	c, err := NewCommand(h.env)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Execute(t.Context(), append(args, h.globals()...))
	return h.out.String(), err
}

// startDaemon runs the daemon until the test ends.
func (h *host) startDaemon(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	c, err := NewCommand(h.env)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- c.Execute(ctx, append(args, h.globals()...)) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the daemon stopped with %v", err)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(h.admin); err == nil {
			if _, err := os.Stat(h.socket); err == nil {
				return
			}
		}
		select {
		case err := <-done:
			t.Fatalf("the daemon exited at start: %v\n%s", err, h.errOut)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not start:\n%s", h.errOut)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDaemonEndToEnd(t *testing.T) {
	t.Parallel()
	srv, pin := fakeDirectory(t)
	h := newHost(t, srv.URL, pin, "")
	h.startDaemon(t, "run", "--listen", h.socket, "--cache-file", h.cache, "--log-level", "debug")

	out, err := h.exec(t, "lookup", "user", "alice", "--socket", h.socket)
	if err != nil || !strings.Contains(out, `"userName": "alice"`) || !strings.Contains(out, `"realName": "Alice Smith"`) {
		t.Fatalf("lookup user: %v\n%s", err, out)
	}
	if out, err := h.exec(t, "lookup", "group", "devs", "--socket", h.socket); err != nil || !strings.Contains(out, `"groupName": "devs"`) {
		t.Fatalf("lookup group: %v\n%s", err, out)
	}
	if out, err := h.exec(t, "lookup", "memberships", "alice", "--socket", h.socket); err != nil || strings.Count(out, `"userName": "alice"`) != 2 {
		t.Fatalf("lookup memberships: %v\n%s", err, out)
	}
	if _, err := h.exec(t, "lookup", "user", "nobody", "--socket", h.socket); err == nil || !strings.Contains(err.Error(), "NoRecordFound") {
		t.Fatalf("lookup nobody: %v", err)
	}
	if _, err := h.exec(t, "lookup", "user", "bob", "--socket", h.socket); err == nil || !strings.Contains(err.Error(), "NoRecordFound") {
		t.Fatalf("should answer a local name NoRecordFound: %v", err)
	}

	out, err = h.exec(t, "status")
	if err == nil {
		t.Fatal("status should fail when the shadow line lacks systemd")
	}
	for _, want := range []string{"state:         online", "key id:        nss_test", "1 users", `the shadow: line "files"`, "nscd is running",
		"collisions:    1 group name collision", "  users: directory gid 2005, local gid 100, first seen "} {
		if !strings.Contains(out, want) {
			t.Fatalf("status should say %q:\n%s", want, out)
		}
	}
	if err := os.WriteFile(h.nsswitch, []byte("passwd: files systemd\ngroup: files systemd\nshadow: files systemd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := h.exec(t, "status"); err != nil || !strings.Contains(out, "nsswitch.conf: ok") {
		t.Fatalf("status: %v\n%s", err, out)
	}

	if !strings.Contains(h.errOut.String(), `"msg":"a directory group has the same name as a local group`) {
		t.Fatalf("the daemon should warn about the collision:\n%s", h.errOut)
	}
	if out, err := h.exec(t, "cache", "-u", "alice"); err != nil || !strings.Contains(out, "marked 1 cached entries stale") {
		t.Fatalf("cache -u: %v\n%s", err, out)
	}
	for _, flag := range []string{"-E", "-U", "-G"} {
		if _, err := h.exec(t, "cache", flag); err != nil {
			t.Fatalf("cache %s: %v", flag, err)
		}
	}
	if _, err := h.exec(t, "cache", "-g", "devs"); err != nil {
		t.Fatalf("cache -g: %v", err)
	}
	if _, err := h.exec(t, "cache"); err == nil {
		t.Fatal("cache with no selection should fail")
	}

	if _, err := h.exec(t, "override", "set", "alice", "--home", "/srv/alice", "--uid", "6001"); err != nil {
		t.Fatalf("override set: %v", err)
	}
	if out, err := h.exec(t, "lookup", "user", "alice", "--socket", h.socket); err != nil || !strings.Contains(out, `"homeDirectory": "/srv/alice"`) || !strings.Contains(out, `"uid": 6001`) {
		t.Fatalf("should serve the override at once: %v\n%s", err, out)
	}
	if out, err := h.exec(t, "override", "list"); err != nil || !strings.Contains(out, "alice") || !strings.Contains(out, "6001") || !strings.Contains(out, "/srv/alice") {
		t.Fatalf("override list: %v\n%s", err, out)
	}
	if _, err := h.exec(t, "override", "set", "alice", "--uid", "5"); err == nil || !strings.Contains(err.Error(), "outside min_id") {
		t.Fatalf("should refuse a uid below min_id: %v", err)
	}
	if _, err := h.exec(t, "override", "set", "alice", "--home", "", "--uid", "5001"); err != nil {
		t.Fatalf("override set clearing home: %v", err)
	}
	if _, err := h.exec(t, "override", "unset", "alice"); err != nil {
		t.Fatalf("override unset: %v", err)
	}
	if _, err := h.exec(t, "override", "unset", "alice"); err == nil {
		t.Fatal("unset of a user with no override should fail")
	}
	if out, err := h.exec(t, "override"); err != nil || !strings.Contains(out, "list") {
		t.Fatalf("override alone should print help: %v\n%s", err, out)
	}

	if !strings.Contains(h.errOut.String(), `"msg":"ssoossh-nss started"`) {
		t.Fatalf("should log the start:\n%s", h.errOut)
	}
}

func TestDaemonShouldServeFromTheBareCommandWhenNoSubcommandIsGiven(t *testing.T) {
	t.Parallel()
	srv, pin := fakeDirectory(t)
	h := newHost(t, srv.URL, pin, "cache_mode: memory\n")
	h.startDaemon(t, "--listen", h.socket)
	if out, err := h.exec(t, "lookup", "user", "alice", "--socket", h.socket); err != nil || !strings.Contains(out, "alice") {
		t.Fatalf("lookup: %v\n%s", err, out)
	}
	if !strings.Contains(h.errOut.String(), "nsswitch.conf will not consult this daemon") {
		t.Fatalf("should log the shadow line at start:\n%s", h.errOut)
	}
}

func TestDaemonShouldRefuseToStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, h *host) []string
		wantErr string
	}{
		{name: "should refuse when the config is missing", setup: func(t *testing.T, h *host) []string {
			_ = os.Remove(h.config)
			return []string{"run", "--listen", h.socket}
		}, wantErr: "no such file"},
		{name: "should refuse when the overrides file is broken", setup: func(t *testing.T, h *host) []string {
			if err := os.WriteFile(h.overrides, []byte("alice: [\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"run", "--listen", h.socket}
		}, wantErr: "overrides"},
		{name: "should refuse when the key file is world-readable", setup: func(t *testing.T, h *host) []string {
			if err := os.Chmod(h.key, 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{"run", "--listen", h.socket, "--cache-file", h.cache}
		}, wantErr: "fleet key"},
		{name: "should refuse when neither activated nor given --listen", setup: func(t *testing.T, h *host) []string {
			return []string{"run", "--cache-file", h.cache}
		}, wantErr: "not socket-activated and no --listen"},
		{name: "should refuse when activation passes two sockets", setup: func(t *testing.T, h *host) []string {
			pid := strconv.Itoa(os.Getpid())
			h.env.Activation = server.Activation{Getenv: func(k string) string {
				return map[string]string{"LISTEN_PID": pid, "LISTEN_FDS": "2"}[k]
			}}
			return []string{"run", "--cache-file", h.cache}
		}, wantErr: "exactly one"},
		{name: "should refuse when the log level is unknown", setup: func(t *testing.T, h *host) []string {
			return []string{"run", "--log-level", "loud"}
		}, wantErr: "--log-level"},
		{name: "should refuse when the cache cannot be opened", setup: func(t *testing.T, h *host) []string {
			return []string{"run", "--listen", h.socket, "--cache-file", h.dir}
		}, wantErr: "cache"},
		{name: "should refuse when the admin socket cannot be bound", setup: func(t *testing.T, h *host) []string {
			if err := os.WriteFile(filepath.Join(h.dir, "blocker"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			h.admin = filepath.Join(h.dir, "blocker", "admin.sock")
			return []string{"run", "--listen", h.socket, "--cache-file", h.cache}
		}, wantErr: "admin socket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHost(t, "https://127.0.0.1:1", "AAAA", "")
			args := tt.setup(t, h)
			if _, err := h.exec(t, args...); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCommandsShouldFailWhenTheDaemonIsNotRunning(t *testing.T) {
	t.Parallel()
	h := newHost(t, "https://127.0.0.1:1", "AAAA", "")

	out, err := h.exec(t, "status")
	if err == nil || !strings.Contains(out, "daemon:        not answering") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if _, err := h.exec(t, "cache", "-E"); err == nil {
		t.Fatal("cache should fail with no daemon")
	}
	if _, err := h.exec(t, "lookup", "user", "alice", "--socket", h.socket); err == nil {
		t.Fatal("lookup should fail with no daemon")
	}
	// Editing overrides still works, with a warning.
	if _, err := h.exec(t, "override", "set", "alice", "--shell", "/bin/bash"); err != nil {
		t.Fatalf("override set: %v", err)
	}
	if !strings.Contains(h.errOut.String(), "the daemon is not running") {
		t.Fatalf("should warn that the daemon will read the file at start:\n%s", h.errOut)
	}
	if _, err := h.exec(t, "override", "set", "alice", "--shell", "relative"); err == nil {
		t.Fatal("should refuse an invalid shell")
	}
	if err := os.Chmod(h.config, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.exec(t, "override", "set", "alice", "--uid", "6000"); err == nil || !strings.Contains(err.Error(), "id range") {
		t.Fatalf("should need the configuration for the id range: %v", err)
	}
	if err := os.WriteFile(h.overrides, []byte("bad: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.exec(t, "override", "list"); err == nil {
		t.Fatal("list should fail on a broken file")
	}
	if _, err := h.exec(t, "override", "unset", "alice"); err == nil {
		t.Fatal("unset should fail on a broken file")
	}
}

// Not parallel: SIGHUP goes to the whole test process, so it would reset
// every daemon a parallel test is running and race their status checks.
func TestReloadOnShouldResetWhenASignalArrives(t *testing.T) {
	srv, pin := fakeDirectory(t)
	h := newHost(t, srv.URL, pin, "")
	h.startDaemon(t, "run", "--listen", h.socket, "--cache-file", h.cache)
	// The real process gets SIGHUP; the daemon registers its handler
	// before it binds a socket, so this cannot kill the test binary.
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(h.errOut.String(), "SIGHUP: reloading") {
		if time.Now().After(deadline) {
			t.Fatalf("no reload logged:\n%s", h.errOut)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDefaultEnvironmentShouldNameTheRealHostWhenUsedInProduction(t *testing.T) {
	t.Parallel()
	env := DefaultEnvironment()
	if env.NSSwitch != "/etc/nsswitch.conf" || env.Proc != "/proc" || env.HostFiles.Passwd != "/etc/passwd" || env.Peers == nil {
		t.Fatalf("got %+v", env)
	}
	c, err := NewCommand(Environment{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err != nil || c == nil {
		t.Fatalf("should default the peer inspector, got %v", err)
	}
}

func TestStatusShouldShowTheLastErrorWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	h := newHost(t, "https://127.0.0.1:1", "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=", "reconnection_retries: 0\ncache_mode: memory\n")
	h.startDaemon(t, "run", "--listen", h.socket)
	if _, err := h.exec(t, "lookup", "user", "alice", "--socket", h.socket); err == nil || !strings.Contains(err.Error(), "ServiceNotAvailable") {
		t.Fatalf("lookup: %v", err)
	}
	out, _ := h.exec(t, "status")
	for _, want := range []string{"state:         offline", "retry at:", "last error:", "connection refused"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status should say %q:\n%s", want, out)
		}
	}
}

func TestOverrideSetShouldNameTheRangeWhenMaxIDIsSet(t *testing.T) {
	t.Parallel()
	h := newHost(t, "https://127.0.0.1:1", "AAAA", "max_id: 60000\n")
	if _, err := h.exec(t, "override", "set", "alice", "--gid", "70000"); err == nil || !strings.Contains(err.Error(), "(1000..60000)") {
		t.Fatalf("got %v", err)
	}
}

// TestMainShouldReturnTheExitStatusWhenRun runs Main itself. Not
// parallel: it sets os.Args, which every test in the package would
// otherwise see.
func TestMainShouldReturnTheExitStatusWhenRun(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })

	os.Args = []string{"ssoossh-nss", "cache", "--help"}
	if code := Main(); code != 0 {
		t.Fatalf("got %d for --help", code)
	}
	os.Args = []string{"ssoossh-nss", "no-such-command"}
	if code := Main(); code != 1 {
		t.Fatalf("got %d for an unknown command", code)
	}
}

// TestCommandShouldExposeTheRealTreeWhenAskedForManPages checks the tree
// gendocs writes the man pages from is the one Execute runs, subcommands
// and flags included, so a page cannot describe a command that is not
// there.
func TestCommandShouldExposeTheRealTreeWhenAskedForManPages(t *testing.T) {
	t.Parallel()
	c, err := NewCommand(Environment{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("build the tree: %v", err)
	}
	root := c.Command()
	if root == nil || root.Name() != "ssoossh-nss" {
		t.Fatalf("got root %v, want ssoossh-nss", root)
	}
	tests := []struct {
		name string
		path []string
		flag string
	}{
		{name: "should carry the global config flag on the root", flag: "config"},
		{name: "should carry the run command and its listen flag", path: []string{"run"}, flag: "listen"},
		{name: "should carry the cache command", path: []string{"cache"}},
		{name: "should carry the override command", path: []string{"override"}},
		{name: "should carry the status command", path: []string{"status"}},
		{name: "should carry the lookup command", path: []string{"lookup"}},
	}
	// Serial: cobra's Find merges persistent flags into the shared tree,
	// so parallel subtests race on it.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := "ssoossh-nss"
			if len(tt.path) > 0 {
				want = tt.path[len(tt.path)-1]
			}
			cmd, _, err := root.Find(tt.path)
			if err != nil || cmd.Name() != want {
				t.Fatalf("find %v: got %v, %v", tt.path, cmd, err)
			}
			if tt.flag != "" && cmd.Flags().Lookup(tt.flag) == nil && cmd.PersistentFlags().Lookup(tt.flag) == nil {
				t.Fatalf("%v has no --%s", tt.path, tt.flag)
			}
		})
	}
}
