// Package configcheck is the report `ssoosshd config check` and
// `ssoossh-nss config check` print: every problem a configuration has,
// found by the same code a start runs, in one pass rather than one restart
// at a time.
//
// The JSON shape is this package's own and is not part of the wire contract
// with pam_ssoossh (internal/apitypes): it is read by CI jobs and packaging
// scripts, and only its documented fields are stable.
package configcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// Severity says whether a finding fails the check.
type Severity string

// Severities.
const (
	// SeverityError fails the check: a start would refuse this
	// configuration.
	SeverityError Severity = "error"
	// SeverityWarning is advice: a start would run, and log it.
	SeverityWarning Severity = "warning"
)

// Kind says what sort of problem a finding is.
type Kind string

// Kinds.
const (
	// KindLoad is a file that could not be found, read or parsed, so
	// nothing after it was checked.
	KindLoad Kind = "load"
	// KindFilePermissions is a file whose mode or owner the daemon refuses.
	KindFilePermissions Kind = "file_permissions"
	// KindValidation is a setting a start refuses.
	KindValidation Kind = "validation"
	// KindUnknownKey is a key nothing reads: a typo, or a key from a newer
	// release.
	KindUnknownKey Kind = "unknown_key"
	// KindWarning is a warning a start logs.
	KindWarning Kind = "warning"
)

// Finding is one problem.
type Finding struct {
	// Severity is error or warning.
	Severity Severity `json:"severity"`
	// Kind is what sort of problem this is.
	Kind Kind `json:"kind"`
	// Section is the configuration block the problem is in, when known.
	Section string `json:"section,omitempty"`
	// Key is the dotted configuration key, for an unknown key.
	Key string `json:"key,omitempty"`
	// File is the file the problem was found in, when it is not the
	// report's own File.
	File string `json:"file,omitempty"`
	// Message says what is wrong, in the words a start would use.
	Message string `json:"message"`
	// Suggestion is the closest key that exists, for an unknown key.
	Suggestion string `json:"suggestion,omitempty"`
}

// Source is one effective setting and the layer that set it.
type Source struct {
	// Key is the dotted configuration key.
	Key string `json:"key"`
	// Value is what is in effect; a configured secret reads [redacted].
	Value string `json:"value"`
	// Origin is the layer the value came from: default, preset or file.
	Origin string `json:"origin"`
	// Secret marks a key whose value is never shown.
	Secret bool `json:"secret,omitempty"`
}

// Report is the outcome of one check.
type Report struct {
	// File is the configuration file checked; empty when none was found.
	File string `json:"file"`
	// Valid is true when there is no error-severity finding.
	Valid bool `json:"valid"`
	// Errors and Warnings count the findings of each severity.
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	// Findings are every problem, in the order they were found.
	Findings []Finding `json:"findings"`
	// Sources are the effective settings, when they were asked for.
	Sources []Source `json:"sources,omitempty"`
}

// ErrInvalid is what a failed check returns, so the command exits
// non-zero after printing its report.
var ErrInvalid = errors.New("configuration check failed")

// New starts an empty, valid report on file.
func New(file string) *Report {
	return &Report{File: file, Valid: true, Findings: []Finding{}}
}

// Add records one finding and keeps the counts and Valid in step.
func (r *Report) Add(f Finding) {
	r.Findings = append(r.Findings, f)
	if f.Severity == SeverityError {
		r.Errors++
		r.Valid = false
		return
	}
	r.Warnings++
}

// Err is nil for a valid report and wraps ErrInvalid otherwise.
func (r *Report) Err() error {
	if r.Valid {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalid, r.summary())
}

// summary counts the findings in words.
func (r *Report) summary() string {
	return fmt.Sprintf("%d %s, %d %s", r.Errors, plural(r.Errors, "error", "errors"),
		r.Warnings, plural(r.Warnings, "warning", "warnings"))
}

// plural picks the word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// WriteJSON prints the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText prints the report for a person: the file, one line per
// finding, the sources when present, and a closing verdict.
func (r *Report) WriteText(w io.Writer) error {
	tw := &textWriter{w: w}
	if r.File != "" {
		tw.printf("checked %s\n", r.File)
	}
	for _, f := range r.Findings {
		// One finding, one line: a message that wraps (a decoder's list
		// of errors) is folded onto it.
		tw.printf("%-8s %s\n", f.Severity, oneLine(f.line()))
	}
	if len(r.Sources) > 0 {
		tw.printf("\neffective settings (origin: default, preset or file)\n")
		width := 0
		for _, s := range r.Sources {
			width = max(width, len(s.Key))
		}
		for _, s := range r.Sources {
			tw.printf("  %-*s  %-7s  %s\n", width, s.Key, s.Origin, s.Value)
		}
	}
	verdict := "the configuration is valid"
	if !r.Valid {
		verdict = "the configuration is invalid"
	}
	tw.printf("%s: %s\n", r.summary(), verdict)
	return tw.err
}

