package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// watchCertificate re-reads the TLS certificate on SIGHUP, and on a timer
// when http.tls.reload_interval is set. It runs until ctx is canceled and is
// started by Run only when a certificate is actually configured.
//
// Two triggers because they cover different deployments. SIGHUP is the
// conventional "re-read your files" signal and is what an operator, a
// certbot --deploy-hook, or a systemd ExecReload= reaches for; it is free to
// claim here because the process's own signal handling
// (go-kit/signals.SignalContext) installs handlers for SIGINT and SIGTERM
// only. The timer covers deployments where nothing signals the process at
// all — a Kubernetes secret remount being the motivating case, since it
// replaces a directory symlink rather than rewriting the files in place, so
// a filesystem watch on the configured paths would never fire.
//
// Note this registers its own signal channel rather than asking
// signals.SignalContext for another context: that helper closes a
// package-level channel to enforce one handler per process and panics on a
// second call.
func (s *Server) watchCertificate(ctx context.Context) {
	defer s.wg.Done()

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	var tick <-chan time.Time
	if interval := s.config.HTTP.TLS.ReloadInterval; interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C

		slog.InfoContext(ctx, "TLS certificate reload watcher started",
			slog.Duration("interval", interval),
		)
	} else {
		slog.InfoContext(ctx, "TLS certificate reload watcher started, SIGHUP only")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sighup:
			s.reloadCertificate(ctx, "sighup")
		case <-tick:
			s.reloadCertificate(ctx, "interval")
		}
	}
}

// reloadCertificate re-reads the configured certificate files and swaps the
// result in.
//
// A failure is logged and otherwise ignored: the certificate already in hand
// keeps serving. The usual causes are transient states of files another
// process is midway through rewriting — a certificate written before its
// key, a truncated file, a file briefly absent — and dropping TLS over one
// of those would turn a successful renewal into an outage. A genuinely
// broken pair keeps being reported on every subsequent trigger.
//
// The interval trigger means this runs even when nothing changed, which is
// a wasted read of two small files and simpler than tracking mtimes.
func (s *Server) reloadCertificate(ctx context.Context, trigger string) {
	if err := s.certSource.Reload(); err != nil {
		slog.WarnContext(ctx, "TLS certificate reload failed, keeping the previous certificate",
			slog.String("trigger", trigger),
			slog.Any("error", err),
		)

		return
	}

	slog.InfoContext(ctx, "TLS certificate reloaded",
		slog.String("trigger", trigger),
	)
}
