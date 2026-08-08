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

	if err := RegisterFrontend(r); err != nil {
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

// newTestFrontendRouter builds a gin.Engine with the real embedded frontend
// registered, exercising the default (!exclude_frontend) build variant
// against the actual server/frontend/dist bundle checked into this repo.
func newTestFrontendRouter(t *testing.T) *gin.Engine {
	t.Helper()

	r := gin.New()
	if err := RegisterFrontend(r); err != nil {
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

func TestRegisterFrontend_ShouldReturn404JSONForUnknownAPIPath(t *testing.T) {
	t.Parallel()

	r := newTestFrontendRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/this-does-not-exist", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusNotFound)
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected JSON error body, got %q", w.Body.String())
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
	r := gin.New()
	r.Use(middleware.NewCspMiddleware().Add())
	if err := RegisterFrontend(r); err != nil {
		t.Fatalf("expected no error registering the frontend, got %v", err)
	}

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
	r := gin.New()
	r.Use(middleware.NewCspMiddleware().Add())
	if err := RegisterFrontend(r); err != nil {
		t.Fatalf("expected no error registering the frontend, got %v", err)
	}

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
