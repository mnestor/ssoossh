package main

// The version ratchet is the whole mechanism, so it is what these tests
// cover: carrying a version forward when nothing moved (which is what keeps
// wire-contract-check's "regenerating changes nothing" assertion honest), and
// bumping it when any part of the contract did.

import (
	"os"
	"path/filepath"
	"testing"
)

// contract builds a manifest with the given fixtures, so a table row can vary
// one thing at a time without restating the rest.
func contract(version int, endpoints, events []string, fixtures map[string]string) *manifest {
	return &manifest{
		Version:   version,
		Note:      note,
		Endpoints: endpoints,
		SSEEvents: events,
		Fixtures:  fixtures,
	}
}

// baseline is the manifest every "did this change?" row is compared against.
func baseline(version int) *manifest {
	return contract(version,
		[]string{"GET /api/ca", "POST /api/certs/pam"},
		[]string{"approved", "denied"},
		map[string]string{"internal/apitypes/testdata/pam_request.full.json": "abc123"},
	)
}

func TestNextVersion(t *testing.T) {
	tests := []struct {
		name     string
		previous *manifest
		built    *manifest
		want     int
	}{
		{
			name:     "should start at one when no manifest is committed yet",
			previous: nil,
			built:    baseline(0),
			want:     1,
		},
		{
			name:     "should carry the version forward when nothing changed",
			previous: baseline(7),
			built:    baseline(0),
			want:     7,
		},
		{
			name:     "should bump when an endpoint is added",
			previous: baseline(7),
			built: contract(0,
				[]string{"GET /api/ca", "POST /api/certs/console", "POST /api/certs/pam"},
				[]string{"approved", "denied"},
				map[string]string{"internal/apitypes/testdata/pam_request.full.json": "abc123"}),
			want: 8,
		},
		{
			name:     "should bump when an endpoint is removed",
			previous: baseline(7),
			built: contract(0,
				[]string{"GET /api/ca"},
				[]string{"approved", "denied"},
				map[string]string{"internal/apitypes/testdata/pam_request.full.json": "abc123"}),
			want: 8,
		},
		{
			name:     "should bump when a terminal SSE event is added",
			previous: baseline(7),
			built: contract(0,
				[]string{"GET /api/ca", "POST /api/certs/pam"},
				[]string{"approved", "denied", "revoked"},
				map[string]string{"internal/apitypes/testdata/pam_request.full.json": "abc123"}),
			want: 8,
		},
		{
			name:     "should bump when a fixture's contents change",
			previous: baseline(7),
			built: contract(0,
				[]string{"GET /api/ca", "POST /api/certs/pam"},
				[]string{"approved", "denied"},
				map[string]string{"internal/apitypes/testdata/pam_request.full.json": "def456"}),
			want: 8,
		},
		{
			name:     "should bump when a fixture is added",
			previous: baseline(7),
			built: contract(0,
				[]string{"GET /api/ca", "POST /api/certs/pam"},
				[]string{"approved", "denied"},
				map[string]string{
					"internal/apitypes/testdata/pam_request.full.json": "abc123",
					"internal/apitypes/testdata/ca_response.full.json": "789xyz",
				}),
			want: 8,
		},
		{
			name:     "should bump when a fixture is removed",
			previous: baseline(7),
			built: contract(0,
				[]string{"GET /api/ca", "POST /api/certs/pam"},
				[]string{"approved", "denied"},
				map[string]string{}),
			want: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextVersion(tt.previous, tt.built); got != tt.want {
				t.Errorf("nextVersion() = %d, want %d", got, tt.want)
			}
		})
	}
}

// should not bump on a reordered fixture map. Fixtures is a map, so Go's
// iteration order varies run to run; a comparison that depended on it would
// bump the version at random and destroy the signal.
func TestSameContract_ShouldIgnoreFixtureMapOrdering(t *testing.T) {
	fixtures := map[string]string{"a.json": "1", "b.json": "2", "c.json": "3"}
	reordered := map[string]string{"c.json": "3", "a.json": "1", "b.json": "2"}

	a := contract(1, []string{"GET /api/ca"}, []string{"approved"}, fixtures)
	b := contract(1, []string{"GET /api/ca"}, []string{"approved"}, reordered)

	if !sameContract(a, b) {
		t.Error("sameContract() = false for the same fixtures in a different insertion order, want true")
	}
}

