//go:build !exclude_frontend

package frontend

// Adapted from https://github.com/pocket-id/pocket-id/blob/main/backend/frontend/frontend_included.go

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
)

// frontendFS holds the built frontend assets, embedded at compile time from
// the SvelteKit build output in dist/.
//
// dist/ is not tracked — its filenames are content hashes. Run `make
// frontend` before building or testing this package, or the embed below
// matches nothing and the build fails with "pattern all:dist/*: no matching
// files found". Build with -tags exclude_frontend to skip it entirely.
//
//go:embed all:dist/*
var frontendFS embed.FS

// scriptTagRe matches <script with optional attributes and closing >.
// Captures the opening tag and attributes, preserving them in group 1.
var scriptTagRe = regexp.MustCompile(`<script([^>]*)>`)

const (
	// staticCacheMaxAge applies to assets whose filenames are stable across
	// builds (robots.txt, _app/env.js, anything the app references by a
	// fixed name), so a client has to revalidate to notice a change.
	staticCacheMaxAge = 24 * time.Hour

	// immutableCacheMaxAge applies to content-hashed assets. A year is the
	// conventional ceiling; combined with the immutable directive it tells
	// browsers not to revalidate even on an explicit reload.
	immutableCacheMaxAge = 365 * 24 * time.Hour
)

// immutableAssetPrefixes are the paths whose contents can never change,
// because the filename *is* a content hash — SvelteKit emits everything
// under _app/immutable/ that way, so a changed file is a changed URL. In a
// typical build that is the overwhelming majority of the bundle.
//
// Deliberately narrow. A year-long immutable response is effectively
// irrevocable, since browsers will not revalidate it even when the user
// reloads, so a path only belongs here if its filename carries a
// content-hash guarantee. Do not add directories that merely happen to
// change rarely.
var immutableAssetPrefixes = []string{"_app/immutable/"}

// isImmutableAsset reports whether path (relative, no leading slash) is
// content-hashed and therefore safe to cache indefinitely.
func isImmutableAsset(path string) bool {
	for _, prefix := range immutableAssetPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// backendOwnedPrefixes are the URL prefixes the server owns rather than the
// SPA. An unmatched path under one of them is a 404, not a route for the
// client-side router to try.
//
// This only affects paths with no registered handler — gin calls NoRoute
// only when nothing else matched — so it catches typos, stale links, and
// misconfiguration. That is exactly when falling through to index.html hurts
// most: the caller gets 200 and an HTML body instead of an error, so a
// mistyped API path or a wrong OIDC redirect_uri looks like it worked.
//
// Note /oauth is deliberately absent: the server serves /auth (see
// server/bootstrap/router.go). Any /oauth reference elsewhere is inherited
// from pocket-id and is a bug in that caller, not a prefix to reserve here.
var backendOwnedPrefixes = []struct {
	prefix  string
	message string
}{
	{"api/", "API endpoint not found"},
	{"auth/", "auth endpoint not found"},
	{".well-known/", "well-known endpoint not found"},
}

// backendOwnedPrefix reports whether path (relative, no leading slash) falls
// under a prefix the server owns, along with the error message to return.
func backendOwnedPrefix(path string) (message string, owned bool) {
	for _, p := range backendOwnedPrefixes {
		if strings.HasPrefix(path, p.prefix) {
			return p.message, true
		}
	}
	return "", false
}

// buildWriteIndexFn reads index.html out of fsys (rooted so "index.html" is
// a top-level entry, e.g. via fs.Sub) and returns a function that writes it
// to w, injecting nonce into each <script> tag when set. It's built once, so
// that reload doesn't require re-parsing the file on every request.
func buildWriteIndexFn(fsys fs.FS) (func(w io.Writer, nonce string) error, error) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil, fmt.Errorf("failed to read index.html: %w", err)
	}

	return func(w io.Writer, nonce string) (err error) {
		// If there's no nonce, write the index as-is
		if nonce == "" {
			_, err = w.Write(index)
			return err
		}

		// Add nonce to all <script> tags, whether they have attributes or not.
		// ReplaceAll inserts nonce before the closing > of each script tag.
		nonceAttr := fmt.Sprintf(` nonce="%s"`, nonce)
		modified := scriptTagRe.ReplaceAll(
			index,
			[]byte(`<script$1`+nonceAttr+`>`),
		)

		_, err = w.Write(modified)
		return err
	}, nil
}

// RegisterFrontend serves the embedded frontend bundle as router's
// catch-all route: static assets are served with caching, unknown paths
// (and API 404s aside) fall back to index.html for client-side routing,
// and index.html itself is always served fresh with a per-request CSP
// nonce injected into its script tags.
func RegisterFrontend(router *gin.Engine) error {
	// fs.Sub only fails on a malformed dir argument; "dist" is a hardcoded
	// literal, so this is unreachable and excluded from coverage
	// (exclude-from-coverage.txt).
	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		return fmt.Errorf("failed to create sub FS: %w", err)
	}

	return registerFrontendFS(router, distFS)
}

