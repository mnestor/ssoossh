//go:build !exclude_frontend

package frontend

// Test methodology: Tests verify frontend asset serving and handler behavior.
// Uses httptest.ResponseRecorder to capture responses without a real listener
// and testing/fstest to mock file systems. Tests run in parallel (t.Parallel()).
// Each test verifies one specific serving behavior or edge case.

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
)

// TestMain puts gin into test mode once for the whole package, matching
// gin's own recommended test setup, instead of an init() function (which
// CLAUDE.md disallows).
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestNewFileServerWithCaching_ShouldSetCacheControlHeader(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello")},
	}
	fileServer := NewFileServerWithCaching(http.FS(fsys), 3600)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	fileServer.ServeHTTP(w, req)

	if got := w.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("got Cache-Control %q, want %q", got, "public, max-age=3600")
	}
}

func TestNewFileServerWithCaching_ShouldSetLastModifiedHeader(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello")},
	}
	fileServer := NewFileServerWithCaching(http.FS(fsys), 3600)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	fileServer.ServeHTTP(w, req)

	if got := w.Header().Get("Last-Modified"); got == "" {
		t.Error("expected Last-Modified header to be set")
	}
}

func TestNewFileServerWithCaching_ShouldServeFileContent(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello world")},
	}
	fileServer := NewFileServerWithCaching(http.FS(fsys), 3600)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	fileServer.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "hello world" {
		t.Errorf("got body %q, want %q", got, "hello world")
	}
}

func TestNewFileServerWithCaching_ShouldReturn304WhenClientCacheIsFresh(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello")},
	}
	fileServer := NewFileServerWithCaching(http.FS(fsys), 3600)

	// A future If-Modified-Since is always considered fresh relative to
	// the server's fixed lastModified (set at construction time).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	fileServer.ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Errorf("got status %d, want %d", w.Code, http.StatusNotModified)
	}
}

func TestNewFileServerWithCaching_ShouldIgnoreMalformedIfModifiedSince(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello")},
	}
	fileServer := NewFileServerWithCaching(http.FS(fsys), 3600)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	req.Header.Set("If-Modified-Since", "not-a-valid-http-date")
	fileServer.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d for a malformed If-Modified-Since header", w.Code, http.StatusOK)
	}
}

