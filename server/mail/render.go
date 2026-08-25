// Package mail renders notification events into messages and delivers them
// over SMTP.
//
// It is split from server/notify (which owns the catalogue of what can be
// sent) and from server/service (which owns who wants to receive it) so
// that neither has to know about templates or SMTP. Nothing here touches
// the database or the request path.
package mail

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/resources"
)

// embeddedDir is where the built-in template set lives inside
// resources.FS, so a default install sends useful mail with no files on
// disk at all. The files sit under server/resources rather than beside
// this package because every embedded static resource the server ships is
// collected there.
const embeddedDir = "mail"

// Template parts. Each registered notify.Kind has one file per part, named
// "<kind>.<part>.tmpl" — that flat naming is what makes an override
// discoverable: an operator lists the directory and sees exactly which
// files they may drop a replacement for.
const (
	partSubject = "subject"
	partText    = "txt"
	partHTML    = "html"
)

// templateParts is every part, in the order NewRenderer loads them.
var templateParts = []string{partSubject, partText, partHTML}

// Rendered is one notification turned into the three pieces a message
// needs. Both bodies are always produced: the HTML one is sent as an
// alternative, so a text-only reader still gets the whole content rather
// than a "this message has no content" placeholder.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// Renderer turns a notification payload into a Rendered message, using the
// embedded templates with local overrides layered on top.
//
// Everything is parsed and test-executed once, at construction. A template
// problem is then a startup failure the operator who caused it is watching
// for, rather than a delivery failure weeks later that looks exactly like
// a notification nobody triggered.
type Renderer struct {
	subjectPrefix string
	kinds         map[notify.Kind]*kindTemplates
	overrides     []string
}

// kindTemplates holds the three parsed templates for one kind. The subject
// and text parts use text/template and the HTML part html/template, so the
// escaping matches the medium: a service account containing markup must
// not become markup in the HTML body, and must not become entities in the
// plain-text one.
type kindTemplates struct {
	subject *texttemplate.Template
	text    *texttemplate.Template
	html    *htmltemplate.Template
}

