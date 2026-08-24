package notify_test

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/notify"
)

var update = flag.Bool("update", false, "rewrite the generated block in docs/email-notifications.md instead of comparing against it")

// docPath is the operator-facing reference this test keeps honest.
const docPath = "../../docs/email-notifications.md"

// The generated block's fences. Everything between them is derived from
// the registry; everything outside is hand-written prose.
const (
	beginMarker = "<!-- BEGIN GENERATED NOTIFICATION REFERENCE -->"
	endMarker   = "<!-- END GENERATED NOTIFICATION REFERENCE -->"
)

// The whole point of documenting template fields is that someone writing an
// override can trust the list. A hand-maintained table drifts the first
// time a field is added — the person adding it is editing Go, not markdown
// — so the table is generated from the same registry the templates render
// against, and this test is what makes "generated" true rather than
// aspirational.
//
// Run `go test ./server/notify/ -update` after adding or changing a
// notification kind.
func TestNotificationReference_shouldMatchTheRegistry(t *testing.T) {
	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}

	before, after, err := splitAroundGeneratedBlock(string(doc))
	if err != nil {
		t.Fatalf("%s: %v", docPath, err)
	}

	want := renderReference()

	if *update {
		updated := before + beginMarker + "\n" + want + endMarker + after
		//nolint:gosec // G703: docPath is the const above, not caller-controlled.
		if err := os.WriteFile(docPath, []byte(updated), 0o600); err != nil {
			t.Fatalf("write %s: %v", docPath, err)
		}
		return
	}

	_, generated, _ := strings.Cut(string(doc), beginMarker+"\n")
	got, _, _ := strings.Cut(generated, endMarker)

	if got != want {
		t.Errorf("the notification reference in %s is stale.\n\ngot:\n%s\nwant:\n%s\n\nrun: go test ./server/notify/ -update",
			docPath, got, want)
	}
}

// Every kind must reach the reference, or a template author has no way to
// learn a notification exists.
func TestNotificationReference_shouldCoverEveryKind(t *testing.T) {
	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}

	for _, def := range notify.Definitions() {
		if !strings.Contains(string(doc), string(def.Kind)) {
			t.Errorf("%s does not document the %q notification", docPath, def.Kind)
		}
	}
}

// renderReference builds the markdown for every registered kind: what it
// is, which template files it reads, and every field a template may use.
func renderReference() string {
	var b strings.Builder

	for _, def := range notify.Definitions() {
		fmt.Fprintf(&b, "\n### %s\n\n", def.Title)
		fmt.Fprintf(&b, "`%s`\n\n", def.Kind)
		fmt.Fprintf(&b, "%s\n\n", def.Description)
		fmt.Fprintf(&b, "Default: **%s**.\n\n", enabledWord(def.DefaultEnabled))

		fmt.Fprintf(&b, "Templates:\n\n")
		for _, part := range []string{"subject", "txt", "html"} {
			fmt.Fprintf(&b, "- `%s`\n", fmt.Sprintf("%s.%s.tmpl", def.Kind, part))
		}
		fmt.Fprintf(&b, "\n")

		fmt.Fprintf(&b, "| Field | Type | Description |\n")
		fmt.Fprintf(&b, "| --- | --- | --- |\n")
		for _, field := range def.Fields {
			fmt.Fprintf(&b, "| `.%s` | `%s` | %s |\n", field.Name, field.Type, field.Description)
		}
		fmt.Fprintf(&b, "\n")
	}

	return b.String()
}

// enabledWord renders a default as the word the doc reads with.
func enabledWord(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// splitAroundGeneratedBlock returns the text before and after the generated
// block, erroring when either marker is missing.
func splitAroundGeneratedBlock(doc string) (before, after string, err error) {
	before, rest, found := strings.Cut(doc, beginMarker+"\n")
	if !found {
		return "", "", fmt.Errorf("missing %s", beginMarker)
	}
	_, after, found = strings.Cut(rest, endMarker)
	if !found {
		return "", "", fmt.Errorf("missing %s", endMarker)
	}
	return before, after, nil
}