func TestBuildWriteIndexFn_ShouldWriteIndexUnchangedWhenNonceEmpty(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html><script>console.log(1)</script></html>`)},
	}

	writeIndexFn, err := buildWriteIndexFn(fsys)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var buf bytes.Buffer
	if err := writeIndexFn(&buf, ""); err != nil {
		t.Fatalf("expected no error writing index, got %v", err)
	}
	if buf.String() != `<html><script>console.log(1)</script></html>` {
		t.Errorf("expected index to be written unchanged, got %q", buf.String())
	}
}

func TestBuildWriteIndexFn_ShouldReturnErrorWhenIndexHtmlMissing(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{} // no index.html

	_, err := buildWriteIndexFn(fsys)
	if err == nil {
		t.Fatal("expected an error when index.html is missing from the filesystem")
	}
}

// failingWriter always fails Write, used to exercise the writeIndexFn error
// path in RegisterFrontend's handler (e.g. a client disconnecting mid-response).
type failingWriter struct {
	header http.Header
}

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

func (w *failingWriter) WriteHeader(int) {}

func TestRegisterFrontend_ShouldRegisterErrorWhenWritingIndexFails(t *testing.T) {
	t.Parallel()

	r := gin.New()

	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})

	if err := registerFrontendFS(r, testBundle()); err != nil {
		t.Fatalf("expected no error registering the frontend, got %v", err)
	}

	w := &failingWriter{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Errorf("expected exactly one error to be attached when the write fails, got %d", gotErrors)
	}
}

func TestRegisterFrontend_ShouldReturnErrFrontendNotIncludedConstant(t *testing.T) {
	t.Parallel()

	// ErrFrontendNotIncluded is shared between the exclude_frontend and
	// default build variants of RegisterFrontend (see frontend_excluded.go
	// and frontend_included.go); this only exercises the sentinel value
	// itself, which is always compiled in regardless of build tags.
	if ErrFrontendNotIncluded == nil {
		t.Fatal("expected ErrFrontendNotIncluded to be a non-nil sentinel error")
	}
	if ErrFrontendNotIncluded.Error() == "" {
		t.Error("expected ErrFrontendNotIncluded to have a non-empty message")
	}
}

// TestRegisterFrontend_ShouldRegisterAgainstTheEmbeddedBundle exercises the
// real //go:embed path — fs.Sub over frontendFS and on into
// registerFrontendFS — which the synthetic-bundle tests below deliberately
// bypass. It asserts only that the embedded dist/ is registerable and serves
// its index, never what that index contains: dist/ is an untracked build
// artifact produced by `make frontend`.
func TestRegisterFrontend_ShouldRegisterAgainstTheEmbeddedBundle(t *testing.T) {
	t.Parallel()

	r := gin.New()
	if err := RegisterFrontend(r); err != nil {
		t.Fatalf("expected no error registering the embedded frontend, got %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Errorf("got status %d serving the embedded index, want %d", w.Code, http.StatusOK)
	}
}

// testBundle is a stand-in for a built frontend: an index.html carrying
// script tags both with and without attributes (so nonce injection is
// exercised in both shapes) plus one static asset. Registering against this
// rather than the embedded dist/ keeps these tests independent of a build
// artifact that is gitignored — see registerFrontendFS.
func testBundle() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(
			`<html><head><script src="/app.js" defer></script><script>console.log(1)</script></head><body></body></html>`,
		)},
		"404.html": &fstest.MapFile{Data: []byte(`<html><body>not found</body></html>`)},
		// Content-hashed, mirroring what SvelteKit emits — the asset that
		// should get the immutable treatment.
		"_app/immutable/chunks/abc123.js": &fstest.MapFile{Data: []byte(`export const x = 1;`)},
		// Stable filename, so it must stay revalidatable.
		"robots.txt": &fstest.MapFile{Data: []byte("User-agent: *\n")},
	}
}

// newTestFrontendRouter builds a gin.Engine serving testBundle, exercising
// the default (!exclude_frontend) build variant.
func newTestFrontendRouter(t *testing.T, middlewares ...gin.HandlerFunc) *gin.Engine {
	t.Helper()

	r := gin.New()
	for _, m := range middlewares {
		r.Use(m)
	}
	if err := registerFrontendFS(r, testBundle()); err != nil {
		t.Fatalf("expected no error registering the frontend, got %v", err)
	}
	return r
}

func TestRegisterFrontend_ShouldServeIndexHtmlForUnknownPath(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/unknown/client-side-route", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("got Content-Type %q, want it to start with text/html", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("got Cache-Control %q, want %q (index.html must never be cached)", got, "no-store")
	}
}

// TestRegisterFrontend_ShouldReturn404JSONForBackendOwnedPrefixes keeps every
// prefix the server owns out of the SPA fallback. Serving index.html there
// answers a caller expecting JSON with 200 and an HTML body, so a mistyped
// API path or a wrong OIDC redirect_uri looks like it succeeded.
//
// Driven off backendOwnedPrefixes so a newly reserved prefix cannot be added
// without a case here.
func TestRegisterFrontend_ShouldReturn404JSONForBackendOwnedPrefixes(t *testing.T) {
	t.Parallel()

	for _, p := range backendOwnedPrefixes {
		t.Run(p.prefix, func(t *testing.T) {
			t.Parallel()

			r := newTestFrontendRouter(t)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/"+p.prefix+"this-does-not-exist", nil)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("got status %d, want %d", w.Code, http.StatusNotFound)
			}
			if got := w.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
				t.Errorf("got Content-Type %q, want JSON", got)
			}
			if strings.Contains(w.Body.String(), "<html") {
				t.Error("expected a JSON error body, got the SPA index.html")
			}
		})
	}
}

// TestRegisterFrontend_ShouldNotReserveOAuthPrefix pins a deliberate absence.
// The server serves /auth; /oauth appears only in frontend code inherited
// from pocket-id. Reserving it here would turn a client-side routing bug into
// a 404 that looks like a server decision.
func TestRegisterFrontend_ShouldNotReserveOAuthPrefix(t *testing.T) {
	t.Parallel()

	if _, owned := backendOwnedPrefix("oauth/login"); owned {
		t.Error("oauth/ is reserved as backend-owned, but the server serves /auth")
	}
}

func TestRegisterFrontend_ShouldRedirectTrailingSlash(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/path/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusMovedPermanently)
	}
	if got := w.Header().Get("Location"); got != "/some/path" {
		t.Errorf("got Location %q, want %q", got, "/some/path")
	}
}

func TestRegisterFrontend_ShouldServeExistingStaticAssetWithCaching(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/404.html", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); !strings.HasPrefix(got, "public, max-age=") {
		t.Errorf("got Cache-Control %q, want a public max-age directive for a static asset", got)
	}
}

func TestRegisterFrontend_ShouldCacheContentHashedAssetsAsImmutable(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/chunks/abc123.js", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("got Cache-Control %q, want the immutable directive for a content-hashed asset", got)
	}
}

// TestRegisterFrontend_ShouldNotCacheStableFilenamesAsImmutable is the guard
// that matters: an immutable response is effectively irrevocable, because
// browsers will not revalidate it even on reload. An asset whose filename
// stays the same across builds must never get one.
func TestRegisterFrontend_ShouldNotCacheStableFilenamesAsImmutable(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("got Cache-Control %q, want no immutable directive for a stable filename", got)
	}
}

func TestRegisterFrontend_ShouldServeIndexHtmlAtRootPath(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("got Content-Type %q, want it to start with text/html", got)
	}
}

func TestRegisterFrontend_ShouldInjectCSPNonceIntoScriptTags(t *testing.T) {
	t.Parallel()

	// With the CSP middleware in front (as initRouter arranges in
	// production), index.html must be served with the per-request nonce
	// injected into its <script> tags.
	r := newTestFrontendRouter(t, middleware.NewCspMiddleware().Add())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `<script nonce="`) {
		t.Error("expected script tags in index.html to carry the CSP nonce")
	}
	// The nonce in the body must match the one advertised in the CSP header.
	csp := w.Header().Get("Content-Security-Policy")
	_, rest, found := strings.Cut(csp, "'nonce-")
	if !found {
		t.Fatalf("expected a nonce in the Content-Security-Policy header, got %q", csp)
	}
	nonce, _, found := strings.Cut(rest, "'")
	if !found {
		t.Fatalf("malformed nonce directive in Content-Security-Policy header: %q", csp)
	}
	if !strings.Contains(w.Body.String(), `<script nonce="`+nonce+`"`) {
		t.Errorf("expected script tags to carry the header's nonce %q", nonce)
	}
}

