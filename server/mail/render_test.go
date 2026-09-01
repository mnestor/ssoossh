package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/notify"
)

// A sample payload per kind, filled in enough that a template referencing
// any documented field produces something visible.
func samplePayload(t *testing.T, kind notify.Kind) any {
	t.Helper()

	expires := time.Now().Add(90 * 24 * time.Hour)
	switch kind {
	case notify.KindServiceEnrollmentCreated:
		return &notify.ServiceEnrollmentCreated{
			ServiceAccount:       "deploy-bot",
			RequestID:            "req-123",
			EnrollmentID:         "enr-456",
			KeyID:                "alice@example.com",
			Principals:           []string{"deploy-bot"},
			PublicKeyFingerprint: "SHA256:abcdef",
			PublicKeyType:        "ssh-ed25519",
			Extensions:           []string{"permit-pty"},
			RequestSourceIP:      "198.51.100.7",
			ApprovedAt:           time.Now(),
			ApprovedByUsername:   "alice",
			CodeExpiresAt:        expires,
			CertificateLifetime:  8 * time.Hour,
			ServerURL:            "https://ssoossh.example.com",
		}
	case notify.KindServiceEnrollmentRedeemed:
		return &notify.ServiceEnrollmentRedeemed{
			ServiceAccount:       "deploy-bot",
			RequestID:            "req-123",
			EnrollmentID:         "enr-456",
			RetrievalID:          "ret-789",
			SourceIP:             "198.51.100.7",
			RetrievedAt:          time.Now(),
			CertificateSerial:    42,
			CertificateExpiresAt: time.Now().Add(8 * time.Hour),
			KeyID:                "alice@example.com",
			Principals:           []string{"deploy-bot"},
			Succeeded:            true,
			FirstRedemption:      true,
			CodeExpiresAt:        expires,
			ServerURL:            "https://ssoossh.example.com",
		}
	case notify.KindServiceEnrollmentExpiring:
		return &notify.ServiceEnrollmentExpiring{
			ServiceAccount:       "deploy-bot",
			RequestID:            "req-123",
			EnrollmentID:         "enr-456",
			KeyID:                "alice@example.com",
			Principals:           []string{"deploy-bot"},
			PublicKeyFingerprint: "SHA256:abcdef",
			PublicKeyType:        "ssh-ed25519",
			FirstRedeemedAt:      time.Now().Add(-30 * 24 * time.Hour),
			CodeExpiresAt:        time.Now().Add(7 * 24 * time.Hour),
			ServerURL:            "https://ssoossh.example.com",
		}
	case notify.KindServiceEnrollmentExpiredAttempt:
		return &notify.ServiceEnrollmentExpiredAttempt{
			ServiceAccount:       "deploy-bot",
			RequestID:            "req-123",
			EnrollmentID:         "enr-456",
			KeyID:                "alice@example.com",
			Principals:           []string{"deploy-bot"},
			PublicKeyFingerprint: "SHA256:abcdef",
			PublicKeyType:        "ssh-ed25519",
			SourceIP:             "198.51.100.7",
			AttemptedAt:          time.Now(),
			CodeExpiredAt:        time.Now().Add(-24 * time.Hour),
			ServerURL:            "https://ssoossh.example.com",
		}
	case notify.KindUserCertificateIssued, notify.KindPAMCertificateIssued:
		// One sample for both kinds because they render one payload type.
		// CertificateType is the only field that distinguishes them, so it
		// is filled from the kind rather than hardcoded.
		certType := "user"
		if kind == notify.KindPAMCertificateIssued {
			certType = "pam"
		}
		return &notify.CertificateIssued{
			CertificateType:      certType,
			RequestID:            "req-123",
			KeyID:                "alice@example.com",
			Principals:           []string{"alice", "deploy"},
			Serial:               42,
			PublicKeyFingerprint: "SHA256:abcdef",
			LocalUsername:        "alice",
			LocalHostname:        "workstation",
			SourceIP:             "198.51.100.7",
			IssuedAt:             time.Now(),
			ExpiresAt:            time.Now().Add(8 * time.Hour),
			Extensions:           []string{"permit-pty"},
			ForceCommand:         "/usr/bin/true",
			SourceAddresses:      []string{"198.51.100.0/24"},
			ServerURL:            "https://ssoossh.example.com",
		}
	default:
		t.Fatalf("no sample payload for kind %q", kind)
		return nil
	}
}

