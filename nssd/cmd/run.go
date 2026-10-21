package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/server"
	"github.com/mnestor/ssoossh/nssd/upstream"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// runFlags are the daemon's own flags, on the root command and on run.
type runFlags struct {
	listen    string
	cacheFile string
	logLevel  string
}

// register adds the flags to cmd.
func (f *runFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.listen, "listen", "",
		"bind the userdb socket at this path when not socket-activated (the RHEL 8 standalone service, where there is no systemd-userdbd to activate it, and development and tests)")
	flags.StringVar(&f.cacheFile, "cache-file", config.DefaultCachePath, "the persistent cache's SQLite file")
	flags.StringVar(&f.logLevel, "log-level", "info", "debug, info, warn or error")
}

// newRunCommand is `ssoossh-nss run`.
func newRunCommand(root *RootCommand) *runCommand { return &runCommand{leaf: leaf{root: root}} }

// runCommand is `run`, the daemon. Its flags are its own copy of the
// root's, since a flag set belongs to one cobra command.
type runCommand struct {
	leaf
	flags runFlags
}

// Init implements simplecobra.Commander.
func (c *runCommand) Init(cd *simplecobra.Commandeer) error {
	c.name = "run"
	c.short = "Run the daemon (the default with no subcommand)."
	c.long = "Runs the userdb service. Under systemd the userdb socket comes from " +
		"ssoossh-nss.socket (socket activation); otherwise --listen binds one. " +
		"SIGHUP re-reads the host files and the fleet key and clears the 401 latch, the offline backoff and " +
		"the group name collisions and refused groups that status lists."
	if err := c.leaf.Init(cd); err != nil {
		// not covered: leaf.Init never fails.
		return err
	}
	c.flags.register(cd.CobraCommand)
	return nil
}

// Run implements simplecobra.Commander.
func (c *runCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return c.root.runDaemon(ctx, c.flags)
}

// Name implements simplecobra.Commander.
func (c *runCommand) Name() string { return "run" }

// newLogger returns the daemon's logger: JSON lines on w, which systemd
// sends to the journal.
func newLogger(w io.Writer, level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("--log-level %q: %w", level, err)
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l})), nil
}

// runDaemon is the daemon: load and check everything, open the cache and
// start accepting before any upstream call (socket activation queues
// lookups until then), and serve until ctx ends.
func (r *RootCommand) runDaemon(ctx context.Context, flags runFlags) error {
	// Registered before any socket is bound, so from the moment anything
	// can reach the daemon a SIGHUP reloads rather than kills it.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	log, err := newLogger(r.env.Stderr, flags.logLevel)
	if err != nil {
		return err
	}
	// nss.yaml, the override file and the fleet key, every problem at
	// once: the same call `config check` reports from.
	cfg, err := config.LoadStartup(r.configPath, r.overrides)
	if err != nil {
		return err
	}
	for _, problem := range r.nsswitchProblems() {
		log.Error(nsswitchProblemMsg, "problem", problem)
	}

	store, err := openStore(ctx, cfg, flags.cacheFile)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	up, err := upstream.New(upstream.Options{Config: cfg, Log: log})
	if err != nil {
		return err
	}
	paths := r.env.HostFiles
	paths.Overrides = r.overrides
	daemon := userdb.New(userdb.Options{
		Config:   cfg,
		Store:    store,
		Upstream: up,
		Policy:   policy.New(cfg, paths, log),
		Clock:    cache.SystemClock{},
		Log:      log,
	})

	userdbL, err := r.userdbListener(flags.listen)
	if err != nil {
		return err
	}
	adminL, err := server.ListenAdmin(r.adminSocket)
	if err != nil {
		_ = userdbL.Close()
		return err
	}
	srv := server.New(server.Options{
		Daemon:         daemon,
		Peers:          r.env.Peers,
		SSHDUnits:      cfg.SSHDUnits,
		AnswerDeadline: cfg.SocketAnswerDeadline,
		MaxConnections: cfg.SocketMaxConnections,
		IdleTimeout:    cfg.SocketIdleTimeout,
		Log:            log,
	})

	log.Info("ssoossh-nss started",
		"version", version.Version, "userdb_socket", userdbL.Addr().String(), "admin_socket", r.adminSocket,
		"cache_mode", cfg.CacheMode, "server", cfg.Server, "key_id", up.Status().KeyID)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { daemon.Run(ctx); return nil })
	g.Go(func() error { return srv.Serve(ctx, userdbL) })
	g.Go(func() error { return srv.ServeAdmin(ctx, adminL) })
	g.Go(func() error { reloadOn(ctx, hup, daemon, log); return nil })
	err = g.Wait()
	log.Info("ssoossh-nss stopped")
	return err
}

// nsswitchProblemMsg is what the daemon logs for each nsswitch.conf
// problem, and what `config check` reports.
const nsswitchProblemMsg = "nsswitch.conf will not consult this daemon; directory users will look missing"

// nsswitchProblems is the nsswitch.conf check the daemon makes at start.
// Not fatal: the daemon runs, and glibc simply never asks it.
func (r *RootCommand) nsswitchProblems() []string {
	return server.CheckNSSwitch(r.env.NSSwitch).Problems()
}

// openStore opens the configured cache.
func openStore(ctx context.Context, cfg *config.Config, path string) (cache.Store, error) {
	if cfg.CacheMode == config.CacheModeMemory {
		return cache.NewMemoryStore(), nil
	}
	return cache.OpenSQLite(ctx, path)
}

// userdbListener takes the activation socket, or binds listen when the
// process was not socket-activated.
func (r *RootCommand) userdbListener(listen string) (net.Listener, error) {
	l, err := r.env.Activation.Listener()
	switch {
	case err == nil:
		return l, nil
	case !errors.Is(err, server.ErrNotActivated):
		return nil, err
	case listen == "":
		return nil, errors.New("not socket-activated and no --listen given: start ssoossh-nss.socket, or pass --listen PATH")
	default:
		// 0666 like the socket unit's SocketMode: every process resolves
		// users.
		return server.Listen(listen, 0o666)
	}
}

// reloadOn calls Reset for every signal on hup until ctx ends.
func reloadOn(ctx context.Context, hup <-chan os.Signal, daemon *userdb.Daemon, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			log.Info("SIGHUP: reloading")
			daemon.Reset()
		}
	}
}
