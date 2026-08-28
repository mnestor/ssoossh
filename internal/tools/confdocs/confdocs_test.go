package confdocs

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	configDir = "../../../server/config"
	tlsDir    = "../../../server/config/tlsutils"
	defaults  = "../../../server/config/defaults.yaml"
)

func walkConfig(t *testing.T) []*Section {
	t.Helper()
	sections, err := Walk([]string{configDir, tlsDir}, "Config")
	if err != nil {
		t.Fatalf("failed to walk the config structs: %v", err)
	}
	return sections
}

// leaves returns every value-carrying key in the walk.
func leaves(sections []*Section) []*Field {
	var out []*Field
	var walk func([]*Field)
	walk = func(fields []*Field) {
		for _, f := range fields {
			if !f.IsStruct() && !f.Embedded {
				out = append(out, f)
			}
			walk(f.Children)
		}
	}
	for _, s := range sections {
		walk(s.Fields)
	}
	return out
}

func TestWalk_ShouldResolveNestedAndSquashedPaths(t *testing.T) {
	t.Parallel()

	paths := map[string]bool{}
	for _, f := range leaves(walkConfig(t)) {
		paths[f.Path] = true
	}

	// A plain nested struct, a struct from another package, and a field
	// squashed to the top level by `mapstructure:",squash"`.
	for _, want := range []string{
		"http.port",
		"http.tls.min_version",
		"cert_options.user.lifetime_policy.default_duration",
		"ssh_key",
		"max_cert_lifetime",
		"pubsub.nats.url",
	} {
		if !paths[want] {
			t.Errorf("expected the walk to produce the key %q", want)
		}
	}
}

func TestWalk_ShouldRenderConfigFacingTypes(t *testing.T) {
	t.Parallel()

	types := map[string]string{}
	for _, f := range leaves(walkConfig(t)) {
		types[f.Path] = f.Type
	}

	for path, want := range map[string]string{
		"http.port":                               "int",
		"http.address":                            "string",
		"http.trusted_proxies":                    "list",
		"cert_options.client_timeout":             "duration",
		"production":                              "bool",
		"http.cert_request_rate_limit.user":       "number",
		"db.provider":                             "string", // a named string type
		"http.service_code_rate_limit.limit":      "number",
		"cert_options.service.valid_duration":     "duration",
		"cert_options.user.lifetime_policy.tiers": "list",
	} {
		if got := types[path]; got != want {
			t.Errorf("%s: got type %q, want %q", path, got, want)
		}
	}
}

// The generated page and defaults.yaml both take their prose from here, so an
// undocumented field would ship as a bare key name in two places at once.
func TestRequireDocs_ShouldFindEveryFieldDocumented(t *testing.T) {
	t.Parallel()

	if err := RequireDocs(walkConfig(t)); err != nil {
		t.Error(err)
	}
}

// A default with no struct behind it is a key the shipped config offers and
// the server ignores.
func TestDefaults_ShouldNotSetKeysNoStructDeclares(t *testing.T) {
	t.Parallel()

	d, err := LoadDefaults(defaults)
	if err != nil {
		t.Fatalf("failed to load the defaults: %v", err)
	}
	if unknown := d.Unknown(walkConfig(t)); len(unknown) > 0 {
		t.Errorf("defaults.yaml sets keys no config struct declares: %v", unknown)
	}
}

func TestDefaults_ShouldDescribeUnsetKeysAsTheGoZeroValue(t *testing.T) {
	t.Parallel()

	d, err := LoadDefaults(defaults)
	if err != nil {
		t.Fatalf("failed to load the defaults: %v", err)
	}

	// http.port is set in defaults.yaml; log_request_body is not, and viper
	// therefore leaves it at the Go zero value.
	if got := d.Describe(&Field{Path: "http.port", Type: "int"}); got != "8080" {
		t.Errorf("http.port: got default %q, want 8080", got)
	}
	if got := d.Describe(&Field{Path: "nope.not.here", Type: "bool"}); got != "false" {
		t.Errorf("an unset bool should describe as false, got %q", got)
	}
	if got := d.Describe(&Field{Path: "nope.not.here", Type: "string"}); got != "empty" {
		t.Errorf("an unset string should describe as empty, got %q", got)
	}
}

// valuesFrom indexes a small YAML document the way WriteDefaults indexes the
// file it is replacing, so a test can say exactly which keys are set.
func valuesFrom(t *testing.T, src string) map[string]*yaml.Node {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("failed to parse the test document: %v", err)
	}
	values := map[string]*yaml.Node{}
	if len(doc.Content) > 0 {
		indexValues(doc.Content[0], "", values)
	}
	return values
}

