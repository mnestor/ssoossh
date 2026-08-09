package main

import (
	"fmt"
	"log"
	"log/syslog"
	"os"
)

// Logger is a minimal logging abstraction used by the PAM module.
type Logger interface {
	Debugf(format string, v ...interface{})
	Infof(format string, v ...interface{})
	Noticef(format string, v ...interface{})
	Warningf(format string, v ...interface{})
	Errorf(format string, v ...interface{})
	SetDebug(d string)
	Close() error
}

// syslogLogger implements Logger using *syslog.Writer.
type syslogLogger struct {
	w     *syslog.Writer
	debug string
}

func (s *syslogLogger) Debugf(format string, v ...interface{}) {
	switch s.debug {
	case "true":
		_ = s.w.Debug(fmt.Sprintf(format, v...))
	case "stdout":
		_, _ = fmt.Fprintf(os.Stdout, format, v...)
	}
}
func (s *syslogLogger) Infof(format string, v ...interface{}) {
	_ = s.w.Info(fmt.Sprintf(format, v...))
}
func (s *syslogLogger) Noticef(format string, v ...interface{}) {
	_ = s.w.Notice(fmt.Sprintf(format, v...))
}
func (s *syslogLogger) Warningf(format string, v ...interface{}) {
	_ = s.w.Warning(fmt.Sprintf(format, v...))
}
func (s *syslogLogger) Errorf(format string, v ...interface{}) {
	_ = s.w.Err(fmt.Sprintf(format, v...))
}
func (s *syslogLogger) Close() error      { return s.w.Close() }
func (s *syslogLogger) SetDebug(d string) { s.debug = d }

// fileLogger implements Logger using log.Logger (writes to file or stderr).
type fileLogger struct {
	l     *log.Logger
	f     *os.File // may be nil when logging to stderr
	debug string
}

func (f *fileLogger) Debugf(format string, v ...interface{}) {
	switch f.debug {
	case "true":
		f.l.Printf("DEBUG: "+format, v...)
	case "stdout":
		_, _ = fmt.Fprintf(os.Stdout, format, v...)
	}
}
func (f *fileLogger) Infof(format string, v ...interface{})    { f.l.Printf("INFO: "+format, v...) }
func (f *fileLogger) Noticef(format string, v ...interface{})  { f.l.Printf("NOTICE: "+format, v...) }
func (f *fileLogger) Warningf(format string, v ...interface{}) { f.l.Printf("WARN: "+format, v...) }
func (f *fileLogger) Errorf(format string, v ...interface{})   { f.l.Printf("ERROR: "+format, v...) }
func (s *fileLogger) SetDebug(d string)                        { s.debug = d }
func (f *fileLogger) Close() error {
	if f.f != nil {
		return f.f.Close()
	}
	return nil
}

// initLogger tries syslog, then /var/log/<tag>.log, then stderr.
// debug toggles verbosity but is only advisory here.
func initLogger(tag string) Logger {
	if w, err := syslog.New(syslog.LOG_AUTHPRIV, tag); err == nil {
		return &syslogLogger{w: w, debug: ""}
	}

	// Try opening a file under /var/log
	// path := "/var/log/" + tag + ".log"
	// if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
	// 	l := log.New(f, tag+" ", log.LstdFlags)
	// 	return &fileLogger{l: l, f: f}
	// }

	// Fallback to stderr
	l := log.New(os.Stderr, tag+" ", log.LstdFlags)
	return &fileLogger{l: l, f: nil, debug: ""}
}