// should treat a reordered endpoint list as a change. Endpoints is sorted by
// build, so a difference in order can only mean a difference in content —
// comparing it as an ordered slice is what makes that true.
func TestSameContract_ShouldCompareEndpointsInOrder(t *testing.T) {
	a := contract(1, []string{"GET /api/ca", "POST /api/certs/pam"}, []string{"approved"}, nil)
	b := contract(1, []string{"POST /api/certs/pam", "GET /api/ca"}, []string{"approved"}, nil)

	if sameContract(a, b) {
		t.Error("sameContract() = true for endpoints in a different order, want false")
	}
}

// should read every operation out of a spec, and only the operations: a path
// item also carries non-method keys, and counting those as endpoints would
// bump the version whenever a shared parameter was added.
func TestReadEndpoints_ShouldReturnOnlyOperations(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "openapi.yaml")

	const doc = `openapi: 3.1.0
paths:
  /api/ca:
    get:
      summary: Fetch the CA keys
  /api/certs/pam:
    parameters:
      - name: shared
    post:
      summary: Create a PAM request
    delete:
      summary: Not a real route, here to prove more than one method is read
`
	if err := os.WriteFile(spec, []byte(doc), 0o600); err != nil {
		t.Fatalf("writing the spec: %v", err)
	}

	got, err := readEndpointsFrom(spec)
	if err != nil {
		t.Fatalf("readEndpointsFrom() error = %v", err)
	}

	want := []string{"DELETE /api/certs/pam", "GET /api/ca", "POST /api/certs/pam"}
	if !equalStrings(got, want) {
		t.Errorf("readEndpointsFrom() = %v, want %v", got, want)
	}
}

// should fail rather than report an empty contract when the spec has no
// paths. An empty endpoint list would compare equal to nothing else and
// silently bump the version on the next real run.
func TestReadEndpoints_ShouldFailOnASpecWithNoPaths(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "openapi.yaml")

	if err := os.WriteFile(spec, []byte("openapi: 3.1.0\n"), 0o600); err != nil {
		t.Fatalf("writing the spec: %v", err)
	}

	if _, err := readEndpointsFrom(spec); err == nil {
		t.Error("readEndpointsFrom() error = nil for a spec with no paths, want an error")
	}
}

// should hash file contents, so an edit to a fixture moves its digest.
func TestHashFixtures_ShouldChangeWhenAFixtureChanges(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "shape.json")

	if err := os.WriteFile(fixture, []byte(`{"username":"alice"}`), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	before, err := hashFixturesIn([]string{filepath.Join(dir, "*.json")})
	if err != nil {
		t.Fatalf("hashFixturesIn() error = %v", err)
	}

	if err := os.WriteFile(fixture, []byte(`{"local_user":"alice"}`), 0o600); err != nil {
		t.Fatalf("rewriting the fixture: %v", err)
	}

	after, err := hashFixturesIn([]string{filepath.Join(dir, "*.json")})
	if err != nil {
		t.Fatalf("hashFixturesIn() error = %v", err)
	}

	if before[filepath.ToSlash(fixture)] == after[filepath.ToSlash(fixture)] {
		t.Error("the digest did not change after a field was renamed; a rename would ship unnoticed")
	}
}

// should fail when a glob matches nothing. An empty fixture set is
// indistinguishable from "no shapes exist", which would let a release ship a
// bundle with no shapes in it.
func TestHashFixtures_ShouldFailWhenAGlobMatchesNothing(t *testing.T) {
	if _, err := hashFixturesIn([]string{filepath.Join(t.TempDir(), "*.json")}); err == nil {
		t.Error("hashFixturesIn() error = nil for a glob matching no files, want an error")
	}
}

