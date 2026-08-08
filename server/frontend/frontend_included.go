//go:build !exclude_frontend

package frontend

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
// the Gatsbyjs build output in dist/.
//
//go:embed all:dist/*
var frontendFS embed.FS

// scriptTagRe matches <script with optional attributes and closing >.
// Captures the opening tag and attributes, preserving them in group 1.
var scriptTagRe = regexp.MustCompile(`<script([^>]*)>`)

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

	// buildWriteIndexFn's own error path is unit tested directly against a
	// synthetic fs.FS (see frontend_test.go); reaching it here would require
	// dist/index.html to be missing from the embedded bundle, which the
	// checked-in frontend build guarantees against. Excluded from coverage
	// (exclude-from-coverage.txt).
	writeIndexFn, err := buildWriteIndexFn(distFS)
	if err != nil {
		return fmt.Errorf("failed to build index.html writer: %w", err)
	}

	cacheMaxAge := time.Hour * 24
	fileServer := NewFileServerWithCaching(http.FS(distFS), int(cacheMaxAge.Seconds()))

	router.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")

		if strings.HasSuffix(path, "/") {
			c.Redirect(http.StatusMovedPermanently, strings.TrimRight(c.Request.URL.String(), "/"))
			return
		}

		if strings.HasPrefix(path, "api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
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
