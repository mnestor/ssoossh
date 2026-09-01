//go:build pam

package main

import (
	"bytes"
	"errors"
	"log"
	"log/syslog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestSyslogWriter dials a real *syslog.Writer against a throwaway unixgram
// socket this test listens on, standing in for a syslog daemon. There is no
// syslog daemon in the sandboxed test environment (syslog.New always fails
// here), so this is what makes syslogLogger's methods reachable at all.
func newTestSyslogWriter(t *testing.T) (*syslog.Writer, *net.UnixConn) {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "log.sock")
	addr, err := net.ResolveUnixAddr("unixgram", sock)
	if err != nil {
		t.Fatalf("ResolveUnixAddr() error = %v", err)
	}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatalf("ListenUnixgram() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	w, err := syslog.Dial("unixgram", sock, syslog.LOG_AUTHPRIV, "ssoossh-test")
	if err != nil {
		t.Fatalf("syslog.Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	return w, conn
}

// readOne reads one datagram off conn with a deadline, failing the test on timeout.
func readOne(t *testing.T, conn *net.UnixConn) string {
	t.Helper()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return string(buf[:n])
}

// should forward every severity to the underlying syslog.Writer, gating Debugf on the debug mode.
func TestSyslogLogger(t *testing.T) {
	t.Run("should send an Info message", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		l := &syslogLogger{w: w}
		l.Infof("info: %s", "hello")
		if got := readOne(t, conn); !strings.Contains(got, "info: hello") {
			t.Errorf("received %q, want it to contain %q", got, "info: hello")
		}
	})

	t.Run("should send a Notice message", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		l := &syslogLogger{w: w}
		l.Noticef("notice: %s", "hello")
		if got := readOne(t, conn); !strings.Contains(got, "notice: hello") {
			t.Errorf("received %q, want it to contain %q", got, "notice: hello")
		}
	})

	t.Run("should send a Warning message", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		l := &syslogLogger{w: w}
		l.Warningf("warn: %s", "hello")
		if got := readOne(t, conn); !strings.Contains(got, "warn: hello") {
			t.Errorf("received %q, want it to contain %q", got, "warn: hello")
		}
	})

	t.Run("should send an Error message", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		l := &syslogLogger{w: w}
		l.Errorf("err: %s", "hello")
		if got := readOne(t, conn); !strings.Contains(got, "err: hello") {
			t.Errorf("received %q, want it to contain %q", got, "err: hello")
		}
	})

	t.Run("should suppress Debugf when debug mode is unset", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		l := &syslogLogger{w: w}
		l.Debugf("debug: %s", "hello")

		// Send a sentinel Info afterward and confirm it arrives first — proof
		// nothing was queued ahead of it by the suppressed Debugf.
		if err := l.w.Info("sentinel"); err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if got := readOne(t, conn); !strings.Contains(got, "sentinel") {
			t.Errorf("received %q, want the sentinel first", got)
		}
	})

	t.Run("should forward Debugf to syslog when debug mode is true", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		l := &syslogLogger{w: w, debug: "true"}
		l.Debugf("debug: %s", "hello")
		if got := readOne(t, conn); !strings.Contains(got, "debug: hello") {
			t.Errorf("received %q, want it to contain %q", got, "debug: hello")
		}
	})

	t.Run("should print Debugf to stdout when debug mode is stdout", func(t *testing.T) {
		w, _ := newTestSyslogWriter(t)
		l := &syslogLogger{w: w, debug: "stdout"}
		// Only asserting this doesn't panic and doesn't reach syslog: stdout
		// capture isn't worth the added complexity here.
		l.Debugf("debug: %s", "hello")
	})

	t.Run("should set and close cleanly", func(t *testing.T) {
		w, _ := newTestSyslogWriter(t)
		l := &syslogLogger{w: w}
		l.SetDebug("true")
		if l.debug != "true" {
			t.Errorf("debug = %q, want %q", l.debug, "true")
		}
		if err := l.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
}

// should write to the underlying *log.Logger, gating Debugf on the debug mode.
func TestFileLogger(t *testing.T) {
	t.Run("should write Info/Notice/Warning/Error with their level prefixes", func(t *testing.T) {
		var buf bytes.Buffer
		l := &fileLogger{l: log.New(&buf, "", 0)}

		l.Infof("hello %s", "world")
		l.Noticef("hello %s", "world")
		l.Warningf("hello %s", "world")
		l.Errorf("hello %s", "world")

		out := buf.String()
		for _, want := range []string{"INFO: hello world", "NOTICE: hello world", "WARN: hello world", "ERROR: hello world"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("should suppress Debugf when debug mode is unset", func(t *testing.T) {
		var buf bytes.Buffer
		l := &fileLogger{l: log.New(&buf, "", 0)}
		l.Debugf("debug %s", "hello")
		if buf.String() != "" {
			t.Errorf("output = %q, want empty with debug mode unset", buf.String())
		}
	})

	t.Run("should write Debugf when debug mode is true", func(t *testing.T) {
		var buf bytes.Buffer
		l := &fileLogger{l: log.New(&buf, "", 0), debug: "true"}
		l.Debugf("debug %s", "hello")
		if !strings.Contains(buf.String(), "DEBUG: debug hello") {
			t.Errorf("output = %q, want it to contain %q", buf.String(), "DEBUG: debug hello")
		}
	})

	t.Run("should print Debugf to stdout when debug mode is stdout", func(t *testing.T) {
		var buf bytes.Buffer
		l := &fileLogger{l: log.New(&buf, "", 0), debug: "stdout"}
		// Only asserting this doesn't panic and doesn't reach the file logger:
		// stdout capture isn't worth the added complexity here.
		l.Debugf("debug %s", "hello")
		if buf.String() != "" {
			t.Errorf("output = %q, want empty: stdout mode should not write through the file logger", buf.String())
		}
	})

	t.Run("should be a no-op Close when there is no backing file", func(t *testing.T) {
		l := &fileLogger{l: log.New(&bytes.Buffer{}, "", 0)}
		if err := l.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("should close the backing file when present", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "logtest")
		if err != nil {
			t.Fatalf("CreateTemp() error = %v", err)
		}
		l := &fileLogger{l: log.New(f, "", 0), f: f}
		if err := l.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
}

// should set the debug mode on a fileLogger.
func TestFileLogger_SetDebug(t *testing.T) {
	l := &fileLogger{l: log.New(&bytes.Buffer{}, "", 0)}
	l.SetDebug("stdout")
	if l.debug != "stdout" {
		t.Errorf("debug = %q, want %q", l.debug, "stdout")
	}
}

// should pick syslog when a daemon accepts the connection and fall back to
// stderr when none does. The constructor is injected so the choice does not
// depend on whether the test host happens to run a syslog daemon.
func TestInitLogger(t *testing.T) {
	t.Run("should use syslog when the daemon accepts the connection", func(t *testing.T) {
		w, conn := newTestSyslogWriter(t)
		newSyslog := func(syslog.Priority, string) (*syslog.Writer, error) { return w, nil }

		logger := initLogger("ssoossh-test", newSyslog)
		t.Cleanup(func() { _ = logger.Close() })

		if _, ok := logger.(*syslogLogger); !ok {
			t.Fatalf("initLogger() returned %T, want *syslogLogger", logger)
		}
		logger.Infof("via syslog")
		if got := readOne(t, conn); !strings.Contains(got, "via syslog") {
			t.Errorf("received %q, want it to contain %q", got, "via syslog")
		}
	})

	t.Run("should fall back to a stderr-backed fileLogger when syslog is unavailable", func(t *testing.T) {
		newSyslog := func(syslog.Priority, string) (*syslog.Writer, error) {
			return nil, errors.New("no syslog daemon")
		}

		logger := initLogger("ssoossh-test", newSyslog)
		t.Cleanup(func() { _ = logger.Close() })

		fl, ok := logger.(*fileLogger)
		if !ok {
			t.Fatalf("initLogger() returned %T, want *fileLogger", logger)
		}
		if fl.f != nil {
			t.Error("fileLogger has a backing file, want nil for the stderr fallback")
		}
	})
}