// newContractTree builds a minimal repository layout in a temp directory and
// chdirs into it, so run() can be exercised against real files without the
// repository's own spec and goldens. Returns the fixture path a test mutates
// to simulate a wire change.
func newContractTree(t *testing.T) (fixture string) {
	t.Helper()

	dir := t.TempDir()
	for _, sub := range []string{"docs", "internal/apitypes/testdata", "server/controller/testdata"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("creating %s: %v", sub, err)
		}
	}

	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}

	write("docs/openapi.yaml", "openapi: 3.1.0\npaths:\n  /api/ca:\n    get:\n      summary: CA keys\n")
	write("internal/apitypes/testdata/pam_request.full.json", `{"username":"alice"}`)
	write("server/controller/testdata/sse_stream_approved.sse", "event:approved\ndata:{\"data\":{}}\n\n")

	t.Chdir(dir)
	return filepath.Join(dir, "internal/apitypes/testdata/pam_request.full.json")
}

// version reads the committed manifest's version, failing the test if it
// cannot.
func version(t *testing.T) int {
	t.Helper()

	m, err := load()
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if m == nil {
		t.Fatal("no manifest was written")
	}
	return m.Version
}

// should write a version 1 manifest on the first run, then leave it alone on
// a second. Idempotence is the property wire-contract-check depends on: it
// regenerates and asserts nothing moved, so a generator that rewrote the
// version every run would fail the gate on every commit.
func TestRun_ShouldBeIdempotentWhenTheContractIsUnchanged(t *testing.T) {
	newContractTree(t)

	if err := run(false); err != nil {
		t.Fatalf("first run() error = %v", err)
	}
	if got := version(t); got != 1 {
		t.Fatalf("first run wrote version %d, want 1", got)
	}

	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}

	if err := run(false); err != nil {
		t.Fatalf("second run() error = %v", err)
	}

	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("re-reading the manifest: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("a second run rewrote the manifest:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// should bump the version when a fixture's shape changes, which is the whole
// point: a renamed field has to reach the other repository as a number that
// moved.
func TestRun_ShouldBumpTheVersionWhenAFixtureChanges(t *testing.T) {
	fixture := newContractTree(t)

	if err := run(false); err != nil {
		t.Fatalf("first run() error = %v", err)
	}

	if err := os.WriteFile(fixture, []byte(`{"local_user":"alice"}`), 0o600); err != nil {
		t.Fatalf("renaming the field: %v", err)
	}

	if err := run(false); err != nil {
		t.Fatalf("second run() error = %v", err)
	}
	if got := version(t); got != 2 {
		t.Errorf("version = %d after a field rename, want 2", got)
	}
}

// should fail -check when the committed manifest no longer matches the
// shapes. This is what make wire-contract-check reports, and what stops a
// rename reaching a release with the contract version unmoved.
func TestRun_ShouldFailCheckWhenTheContractDrifted(t *testing.T) {
	fixture := newContractTree(t)

	if err := run(false); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if err := run(true); err != nil {
		t.Fatalf("run(check) error = %v on an up-to-date manifest, want nil", err)
	}

	if err := os.WriteFile(fixture, []byte(`{"local_user":"alice"}`), 0o600); err != nil {
		t.Fatalf("renaming the field: %v", err)
	}

	if err := run(true); err == nil {
		t.Error("run(check) error = nil after a field rename, want a staleness error")
	}
}

// should fail -check when there is no manifest at all, rather than reporting
// a contract nobody committed.
func TestRun_ShouldFailCheckWhenNoManifestExists(t *testing.T) {
	newContractTree(t)

	if err := run(true); err == nil {
		t.Error("run(check) error = nil with no committed manifest, want an error")
	}
}

// should report the committed version, which is what the bundle script reads
// to name what it ships.
func TestReportVersion_ShouldFailBeforeAManifestExists(t *testing.T) {
	newContractTree(t)

	if err := reportVersion(); err == nil {
		t.Error("reportVersion() error = nil with no committed manifest, want an error")
	}

	if err := run(false); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if err := reportVersion(); err != nil {
		t.Errorf("reportVersion() error = %v after a manifest was written, want nil", err)
	}
}
