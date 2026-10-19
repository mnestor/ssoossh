package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/internal/tools/confdocs"
)

// repoRoot is where genconfdocs runs from; tests run in the package
// directory instead.
const repoRoot = "../../../"

// walkFromTest walks the config structs the way run does, from the
// package directory.
func walkFromTest(t *testing.T) []*confdocs.Section {
	t.Helper()
	sections, err := confdocs.Walk([]string{repoRoot + configPkg, repoRoot + tlsPkg}, "Config")
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	return sections
}

// should keep the presets page's table identical to what the embedded
// preset files render, so the docs cannot describe values the server does
// not merge.
func TestPresetTable_ShouldMatchTheDocsPage(t *testing.T) {
	t.Parallel()

	table, err := presetTable(walkFromTest(t))
	if err != nil {
		t.Fatalf("presetTable() error = %v", err)
	}
	page, err := os.ReadFile(repoRoot + presetsPage)
	if err != nil {
		t.Fatalf("read %s: %v", presetsPage, err)
	}
	_, rest, ok := strings.Cut(string(page), presetsBegin+"\n")
	if !ok {
		t.Fatalf("%s has no %q marker", presetsPage, presetsBegin)
	}
	region, _, ok := strings.Cut(rest, presetsEnd)
	if !ok {
		t.Fatalf("%s has no %q marker", presetsPage, presetsEnd)
	}
	if region != table {
		t.Errorf("%s is stale, run `make confdocs`:\ngot:\n%s\nwant:\n%s", presetsPage, region, table)
	}
}

// should require every key a preset sets to mention ldap.schema in its
// doc comment.
func TestCheckPresetDocs_ShouldPassForTheShippedStructs(t *testing.T) {
	t.Parallel()

	if err := checkPresetDocs(walkFromTest(t)); err != nil {
		t.Error(err)
	}
}

// should fail when a preset key's doc comment says nothing of the preset.
func TestCheckPresetDocs_ShouldFailWhenADocOmitsThePreset(t *testing.T) {
	t.Parallel()

	sections := walkFromTest(t)
	_, fields := fieldIndex(sections)
	f, ok := fields["nss.attributes.shell"]
	if !ok {
		t.Fatal("nss.attributes.shell not found in the walk")
	}
	f.Doc = []string{"Shell is the login shell."}

	if err := checkPresetDocs(sections); err == nil || !strings.Contains(err.Error(), "nss.attributes.shell") {
		t.Errorf("checkPresetDocs() = %v, want an error naming nss.attributes.shell", err)
	}
}

// should link every row to a heading that exists on the generated
// reference page it names.
func TestReferenceURL_ShouldPointAtARealHeading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		page string
	}{
		{"ldap.id_attribute", "ldap/index.md"},
		{"nss.user_filter", "nss/index.md"},
		{"nss.group_filter", "nss/index.md"},
		{"nss.disabled_filter", "nss/index.md"},
		{"nss.attributes.shell", "nss/attributes.md"},
		{"nss.attributes.group_member", "nss/attributes.md"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()

			url := referenceURL(tt.key)
			_, anchor, _ := strings.Cut(url, "#")
			wantPage := "/ssoossh/reference/config/" + strings.TrimSuffix(strings.TrimSuffix(tt.page, "index.md"), ".md")
			if !strings.HasPrefix(url, strings.TrimSuffix(wantPage, "/")) {
				t.Errorf("referenceURL(%q) = %q, want it on %s", tt.key, url, wantPage)
			}
			src, err := os.ReadFile(repoRoot + siteDir + "/" + tt.page)
			if err != nil {
				t.Fatalf("read %s: %v", tt.page, err)
			}
			if !strings.Contains(string(src), "\n## `"+anchor+"`\n") {
				t.Errorf("%s has no heading for #%s", tt.page, anchor)
			}
		})
	}
}

// should render an empty value as a word and escape a pipe inside a cell.
func TestPresetCell_ShouldRenderValuesVerbatim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"should mark an empty value", "", "*empty*"},
		{"should quote a value as code", "member", "`member`"},
		{"should escape a pipe", "(|(a=1)(b=2))", "`(\\|(a=1)(b=2))`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := presetCell(tt.value, "empty"); got != tt.want {
				t.Errorf("presetCell(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