// NewRenderer parses the embedded templates, layers any overrides found in
// templateDir on top, and prepends subjectPrefix to every rendered subject.
//
// templateDir may be empty, which means embedded templates only.
func NewRenderer(templateDir, subjectPrefix string) (*Renderer, error) {
	r := &Renderer{
		subjectPrefix: subjectPrefix,
		kinds:         make(map[notify.Kind]*kindTemplates),
	}

	overrides, err := loadOverrides(templateDir)
	if err != nil {
		return nil, err
	}

	for _, def := range notify.Definitions() {
		parsed := &kindTemplates{}
		for _, part := range templateParts {
			name := TemplateName(def.Kind, part)

			body, overridden, err := templateBody(name, overrides)
			if err != nil {
				return nil, err
			}
			if overridden {
				r.overrides = append(r.overrides, name)
			}

			if err := parsed.parse(part, name, body); err != nil {
				return nil, err
			}
		}

		// Execute once against a zero payload. text/template errors on a
		// field the data does not have, so this is what turns a typo'd
		// {{ .ServiceAcount }} into a startup failure.
		if err := parsed.checkExecutable(def.NewPayload()); err != nil {
			return nil, fmt.Errorf("notification template for %s: %w", def.Kind, err)
		}

		r.kinds[def.Kind] = parsed
	}

	// Anything left is a file whose name matches no registered kind or
	// part — almost always a misspelling, and silently ignoring it means
	// the operator's edit appears to do nothing at all.
	if len(overrides) > 0 {
		names := make([]string, 0, len(overrides))
		for name := range overrides {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf(
			"mail.template_dir %q contains template files matching no notification: %s (expected <kind>.{subject,txt,html}.tmpl; see docs/email-notifications.md)",
			templateDir, strings.Join(names, ", "))
	}

	sort.Strings(r.overrides)
	return r, nil
}

// TemplateName is the file name for one part of one kind's template. The
// single authority for the naming convention — the docs, the loader, and
// the override check all read it from here.
func TemplateName(kind notify.Kind, part string) string {
	return fmt.Sprintf("%s.%s.tmpl", kind, part)
}

// Overrides lists the override files actually in use, sorted. Logged at
// startup so an operator can see that the file they wrote is the one being
// rendered.
func (r *Renderer) Overrides() []string {
	out := make([]string, len(r.overrides))
	copy(out, r.overrides)
	return out
}

// Render produces the message for kind from data, which must be the
// payload type the kind's Definition builds.
func (r *Renderer) Render(kind notify.Kind, data any) (Rendered, error) {
	templates, ok := r.kinds[kind]
	if !ok {
		return Rendered{}, fmt.Errorf("no template for notification kind %q", kind)
	}

	subject, err := executeText(templates.subject, data)
	if err != nil {
		return Rendered{}, fmt.Errorf("failed to render the %s subject: %w", kind, err)
	}
	text, err := executeText(templates.text, data)
	if err != nil {
		return Rendered{}, fmt.Errorf("failed to render the %s text body: %w", kind, err)
	}

	var htmlBuf bytes.Buffer
	if err := templates.html.Execute(&htmlBuf, data); err != nil {
		return Rendered{}, fmt.Errorf("failed to render the %s html body: %w", kind, err)
	}

	return Rendered{
		Subject: r.subjectPrefix + foldSubject(subject),
		Text:    text,
		HTML:    htmlBuf.String(),
	}, nil
}

// parse compiles one part into the right template engine for its medium.
func (k *kindTemplates) parse(part, name, body string) error {
	var err error
	switch part {
	case partSubject:
		k.subject, err = texttemplate.New(name).Funcs(textFuncs()).Option("missingkey=zero").Parse(body)
	case partText:
		k.text, err = texttemplate.New(name).Funcs(textFuncs()).Option("missingkey=zero").Parse(body)
	case partHTML:
		k.html, err = htmltemplate.New(name).Funcs(htmlFuncs()).Option("missingkey=zero").Parse(body)
	default:
		// not covered: part comes from templateParts, which lists exactly
		// the three cases above.
		return fmt.Errorf("unknown template part %q", part)
	}
	if err != nil {
		return fmt.Errorf("failed to parse notification template %q: %w", name, err)
	}
	return nil
}

// checkExecutable runs all three parts against payload, surfacing a
// template that names a field the payload does not have.
func (k *kindTemplates) checkExecutable(payload any) error {
	if _, err := executeText(k.subject, payload); err != nil {
		return fmt.Errorf("subject: %w", err)
	}
	if _, err := executeText(k.text, payload); err != nil {
		return fmt.Errorf("text body: %w", err)
	}
	if err := k.html.Execute(&bytes.Buffer{}, payload); err != nil {
		return fmt.Errorf("html body: %w", err)
	}
	return nil
}

// executeText renders a text/template into a string.
func executeText(t *texttemplate.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// loadOverrides reads every *.tmpl in dir. The map is consumed by name as
// templates are built, so whatever is left over at the end is a file that
// matched nothing.
func loadOverrides(dir string) (map[string]string, error) {
	overrides := map[string]string{}
	if dir == "" {
		return overrides, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read mail.template_dir %q: %w", dir, err)
	}

	for _, entry := range entries {
		// Non-template files are ignored rather than rejected: an operator
		// keeping a README or an editor backup beside their overrides is
		// not making a mistake.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tmpl") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			// not covered: the entry was listed by ReadDir moments ago, so
			// reaching this needs the file to become unreadable in between.
			// A fixture with mode 0000 would not do it either — the suite
			// runs as root in CI, where the mode does not apply.
			return nil, fmt.Errorf("failed to read notification template %q: %w", entry.Name(), err)
		}
		overrides[entry.Name()] = string(body)
	}
	return overrides, nil
}

// templateBody returns the body for name, preferring an override and
// consuming it from the map so unmatched leftovers can be reported.
func templateBody(name string, overrides map[string]string) (body string, overridden bool, err error) {
	if body, ok := overrides[name]; ok {
		delete(overrides, name)
		return body, true, nil
	}

	data, err := fs.ReadFile(resources.FS, embeddedDir+"/"+name)
	if err != nil {
		// not covered: the embedded set and the registry ship together, and
		// TestNewRenderer_shouldRenderEveryRegisteredKind fails the build if
		// a kind is added without its three templates. Reaching this needs a
		// binary whose embedded FS does not match its own registry.
		return "", false, fmt.Errorf("missing embedded notification template %q: %w", name, err)
	}
	return string(data), false, nil
}

// foldSubject collapses a rendered subject onto one line. A subject is a
// single header field, so a line break in one is either a mistake in a
// template or an attempt to inject headers through interpolated data;
// folding is the answer to both, and keeps the content visible rather than
// dropping it.
func foldSubject(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// textFuncs are the helpers available to subject and text templates. They
// exist so payloads can stay typed (a time.Time is a time.Time) while
// templates still render something a person wants to read.
func textFuncs() texttemplate.FuncMap {
	return texttemplate.FuncMap{
		"datetime": formatDateTime,
		"date":     formatDate,
		"approx":   approxDuration,
		"until":    func(t time.Time) string { return approxDuration(time.Until(t)) },
		"join":     func(items []string, sep string) string { return strings.Join(items, sep) },
	}
}

// htmlFuncs mirrors textFuncs for the HTML template engine.
func htmlFuncs() htmltemplate.FuncMap {
	return htmltemplate.FuncMap(textFuncs())
}

// formatDateTime renders a timestamp for a human, in the server's local
// zone with the zone named — a recipient reading "15:04" with no zone has
// to guess, and these messages are about credential windows.
func formatDateTime(t time.Time) string {
	if t.IsZero() {
		return "not set"
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

// formatDate renders just the day.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "not set"
	}
	return t.Local().Format("2006-01-02")
}

// approxDuration renders a span in the largest unit that still says
// something useful. time.Duration's own String prints "2159h58m12.4s" for
// 90 days, which answers the question in a form nobody reads at a glance.
//
// Truncated rather than rounded, matching `ssoossh service enroll`'s
// terminal output: for a credential's remaining life, understating is the
// safe direction.
func approxDuration(d time.Duration) string {
	if d <= 0 {
		return "already elapsed"
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
}
