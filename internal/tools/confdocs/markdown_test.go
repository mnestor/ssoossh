package confdocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mdPages walks the real config and renders the Markdown pages, the same way
// genconfdocs does.
func mdPages(t *testing.T) map[string]string {
	t.Helper()
	d, err := LoadDefaults(defaults)
	if err != nil {
		t.Fatalf("failed to load defaults.yaml: %v", err)
	}
	return MarkdownPages(walkConfig(t), d)
}

func TestMarkdownPages_ShouldRenderOnePagePerSectionPlusIndex(t *testing.T) {
	t.Parallel()

	pages := mdPages(t)
	for _, want := range []string{"index.md", "top-level.md", "http.md", "cert_options.md", "db.md"} {
		if _, ok := pages[want]; !ok {
			t.Errorf("expected a page named %q", want)
		}
	}
}

func TestMarkdownPages_ShouldRenderKeyHeadingWithTypeAndDefault(t *testing.T) {
	t.Parallel()

	page := mdPages(t)["http.md"]
	for _, want := range []string{
		"## `http.port`",
		"`int`, default `8080`",
		// A nested struct's key lands on its parent section's page.
		"## `http.tls.min_version`",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("expected http.md to contain %q", want)
		}
	}
}

func TestMarkdownPages_ShouldRewriteGoNamesToBacktickedKeys(t *testing.T) {
	t.Parallel()

	// ProxyProtocol's doc comment references TrustedProxies; inside the http
	// section that renders as the short key, wrapped in backticks.
	page := mdPages(t)["http.md"]
	if !strings.Contains(page, "as opposed to `trusted_proxies` below") {
		t.Errorf("expected http.md to rewrite TrustedProxies to a backticked key")
	}
	// gin's SetTrustedProxies is Go code, not a config key, and must survive.
	if !strings.Contains(page, "SetTrustedProxies") {
		t.Errorf("expected http.md to leave the Go method name SetTrustedProxies alone")
	}
}

func TestMarkdownPages_ShouldFenceDocCommentExamples(t *testing.T) {
	t.Parallel()

	// The fips field's doc comment carries an indented example line, which
	// must arrive fenced and dedented rather than reflowed into the prose.
	page := mdPages(t)["top-level.md"]
	if !strings.Contains(page, "```yaml\nfips: true\n```") {
		t.Errorf("expected top-level.md to fence the fips example as YAML")
	}
}

func TestMarkdownPages_ShouldEmitStarlightFrontmatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page string
		want []string
	}{
		{
			name: "should title the index and pin it first",
			page: "index.md",
			want: []string{"---\ntitle: \"Configuration reference\"", "sidebar:\n  order: 0"},
		},
		{
			name: "should title a section page by its key",
			page: "http.md",
			want: []string{"---\ntitle: \"http\""},
		},
		{
			name: "should mark every page as generated",
			page: "db.md",
			want: []string{mdGenerated},
		},
	}
	pages := mdPages(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, want := range tt.want {
				if !strings.Contains(pages[tt.page], want) {
					t.Errorf("expected %s to contain %q", tt.page, want)
				}
			}
		})
	}
}

func TestMarkdownPages_ShouldLinkEverySectionFromTheIndex(t *testing.T) {
	t.Parallel()

	index := mdPages(t)["index.md"]
	for _, want := range []string{
		"- [Top-level keys](./top-level/)",
		"- [http](./http/)",
		"- [cert_options](./cert_options/)",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("expected the index to contain %q", want)
		}
	}
}

func TestMarkdownPages_ShouldDocumentEmbeddedGroupUnderParentGlob(t *testing.T) {
	t.Parallel()

	// A synthetic embedded field: its keys belong to another module, so it is
	// documented as one group under the enclosing struct's path.
	sections := []*Section{{
		Key:   "logging",
		Title: "logging",
		Fields: []*Field{{
			Path:   "logging.file",
			Key:    "file",
			GoName: "File",
			Doc:    []string{"Rotating file destination."},
			Children: []*Field{{
				Key:      "Options",
				GoName:   "Options",
				Doc:      []string{"Rotation options from the embedded logger."},
				Embedded: true,
			}},
		}},
	}}
	page := MarkdownPages(sections, &Defaults{})["logging.md"]
	if !strings.Contains(page, "## `logging.file.*`") {
		t.Errorf("expected the embedded group to render under logging.file.*, got:\n%s", page)
	}
}

func TestWriteMarkdown_ShouldReportUnchangedWhenNothingMoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sections := walkConfig(t)
	d, err := LoadDefaults(defaults)
	if err != nil {
		t.Fatalf("failed to load defaults.yaml: %v", err)
	}

	changed, err := WriteMarkdown(dir, sections, d)
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if !changed {
		t.Errorf("expected the first write into an empty dir to report a change")
	}

	changed, err = WriteMarkdown(dir, sections, d)
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if changed {
		t.Errorf("expected the second write to report no change")
	}
}

func TestWriteMarkdown_ShouldDeleteStalePagesWhenSectionIsGone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stale := filepath.Join(dir, "removed_section.md")
	if err := os.WriteFile(stale, []byte("leftover"), 0o600); err != nil {
		t.Fatalf("failed to seed the stale page: %v", err)
	}

	d, err := LoadDefaults(defaults)
	if err != nil {
		t.Fatalf("failed to load defaults.yaml: %v", err)
	}
	if _, err := WriteMarkdown(dir, walkConfig(t), d); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected the stale page to be deleted, stat returned: %v", err)
	}
}