// An unset key used to be written as prose with nothing under it, which reads
// as the comment for whichever key comes next. The example: tag is what names
// it, and it is rendered only where the file sets nothing.
func TestWriteYAMLField_ShouldRenderTheExampleOnlyForAnUnsetKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		field   *Field
		file    string
		want    string
		notWant string
	}{
		{
			name:  "should comment the key in when the file leaves it unset",
			field: &Field{Path: "logging.enable_stdout", Key: "enable_stdout", Type: "bool", Doc: []string{"Also writes to stdout."}, Example: "false"},
			file:  "logging:\n  level: WARN\n",
			want:  "# enable_stdout: false\n",
		},
		{
			name:    "should write the live value alone when the file sets it",
			field:   &Field{Path: "logging.enable_stdout", Key: "enable_stdout", Type: "bool", Doc: []string{"Also writes to stdout."}, Example: "false"},
			file:    "logging:\n  enable_stdout: true\n",
			want:    "enable_stdout: true\n",
			notWant: "# enable_stdout:",
		},
		{
			name:    "should leave a blank line when an unset key has no example",
			field:   &Field{Path: "logging.include_app_name", Key: "include_app_name", Type: "bool", Doc: []string{"Adds an app attribute."}},
			file:    "logging:\n  level: WARN\n",
			want:    "# Adds an app attribute.\n\n",
			notWant: "include_app_name:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder
			if err := writeYAMLField(&b, tt.field, valuesFrom(t, tt.file), nil, "logging", 0); err != nil {
				t.Fatalf("failed to write the field: %v", err)
			}

			got := b.String()
			if !strings.Contains(got, tt.want) {
				t.Errorf("got:\n%s\nwant it to contain:\n%s", got, tt.want)
			}
			if tt.notWant != "" && strings.Contains(got, tt.notWant) {
				t.Errorf("got:\n%s\nwant it not to contain:\n%s", got, tt.notWant)
			}
		})
	}
}

// A section the file sets nothing under has no key written, so its fields
// have nothing to sit beneath: emitted anyway they were indented under a
// header that was not there. hsm is the real one -- documented in full by its
// own comment, and left unset.
func TestWriteSection_ShouldWithholdFieldsWhenTheKeyIsWithheld(t *testing.T) {
	t.Parallel()

	section := &Section{
		Key: "hsm",
		Doc: []string{"Optionally sources the CA key from a PKCS#11 token."},
		Fields: []*Field{
			{Path: "hsm.module", Key: "module", Type: "string", Doc: []string{"The absolute path to the PKCS#11 shared library."}},
			{Path: "hsm.pin", Key: "pin", Type: "string", Doc: []string{"The user PIN."}},
		},
	}

	var b strings.Builder
	if err := writeSection(&b, section, valuesFrom(t, "logging:\n  level: WARN\n"), nil); err != nil {
		t.Fatalf("failed to write the section: %v", err)
	}

	got := b.String()
	if !strings.Contains(got, "Optionally sources the CA key") {
		t.Errorf("the section comment must survive, got:\n%s", got)
	}
	for _, unwanted := range []string{"hsm:", "PKCS#11 shared library", "The user PIN"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("got:\n%s\nwant it not to contain %q", got, unwanted)
		}
	}
}

// The counterpart: one value below the section is enough for the header, and
// then every field is written under it as usual.
func TestWriteSection_ShouldWriteTheKeyAndFieldsWhenSomethingIsSet(t *testing.T) {
	t.Parallel()

	section := &Section{
		Key: "hsm",
		Doc: []string{"Optionally sources the CA key from a PKCS#11 token."},
		Fields: []*Field{
			{Path: "hsm.module", Key: "module", Type: "string", Doc: []string{"The absolute path to the PKCS#11 shared library."}},
		},
	}

	var b strings.Builder
	if err := writeSection(&b, section, valuesFrom(t, "hsm:\n  module: /usr/lib/libsofthsm2.so\n"), nil); err != nil {
		t.Fatalf("failed to write the section: %v", err)
	}

	if got := b.String(); !strings.Contains(got, "hsm:\n") || !strings.Contains(got, "module: /usr/lib/libsofthsm2.so") {
		t.Errorf("expected the header and the field, got:\n%s", got)
	}
}