// The embedded set has to be complete: a kind registered without its three
// templates is a notification that can be enabled in the UI and then fails
// at delivery, which is the failure mode furthest from the mistake.
func TestNewRenderer_shouldRenderEveryRegisteredKind(t *testing.T) {
	r, err := NewRenderer("", "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	for _, def := range notify.Definitions() {
		t.Run(string(def.Kind), func(t *testing.T) {
			out, err := r.Render(def.Kind, samplePayload(t, def.Kind))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.TrimSpace(out.Subject) == "" {
				t.Error("subject is empty")
			}
			if strings.TrimSpace(out.Text) == "" {
				t.Error("text body is empty")
			}
			if strings.TrimSpace(out.HTML) == "" {
				t.Error("html body is empty")
			}
		})
	}
}

// The zero payload is what a template sees when an event was published
// with fields the emitting code did not fill in. It must still render:
// text/template fails hard on a field that does not exist, so this is what
// catches a typo'd {{ .ServiceAcount }} at startup rather than at send.
func TestNewRenderer_shouldRenderEveryKindFromAZeroPayload(t *testing.T) {
	r, err := NewRenderer("", "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	for _, def := range notify.Definitions() {
		t.Run(string(def.Kind), func(t *testing.T) {
			if _, err := r.Render(def.Kind, def.NewPayload()); err != nil {
				t.Errorf("Render on a zero payload: %v", err)
			}
		})
	}
}

func TestRender_shouldRejectAnUnregisteredKind(t *testing.T) {
	r, err := NewRenderer("", "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	if _, err := r.Render("nope", struct{}{}); err == nil {
		t.Error("Render accepted an unregistered kind")
	}
}

// The enrollment code is the one thing that must never reach a mailbox.
// Asserting on the rendered output rather than on the payload struct is
// deliberate: this is the check that survives someone adding a field.
func TestRender_shouldNotCarryAnEnrollmentCode(t *testing.T) {
	r, err := NewRenderer("", "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	for _, def := range notify.Definitions() {
		out, err := r.Render(def.Kind, samplePayload(t, def.Kind))
		if err != nil {
			t.Fatalf("Render %s: %v", def.Kind, err)
		}
		for _, body := range []string{out.Subject, out.Text, out.HTML} {
			lowered := strings.ToLower(body)
			if strings.Contains(lowered, "enrollment code is") {
				t.Errorf("%s renders an enrollment code", def.Kind)
			}
		}
	}
}

func TestRender_shouldApplyTheSubjectPrefix(t *testing.T) {
	r, err := NewRenderer("", "[ssoossh] ")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	out, err := r.Render(notify.KindServiceEnrollmentCreated, samplePayload(t, notify.KindServiceEnrollmentCreated))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(out.Subject, "[ssoossh] ") {
		t.Errorf("subject %q does not carry the prefix", out.Subject)
	}
}

// A subject is one header field. A newline reaching it would let anything
// interpolated into a template inject headers of its own, so it is folded
// away rather than trusted.
func TestRender_shouldCollapseNewlinesInTheSubject(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.subject.tmpl", "one\nBcc: attacker@example.com\r\ntwo")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	out, err := r.Render(notify.KindServiceEnrollmentCreated, samplePayload(t, notify.KindServiceEnrollmentCreated))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if strings.ContainsAny(out.Subject, "\r\n") {
		t.Errorf("subject %q still contains a line break", out.Subject)
	}
	if !strings.Contains(out.Subject, "Bcc: attacker@example.com") {
		t.Errorf("subject %q lost the folded content entirely", out.Subject)
	}
}

func TestNewRenderer_shouldPreferALocalOverride(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "local override for {{ .ServiceAccount }}")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	out, err := r.Render(notify.KindServiceEnrollmentCreated, samplePayload(t, notify.KindServiceEnrollmentCreated))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out.Text != "local override for deploy-bot" {
		t.Errorf("text = %q, want the local override", out.Text)
	}
}

// Overriding one part of one message must not mean vendoring the rest.
func TestNewRenderer_shouldFallBackToTheEmbeddedTemplateForUnoverriddenParts(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "local override")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	out, err := r.Render(notify.KindServiceEnrollmentCreated, samplePayload(t, notify.KindServiceEnrollmentCreated))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.TrimSpace(out.HTML) == "" {
		t.Error("html body did not fall back to the embedded template")
	}
	if !strings.Contains(out.Subject, "deploy-bot") && strings.TrimSpace(out.Subject) == "" {
		t.Error("subject did not fall back to the embedded template")
	}
}

func TestNewRenderer_shouldReportWhichTemplatesAreOverridden(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "local override")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	overrides := r.Overrides()
	if len(overrides) != 1 || overrides[0] != "service_enrollment_created.txt.tmpl" {
		t.Errorf("Overrides = %v, want the one overridden file", overrides)
	}
}

