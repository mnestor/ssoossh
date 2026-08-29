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
	for _, want := range []string{"index.md", "top-level.md", "db.md", "authentication.md"} {
		if _, ok := pages[want]; !ok {
			t.Errorf("expected a page named %q", want)
		}
	}
}

func TestMarkdownPages_ShouldSplitLargeGroupsOntoTheirOwnPages(t *testing.T) {
	t.Parallel()

	pages := mdPages(t)
	for _, want := range []string{
		// http's big groups leave the section index; cert_options splits per
		// certificate type.
		"http/index.md",
		"http/tls.md",
		"http/access_logging.md",
		"cert_options/index.md",
		"cert_options/user.md",
		"cert_options/service.md",
	} {
		if _, ok := pages[want]; !ok {
			t.Errorf("expected a page named %q", want)
		}
	}
	for _, gone := range []string{"http.md", "cert_options.md"} {
		if _, ok := pages[gone]; ok {
			t.Errorf("expected no flat page %q once the section is split", gone)
		}
	}
}

func TestMarkdownPages_ShouldRenderKeyHeadingWithTypeAndDefault(t *testing.T) {
	t.Parallel()

	pages := mdPages(t)
	tests := []struct {
		name string
		page string
		want []string
	}{
		{
			name: "should render a section key relative to its page",
			page: "http/index.md",
			want: []string{"## `port`", "`int`, default `8080`"},
		},
		{
			name: "should render a split group's keys relative to the subpage",
			page: "http/tls.md",
			want: []string{"## `min_version`"},
		},
		{
			name: "should keep an inline nested group's keys on the section page",
			page: "http/index.md",
			want: []string{"## `service_code_rate_limit.limit`"},
		},
	}
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

func TestMarkdownPages_ShouldRenderAtAGlanceTableWithFullPaths(t *testing.T) {
	t.Parallel()

	pages := mdPages(t)
	page := pages["http/index.md"]
	for _, want := range []string{
		"| Key | Type | Default |",
		// A local key anchors down the page by its relative name.
		"| [`http.port`](#port) | int | `8080` |",
		// A split-out key links into its subpage.
		"| [`http.tls.min_version`](/ssoossh/reference/config/http/tls/#min_version) | string |",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("expected the http index table to contain %q", want)
		}
	}
	if !strings.Contains(pages["http/tls.md"], "| [`http.tls.min_version`](#min_version) | string |") {
		t.Errorf("expected the tls subpage table to anchor min_version locally")
	}
}

func TestMarkdownPages_ShouldRewriteGoNamesToBacktickedKeys(t *testing.T) {
	t.Parallel()

	// ProxyProtocol's doc comment references TrustedProxies; inside the http
	// section that renders as the short key, wrapped in backticks.
	page := mdPages(t)["http/index.md"]
	if !strings.Contains(page, "as opposed to `trusted_proxies` below") {
		t.Errorf("expected the http page to rewrite TrustedProxies to a backticked key")
	}
	// gin's SetTrustedProxies is Go code, not a config key, and must survive.
	if !strings.Contains(page, "SetTrustedProxies") {
		t.Errorf("expected the http page to leave the Go method name SetTrustedProxies alone")
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
			name: "should title the index",
			page: "index.md",
			want: []string{"---\ntitle: \"Configuration reference\""},
		},
		{
			name: "should title a section page by its key",
			page: "db.md",
			want: []string{"---\ntitle: \"db\""},
		},
		{
			name: "should title a split section's index by the section key",
			page: "http/index.md",
			want: []string{"---\ntitle: \"http\""},
		},
		{
			name: "should title a subpage by its full key",
			page: "http/tls.md",
			want: []string{"---\ntitle: \"http.tls\""},
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
		"| [Top-level keys](/ssoossh/reference/config/top-level/) |",
		"| [http](/ssoossh/reference/config/http/) |",
		"| [cert_options](/ssoossh/reference/config/cert_options/) |",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("expected the index to contain %q", want)
		}
	}
}

func TestMarkdownPages_ShouldDocumentEmbeddedGroupUnderParentGlob(t *testing.T) {
	t.Parallel()

	// A synthetic embedded field: its keys belong to another module, so it is
	// documented as one group named for the struct embedding it.
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
	if !strings.Contains(page, "## `file.*`") {
		t.Errorf("expected the embedded group to render as file.*, got:\n%s", page)
	}
	// The group heading `file` takes the base slug, so the embedded entry
	// `file.*` (same slug once punctuation drops) lands on file-1.
	if !strings.Contains(page, "| [`logging.file.*`](#file-1) | | |") {
		t.Errorf("expected the table to list the embedded group by full path, got:\n%s", page)
	}
}

func TestSidebarJSON_ShouldKeepDeclarationOrderAndGroupSplitSections(t *testing.T) {
	t.Parallel()

	out, err := SidebarJSON(walkConfig(t))
	if err != nil {
		t.Fatalf("SidebarJSON failed: %v", err)
	}

	// Declaration order, not alphabetical: http is declared before
	// cert_options in the Config struct, so it must list first.
	http := strings.Index(out, `"label": "http"`)
	certs := strings.Index(out, `"label": "cert_options"`)
	if http < 0 || certs < 0 || http > certs {
		t.Errorf("expected http to precede cert_options in the sidebar, got http=%d cert_options=%d", http, certs)
	}

	// A split section is a group holding its overview and subpages.
	for _, want := range []string{
		`"slug": "reference/config/http"`,
		`"slug": "reference/config/http/tls"`,
		`"slug": "reference/config/cert_options/user"`,
		// An unsplit section is a plain page entry.
		`"slug": "reference/config/db"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the sidebar JSON to contain %s", want)
		}
	}
}

func TestSlugger_ShouldMatchGithubSluggerRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		heading string
		want    string
	}{
		{name: "should keep underscores", heading: "min_version", want: "min_version"},
		{name: "should drop dots", heading: "service_code_rate_limit.limit", want: "service_code_rate_limitlimit"},
		{name: "should drop asterisks", heading: "file.*", want: "file"},
		{name: "should lowercase", heading: "Overview", want: "overview"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := newSlugger().slug(tt.heading); got != tt.want {
				t.Errorf("slug(%q) = %q, want %q", tt.heading, got, tt.want)
			}
		})
	}
}

func TestSlugger_ShouldSuffixRepeatedHeadings(t *testing.T) {
	t.Parallel()

	s := newSlugger()
	if got := s.slug("limit"); got != "limit" {
		t.Errorf("first slug = %q, want limit", got)
	}
	if got := s.slug("limit"); got != "limit-1" {
		t.Errorf("second slug = %q, want limit-1", got)
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

func TestWriteMarkdown_ShouldDeleteStalePagesAndEmptiedDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	staleDir := filepath.Join(dir, "removed_section")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("failed to seed the stale dir: %v", err)
	}
	stale := filepath.Join(staleDir, "gone.md")
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
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("expected the emptied dir to be deleted, stat returned: %v", err)
	}
}

func TestMarkdownPages_ShouldEscapeAngleBracketsInProse(t *testing.T) {
	t.Parallel()

	// The extra-fields doc writes {{.Extra.<name>}}; unescaped, Markdown
	// reads <name> as an inline HTML tag and swallows the sentence.
	page := mdPages(t)["authentication.md"]
	if !strings.Contains(page, "{{.Extra.&lt;name") {
		t.Errorf("expected the angle brackets in {{.Extra.<name>}} to be escaped")
	}
	if strings.Contains(page, "as {{.Extra.<name") {
		t.Errorf("expected no raw <name> to survive in prose")
	}
}