func TestRewriteRefs_ShouldRewriteKeysButNotAcronymsOrGoCode(t *testing.T) {
	t.Parallel()

	refs := CrossRefs(walkConfig(t))

	cases := []struct {
		name, line, scope, want string
	}{
		{
			name:  "a field name becomes its key, shortened inside its own section",
			line:  "See TrustedProxies above.",
			scope: "http",
			want:  "See trusted_proxies above.",
		},
		{
			name:  "a field name in another section keeps its full path",
			line:  "Resolved through TrustedProxies.",
			scope: "cert_options",
			want:  "Resolved through http.trusted_proxies.",
		},
		{
			name:  "an acronym is prose, not a reference",
			line:  "Steers the server toward FIPS 140-3 approved algorithms.",
			scope: "",
			want:  "Steers the server toward FIPS 140-3 approved algorithms.",
		},
		{
			name:  "a Go-qualified name is code, not a key",
			line:  "Checked in CertRequestService.Approve before signing.",
			scope: "",
			want:  "Checked in CertRequestService.Approve before signing.",
		},
		{
			name:  "a name with no key behind it is left alone",
			line:  "See DBProviderSqlite for the value.",
			scope: "db",
			want:  "See DBProviderSqlite for the value.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rewriteRefs(tc.line, refs, tc.scope); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// RequireGroup is four different keys across the file, so it resolves only
// within the section that defines the one being referred to.
func TestCrossRefsIn_ShouldResolveAnAmbiguousNameInsideItsOwnSection(t *testing.T) {
	t.Parallel()

	sections := walkConfig(t)

	if _, ok := CrossRefs(sections)["RequireGroup"]; ok {
		t.Error("RequireGroup is ambiguous across the file and should not resolve globally")
	}
	if got := CrossRefsIn(sections, "admin")["RequireGroup"]; got != "admin.require_group" {
		t.Errorf("inside admin, RequireGroup should be admin.require_group, got %q", got)
	}
	// A name that is unambiguous globally is unaffected.
	if got := CrossRefsIn(sections, "admin")["TrustedProxies"]; got != "http.trusted_proxies" {
		t.Errorf("TrustedProxies should still resolve globally, got %q", got)
	}
}

func TestStripGoOnlyRefs_ShouldDropPointersWithNoKeyBehindThem(t *testing.T) {
	t.Parallel()

	refs := CrossRefs(walkConfig(t))

	// The sentence wraps across two lines, as it does in the real comment.
	got := StripGoOnlyRefs([]string{
		"Identifies which database backend to use. See DBProviderSqlite and",
		"DBProviderPostgres.",
	}, refs)
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "DBProviderSqlite") {
		t.Errorf("a Go-only reference should be removed, got %q", joined)
	}
	if !strings.Contains(joined, "Identifies which database backend") {
		t.Errorf("the surrounding prose must survive, got %q", joined)
	}

	// A reference that does resolve to a key stays.
	kept := strings.Join(StripGoOnlyRefs([]string{"See TrustedProxies."}, refs), " ")
	if !strings.Contains(kept, "TrustedProxies") {
		t.Errorf("a reference with a key behind it must be kept, got %q", kept)
	}
}

func TestTroffEscape_ShouldNeutraliseRequestLines(t *testing.T) {
	t.Parallel()

	// A line opening with a dot would be read as a troff request.
	if got := troffEscape(".well-known is stripped"); !strings.HasPrefix(got, `\&.`) {
		t.Errorf("a leading dot must be escaped, got %q", got)
	}
	if got := troffEscape(`a \ backslash`); !strings.Contains(got, `\e`) {
		t.Errorf("a backslash must be escaped, got %q", got)
	}
	if got := troffEscape("an — em dash"); !strings.Contains(got, `\(em`) {
		t.Errorf("an em dash must become a troff escape, got %q", got)
	}
}

func TestManPage_ShouldBeDeterministic(t *testing.T) {
	t.Parallel()

	sections := walkConfig(t)
	d, err := LoadDefaults(defaults)
	if err != nil {
		t.Fatalf("failed to load the defaults: %v", err)
	}

	first := ManPage(sections, d)
	if second := ManPage(sections, d); first != second {
		t.Error("ManPage is not deterministic across two renders of the same walk")
	}
	if !strings.Contains(first, ".SS http") {
		t.Error("expected an http section in the rendered page")
	}
}

func TestSpliceMarkers_ShouldReplaceOnlyTheGeneratedRegion(t *testing.T) {
	t.Parallel()

	src := "keep me\n" + ManBegin + "\nold body\n" + ManEnd + "\nkeep me too\n"
	got, err := spliceMarkers(src, ManBegin, ManEnd, "new body\n")
	if err != nil {
		t.Fatalf("failed to splice: %v", err)
	}

	want := "keep me\n" + ManBegin + "\nnew body\n" + ManEnd + "\nkeep me too\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSpliceMarkers_ShouldFailWhenMarkersAreMissing(t *testing.T) {
	t.Parallel()

	if _, err := spliceMarkers("no markers here\n", ManBegin, ManEnd, "body"); err == nil {
		t.Error("expected an error when the markers are absent")
	}
}