// A broken override is a startup failure, not a delivery failure: the
// operator who just edited it is watching the server come up, and nobody is
// watching the mail that silently stops arriving three weeks later.
func TestNewRenderer_shouldFailOnAnUnparseableOverride(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "{{ .ServiceAccount ")

	if _, err := NewRenderer(dir, ""); err == nil {
		t.Error("NewRenderer accepted an unparseable override")
	}
}

// An override naming a field that does not exist parses fine and fails at
// execution, which without this check means at send time.
func TestNewRenderer_shouldFailOnAnOverrideNamingAnUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "{{ .NoSuchField }}")

	if _, err := NewRenderer(dir, ""); err == nil {
		t.Error("NewRenderer accepted an override naming an unknown field")
	}
}

// A file in the override directory that matches no registered kind is
// almost always a typo in the filename, and silently ignoring it means the
// operator's edit appears to do nothing.
func TestNewRenderer_shouldFailOnAnUnrecognizedOverrideFile(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrolment_created.txt.tmpl", "misspelled")

	_, err := NewRenderer(dir, "")
	if err == nil {
		t.Fatal("NewRenderer accepted an unrecognized override file")
	}
	if !strings.Contains(err.Error(), "service_enrolment_created.txt.tmpl") {
		t.Errorf("error %q does not name the offending file", err)
	}
}

func TestNewRenderer_shouldIgnoreNonTemplateFilesInTheOverrideDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewRenderer(dir, ""); err != nil {
		t.Errorf("NewRenderer: %v", err)
	}
}

// The HTML body goes through html/template so an account name carrying
// markup cannot become markup; the text body must not be escaped, or every
// apostrophe in it turns into an entity.
func TestRender_shouldEscapeHTMLButNotText(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.html.tmpl", "<p>{{ .ServiceAccount }}</p>")
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "{{ .ServiceAccount }}")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	payload := samplePayload(t, notify.KindServiceEnrollmentCreated).(*notify.ServiceEnrollmentCreated)
	payload.ServiceAccount = `<script>alert("x")</script>`

	out, err := r.Render(notify.KindServiceEnrollmentCreated, payload)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out.HTML, "<script>") {
		t.Errorf("html body %q was not escaped", out.HTML)
	}
	if !strings.Contains(out.Text, `<script>alert("x")</script>`) {
		t.Errorf("text body %q was escaped", out.Text)
	}
}

func TestTemplateFuncs_shouldFormatTheValuesTemplatesActuallyUse(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl",
		`{{ datetime .ApprovedAt }}|{{ approx .CertificateLifetime }}|{{ join .Principals ", " }}|{{ if .NoTouchRequired }}yes{{ else }}no{{ end }}`)

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	payload := &notify.ServiceEnrollmentCreated{
		ApprovedAt:          time.Date(2026, 8, 24, 15, 4, 5, 0, time.UTC),
		CertificateLifetime: 8 * time.Hour,
		Principals:          []string{"a", "b"},
	}
	out, err := r.Render(notify.KindServiceEnrollmentCreated, payload)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	parts := strings.Split(out.Text, "|")
	if len(parts) != 4 {
		t.Fatalf("rendered %q, want four parts", out.Text)
	}
	if !strings.Contains(parts[0], "2026-08-24") {
		t.Errorf("datetime = %q", parts[0])
	}
	if parts[1] != "8 hours" {
		t.Errorf("approx = %q, want %q", parts[1], "8 hours")
	}
	if parts[2] != "a, b" {
		t.Errorf("join = %q, want %q", parts[2], "a, b")
	}
	if parts[3] != "no" {
		t.Errorf("bool branch = %q, want %q", parts[3], "no")
	}
}

// A zero time is what an unset optional timestamp renders as, and
// "0001-01-01" in a mailbox is worse than saying nothing.
func TestTemplateFuncs_shouldRenderAZeroTimeAsUnset(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "{{ datetime .ApprovedAt }}")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	out, err := r.Render(notify.KindServiceEnrollmentCreated, &notify.ServiceEnrollmentCreated{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out.Text, "0001") {
		t.Errorf("rendered %q for a zero time", out.Text)
	}
}