// registerFrontendFS is RegisterFrontend's body, parameterized over the asset
// filesystem. The split exists so tests can register a synthetic bundle:
// dist/ is an untracked build artifact whose contents change with every
// frontend build, so asserting against it would make these tests depend on
// which build happens to be on disk.
func registerFrontendFS(router *gin.Engine, distFS fs.FS) error {
	// buildWriteIndexFn's own error path is unit tested directly against a
	// synthetic fs.FS (see frontend_test.go); reaching it here would require
	// index.html to be missing from the bundle, which `make frontend`
	// guarantees against. Excluded from coverage
	// (exclude-from-coverage.txt).
	writeIndexFn, err := buildWriteIndexFn(distFS)
	if err != nil {
		return fmt.Errorf("failed to build index.html writer: %w", err)
	}

	fileServer := NewFileServerWithCaching(http.FS(distFS), int(staticCacheMaxAge.Seconds()))
	immutableFileServer := NewImmutableFileServerWithCaching(http.FS(distFS), int(immutableCacheMaxAge.Seconds()))

	router.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")

		if strings.HasSuffix(path, "/") {
			// Redirect to the slash-trimmed path, but build the target from
			// the cleaned path with a single forced leading slash. Using
			// c.Request.URL.String() directly turned a protocol-relative
			// request like //evil.com/ into "Location: //evil.com", which a
			// browser follows off-site — an open redirect. Collapsing the
			// leading slashes keeps the redirect on this origin; the query
			// string is preserved.
			target := "/" + strings.Trim(path, "/")
			if c.Request.URL.RawQuery != "" {
				target += "?" + c.Request.URL.RawQuery
			}
			c.Redirect(http.StatusMovedPermanently, target)
			return
		}

		// Paths the SPA must never answer for — see backendOwnedPrefixes.
		if msg, owned := backendOwnedPrefix(path); owned {
			c.JSON(http.StatusNotFound, gin.H{"error": msg})
			return
		}

		// If path is / or does not exist, serve index.html
		if path == "" {
			path = "index.html"
		} else if _, err := fs.Stat(distFS, path); os.IsNotExist(err) {
			path = "index.html"
		}

		if path == "index.html" {
			nonce := middleware.GetCSPNonce(c)

			// Do not cache the HTML shell, as it embeds a per-request nonce
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.Header("Cache-Control", "no-store")
			c.Status(http.StatusOK)
			if err := writeIndexFn(c.Writer, nonce); err != nil {
				_ = c.Error(fmt.Errorf("failed to write index.html file: %w", err)) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			}
			return
		}

		// Serve other static assets with caching
		c.Request.URL.Path = "/" + path
		if isImmutableAsset(path) {
			immutableFileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	return nil
}

// FileServerWithCaching wraps http.FileServer to add caching headers.
type FileServerWithCaching struct {
	root                    http.FileSystem
	lastModified            time.Time
	cacheMaxAge             int
	lastModifiedHeaderValue string
	cacheControlHeaderValue string
}

// NewFileServerWithCaching creates a FileServerWithCaching serving files
// from root, advertising maxAge (in seconds) via Cache-Control and stamping
// Last-Modified with the time the server started.
func NewFileServerWithCaching(root http.FileSystem, maxAge int) *FileServerWithCaching {
	return &FileServerWithCaching{
		root:                    root,
		lastModified:            time.Now(),
		cacheMaxAge:             maxAge,
		lastModifiedHeaderValue: time.Now().UTC().Format(http.TimeFormat),
		cacheControlHeaderValue: fmt.Sprintf("public, max-age=%d", maxAge),
	}
}

// NewImmutableFileServerWithCaching is NewFileServerWithCaching for
// content-hashed assets: identical behavior, but the Cache-Control it
// advertises carries the immutable directive so browsers skip revalidation
// entirely, including on reload. Only for paths where the filename is the
// version — see immutableAssetPrefixes.
func NewImmutableFileServerWithCaching(root http.FileSystem, maxAge int) *FileServerWithCaching {
	f := NewFileServerWithCaching(root, maxAge)
	f.cacheControlHeaderValue += ", immutable"
	return f
}

// ServeHTTP responds 304 Not Modified if the client's cached copy is still
// fresh relative to f.lastModified, otherwise it serves the file with
// caching headers set.
func (f *FileServerWithCaching) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if the client has a cached version
	if ifModifiedSince := r.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
		ifModifiedSinceTime, err := time.Parse(http.TimeFormat, ifModifiedSince)
		if err == nil && f.lastModified.Before(ifModifiedSinceTime.Add(1*time.Second)) {
			// Client's cached version is up to date
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	w.Header().Set("Last-Modified", f.lastModifiedHeaderValue)
	w.Header().Set("Cache-Control", f.cacheControlHeaderValue)

	http.FileServer(f.root).ServeHTTP(w, r)
}
