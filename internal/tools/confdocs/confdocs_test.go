package confdocs

import (
	"strings"
	"testing"
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

// One struct type behind several config keys is why default_ overrides
// exist: GenericLogging is db.logging, queue.logging, ldap.logging and
// mail.logging at once, and those destinations ship different values. The
// instantiating field carries them, and absence means absence -- a
// destination with no override renders as prose with no key, the deliberate
// "unset, falls through to the main log" shape.
func TestWalk_ShouldApplyPerInstantiationDefaultOverrides(t *testing.T) {
	t.Parallel()

	defaults := map[string]*Field{}
	for _, f := range leaves(walkConfig(t)) {
		defaults[f.Path] = f
	}

	tests := []struct {
		path        string
		wantDefault string
		wantHas     bool
	}{
		{"db.logging.level", "WARN", true},
		{"mail.logging.level", "info", true},
		{"queue.logging.level", "", false},
		{"ldap.logging.level", "", false},
	}
	for _, tt := range tests {
		f := defaults[tt.path]
		if f == nil {
			t.Errorf("%s: not produced by the walk", tt.path)
			continue
		}
		if f.HasDefault != tt.wantHas || f.Default != tt.wantDefault {
			t.Errorf("%s: got default %q (set=%v), want %q (set=%v)",
				tt.path, f.Default, f.HasDefault, tt.wantDefault, tt.wantHas)
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

// A key's value comes from its default: tag and nowhere else. A field
// without one is not written -- its example: tag stands in, commented out,
// or its doc comment stands alone. That last shape is what regressing here
// brings back: prose with no key under it, reading as the comment for
// whichever key comes next.
func TestWriteYAMLField_ShouldWriteTheDefaultTheExampleOrNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		field   *Field
		want    string
		notWant string
	}{
		{
			name:  "should write the default when the field has one",
			field: &Field{Path: "logging.enable_stdout", Key: "enable_stdout", Type: "bool", Doc: []string{"Also writes to stdout."}, Default: "false", HasDefault: true},
			want:  "enable_stdout: false\n",
		},
		{
			name:  "should quote a string default so YAML cannot retype it",
			field: &Field{Path: "logging.level", Key: "level", Type: "string", Doc: []string{"The minimum log level."}, Default: "WARN", HasDefault: true},
			want:  "level: \"WARN\"\n",
		},
		{
			name:    "should prefer the default over the example when both are set",
			field:   &Field{Path: "logging.enable_stdout", Key: "enable_stdout", Type: "bool", Doc: []string{"Also writes to stdout."}, Default: "true", HasDefault: true, Example: "false"},
			want:    "enable_stdout: true\n",
			notWant: "# enable_stdout:",
		},
		{
			name:  "should comment the example in when there is no default",
			field: &Field{Path: "logging.enable_stdout", Key: "enable_stdout", Type: "bool", Doc: []string{"Also writes to stdout."}, Example: "false"},
			want:  "# enable_stdout: false\n",
		},
		{
			name:    "should leave a blank line when there is neither",
			field:   &Field{Path: "logging.include_app_name", Key: "include_app_name", Type: "bool", Doc: []string{"Adds an app attribute."}},
			want:    "# Adds an app attribute.\n\n",
			notWant: "include_app_name:",
		},
		{
			name:  "should write an empty-string default as a quoted empty string",
			field: &Field{Path: "http.server_name", Key: "server_name", Type: "string", Doc: []string{"The public host name."}, Default: "", HasDefault: true},
			want:  "server_name: \"\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder
			writeYAMLField(&b, tt.field, nil, "logging", 0)

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

// A section none of whose keys carry a default has no key written, so its
// fields have nothing to sit beneath: emitted anyway they were indented
// under a header that was not there. hsm is the real one -- documented in
// full by its own comment, and left unset.
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
	writeSection(&b, section, nil)

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

// The counterpart: one default below the section is enough for the header,
// and then every field is written under it as usual.
func TestWriteSection_ShouldWriteTheKeyAndFieldsWhenSomethingHasADefault(t *testing.T) {
	t.Parallel()

	section := &Section{
		Key: "hsm",
		Doc: []string{"Optionally sources the CA key from a PKCS#11 token."},
		Fields: []*Field{
			{Path: "hsm.module", Key: "module", Type: "string", Doc: []string{"The absolute path to the PKCS#11 shared library."}, Default: "/usr/lib/libsofthsm2.so", HasDefault: true},
		},
	}

	var b strings.Builder
	writeSection(&b, section, nil)

	if got := b.String(); !strings.Contains(got, "hsm:\n") || !strings.Contains(got, "module: \"/usr/lib/libsofthsm2.so\"") {
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