func TestApproxDuration_shouldPickAUsefulUnit(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "already elapsed"},
		{0, "already elapsed"},
		{45 * time.Second, "45 seconds"},
		{30 * time.Minute, "30 minutes"},
		{8 * time.Hour, "8 hours"},
		{90 * 24 * time.Hour, "90 days"},
	}

	for _, tt := range tests {
		if got := approxDuration(tt.in); got != tt.want {
			t.Errorf("approxDuration(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// writeTemplate drops a template override into dir.
func writeTemplate(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// The `date` helper is documented for template authors even though no
// built-in template uses it, so it has to actually work.
func TestTemplateFuncs_shouldRenderADateOnDemand(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.txt.tmpl", "{{ date .CodeExpiresAt }}|{{ date .ApprovedAt }}")

	r, err := NewRenderer(dir, "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	out, err := r.Render(notify.KindServiceEnrollmentCreated, &notify.ServiceEnrollmentCreated{
		CodeExpiresAt: time.Date(2026, 11, 22, 15, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered, unset, _ := strings.Cut(out.Text, "|")
	if !strings.HasPrefix(rendered, "2026-11-2") {
		t.Errorf("date = %q, want the day of CodeExpiresAt", rendered)
	}
	if unset != "not set" {
		t.Errorf("date of a zero time = %q, want %q", unset, "not set")
	}
}

// An override directory the operator cannot read is a startup failure, not
// a silent fallback to the embedded set: they wrote those files expecting
// them to be used.
func TestNewRenderer_shouldFailWhenTheOverrideDirectoryCannotBeRead(t *testing.T) {
	if _, err := NewRenderer(filepath.Join(t.TempDir(), "absent"), ""); err == nil {
		t.Error("NewRenderer accepted a template directory that does not exist")
	}
}

// Render is handed whatever the delivery handler decoded, so a kind whose
// payload constructor and templates disagree surfaces here rather than as
// a blank message. Each part is checked separately: Render returns at the
// first failure, so a single case would only ever reach the subject.
func TestRender_shouldReportAPayloadTheTemplateCannotRead(t *testing.T) {
	tests := []struct {
		name      string
		templates map[string]string
		wantPart  string
	}{
		{
			name:      "subject",
			templates: map[string]string{"service_enrollment_created.subject.tmpl": "{{ .ServiceAccount }}"},
			wantPart:  "subject",
		},
		{
			name: "text body",
			templates: map[string]string{
				"service_enrollment_created.subject.tmpl": "static",
				"service_enrollment_created.txt.tmpl":     "{{ .ServiceAccount }}",
			},
			wantPart: "text body",
		},
		{
			name: "html body",
			templates: map[string]string{
				"service_enrollment_created.subject.tmpl": "static",
				"service_enrollment_created.txt.tmpl":     "static",
				"service_enrollment_created.html.tmpl":    "{{ .ServiceAccount }}",
			},
			wantPart: "html body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.templates {
				writeTemplate(t, dir, name, body)
			}

			r, err := NewRenderer(dir, "")
			if err != nil {
				t.Fatalf("NewRenderer: %v", err)
			}

			// A struct with none of the payload's fields, which is what a
			// mismatched NewPayload would hand over.
			err = func() error {
				_, err := r.Render(notify.KindServiceEnrollmentCreated, struct{ Unrelated string }{})
				return err
			}()
			if err == nil {
				t.Fatal("Render accepted a payload the template cannot read")
			}
			if !strings.Contains(err.Error(), tt.wantPart) {
				t.Errorf("error %q does not name the %s", err, tt.wantPart)
			}
		})
	}
}

// The HTML part gets the same startup check as the other two: an override
// naming a field that does not exist must not wait until send time.
func TestNewRenderer_shouldFailOnAnHTMLOverrideNamingAnUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "service_enrollment_created.html.tmpl", "<p>{{ .NoSuchField }}</p>")

	_, err := NewRenderer(dir, "")
	if err == nil {
		t.Fatal("NewRenderer accepted an html override naming an unknown field")
	}
	if !strings.Contains(err.Error(), "html body") {
		t.Errorf("error %q does not name the html body", err)
	}
}

// The emailed ssh_config recipe mirrors what `ssoossh service enroll`
// prints, which names the approved service account on the Match line
// because that account is the certificate's sole principal. A placeholder
// there would have to be edited before the recipe works.
func TestRender_shouldNameTheServiceAccountInTheRecipe(t *testing.T) {
	r, err := NewRenderer("", "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	out, err := r.Render(notify.KindServiceEnrollmentCreated, samplePayload(t, notify.KindServiceEnrollmentCreated))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	for name, body := range map[string]string{"text": out.Text, "html": out.HTML} {
		if !strings.Contains(body, "Match user deploy-bot") {
			t.Errorf("%s body does not name the service account on the Match line", name)
		}
	}
}

// A payload with no service account — an older row, or an enrollment the
// field never reached — keeps the placeholder rather than emitting a Match
// line that silently matches nothing.
func TestRender_shouldKeepThePlaceholderWithoutAServiceAccount(t *testing.T) {
	r, err := NewRenderer("", "")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	out, err := r.Render(notify.KindServiceEnrollmentCreated, &notify.ServiceEnrollmentCreated{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if !strings.Contains(out.Text, "Match user USERNAME") {
		t.Error("text body dropped the placeholder when no service account is known")
	}
}