// line renders a finding as the text after its severity.
func (f Finding) line() string {
	var b strings.Builder
	if f.Section != "" {
		b.WriteString(f.Section)
		b.WriteString(": ")
	}
	if f.Kind == KindUnknownKey {
		fmt.Fprintf(&b, "unknown key %s", f.Key)
		if f.File != "" {
			fmt.Fprintf(&b, " in %s", f.File)
		}
		b.WriteString(" has no effect")
		if f.Suggestion != "" {
			fmt.Fprintf(&b, "; did you mean %s?", f.Suggestion)
		}
		return b.String()
	}
	b.WriteString(f.Message)
	if f.File != "" && !strings.Contains(f.Message, f.File) {
		fmt.Fprintf(&b, " (%s)", f.File)
	}
	return b.String()
}

// oneLine folds every run of line breaks in s into one space.
func oneLine(s string) string {
	lines := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	return strings.Join(lines, " ")
}

// textWriter keeps the first write error, so WriteText checks once.
type textWriter struct {
	w   io.Writer
	err error
}

// printf writes unless an earlier write failed.
func (t *textWriter) printf(format string, args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintf(t.w, format, args...)
	}
}

// Joined is several independent errors as one. Validation returns it so a
// start can refuse with every reason at once and a check can list them
// separately; errors.Is and errors.As see through it as through
// errors.Join, and its text is the same.
type Joined struct {
	// Errs are the errors, none nil.
	Errs []error
}

// Join returns nil when every err is nil, and a *Joined of the others
// otherwise.
func Join(errs ...error) error {
	var out []error
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &Joined{Errs: out}
}

// Error implements error: one error per line, as errors.Join prints.
func (j *Joined) Error() string {
	parts := make([]string, len(j.Errs))
	for i, err := range j.Errs {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "\n")
}

// Unwrap gives errors.Is and errors.As every error.
func (j *Joined) Unwrap() []error { return j.Errs }

// Split returns the errors a *Joined in err's chain holds, or err alone
// when there is none. Only a Joined is split: an error wrapping two others
// with fmt.Errorf is one problem, not two.
func Split(err error) []error {
	if err == nil {
		return nil
	}
	var j *Joined
	if errors.As(err, &j) {
		return j.Errs
	}
	return []error{err}
}

// Recorder is a slog.Handler that keeps every record at warning level or
// above, rendered as one line, so a check can report what a start would
// log without parsing log output.
type Recorder struct {
	mu    *sync.Mutex
	lines *[]string
	attrs []slog.Attr
	group string
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{mu: &sync.Mutex{}, lines: &[]string{}}
}

// Lines returns what was recorded, in order.
func (r *Recorder) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), *r.lines...)
}

// Enabled implements slog.Handler: warnings and errors only.
func (r *Recorder) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

// Handle implements slog.Handler: the message, then key=value for each
// attribute.
func (r *Recorder) Handle(_ context.Context, rec slog.Record) error {
	var b strings.Builder
	b.WriteString(rec.Message)
	for _, a := range r.attrs {
		fmt.Fprintf(&b, " %s=%s", a.Key, a.Value.Resolve().String())
	}
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%s", r.prefixed(a.Key), a.Value.Resolve().String())
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.lines = append(*r.lines, b.String())
	return nil
}

// WithAttrs implements slog.Handler, sharing the record list. The keys
// take the group in force now, as slog's own handlers do.
func (r *Recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := *r
	out.attrs = append([]slog.Attr(nil), r.attrs...)
	for _, a := range attrs {
		out.attrs = append(out.attrs, slog.Attr{Key: r.prefixed(a.Key), Value: a.Value})
	}
	return &out
}

// prefixed puts the group in force before key.
func (r *Recorder) prefixed(key string) string {
	if r.group == "" {
		return key
	}
	return r.group + "." + key
}

// WithGroup implements slog.Handler, sharing the record list.
func (r *Recorder) WithGroup(name string) slog.Handler {
	out := *r
	if out.group != "" {
		name = out.group + "." + name
	}
	out.group = name
	return &out
}