func TestRegisterFrontend_ShouldInjectNonceIntoScriptTagsWithAttributes(t *testing.T) {
	t.Parallel()

	// The nonce injection must work for script tags with various attributes
	// like async, defer, type, src, etc. — the regex should handle any
	// <script...> tags, not just bare <script>.
	r := newTestFrontendRouter(t, middleware.NewCspMiddleware().Add())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	csp := w.Header().Get("Content-Security-Policy")
	_, rest, found := strings.Cut(csp, "'nonce-")
	if !found {
		t.Fatalf("expected a nonce in the Content-Security-Policy header, got %q", csp)
	}
	nonce, _, found := strings.Cut(rest, "'")
	if !found {
		t.Fatalf("malformed nonce directive in Content-Security-Policy header: %q", csp)
	}

	body := w.Body.String()

	// All script tags should have the nonce injected, even those with attributes.
	// Search for patterns like <script async ... nonce="..."> or <script src="..." nonce="...">.
	// We just verify that all <script tags end up with a nonce attribute.
	scriptCount := strings.Count(body, "<script")
	nonceCount := strings.Count(body, `nonce="`+nonce+`"`)

	if scriptCount != nonceCount {
		t.Errorf("expected all %d script tags to have nonce injected, but only %d have it", scriptCount, nonceCount)
	}
	if nonceCount == 0 {
		t.Error("expected at least one script tag with the nonce injected")
	}
}
