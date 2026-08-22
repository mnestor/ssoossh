package controller

import (
	"bytes"
	"fmt"
	"os"
)

// logoImage holds the bytes and metadata of a logo image file. It is loaded
// and validated at startup so failures are immediately visible to the operator.
type logoImage struct {
	bytes       []byte
	contentType string
	etag        string
}

// maxLogoSize is the maximum acceptable logo file size: 1 MB. This prevents
// accidents like pointing at a huge image or a non-image file. 1 MB is
// well above typical SVG and PNG logos but well below problematic sizes.
const maxLogoSize = 1024 * 1024

// LoadLogoImage reads and validates the logo file from the filesystem.
// It returns nil if path is empty. Returns a startup error if the file is
// missing, unreadable, too large, or not a recognized image format.
//
// Recognized formats: PNG, JPEG, GIF, WebP, SVG.
// SVG is permitted but served with Content-Security-Policy: default-src 'none'
// to prevent script execution when the file is accessed directly, since <script>
// tags in SVG will execute in the origin context outside of an <img> tag.
// This is an acceptable trade-off: SVG is by far the most common logo format,
// and the CSP header on the direct endpoint prevents the XSS while <img>
// inside a page uses the broader page CSP anyway.
func LoadLogoImage(path string) (*logoImage, error) {
	if path == "" {
		return nil, nil
	}

	// Read the file
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read logo file %q: %w", path, err)
	}

	// Check size
	if len(bytes) > maxLogoSize {
		return nil, fmt.Errorf("logo file %q is too large (%d bytes, max %d)", path, len(bytes), maxLogoSize)
	}

	if len(bytes) == 0 {
		return nil, fmt.Errorf("logo file %q is empty", path)
	}

	// Sniff content type by magic bytes
	contentType := sniffImageType(bytes)
	if contentType == "" {
		return nil, fmt.Errorf("logo file %q is not a recognized image format (PNG, JPEG, GIF, WebP, or SVG)", path)
	}

	// Compute a simple ETag as hash of the bytes. We use a simple approach
	// since the logo is small and fixed at startup: just the length and
	// first/last bytes. In practice, an operator rarely changes the logo,
	// but when they do, this changes, so browsers re-fetch.
	etag := computeSimpleETag(bytes)

	return &logoImage{
		bytes:       bytes,
		contentType: contentType,
		etag:        etag,
	}, nil
}

// sniffImageType detects image format by magic bytes. Returns the appropriate
// Content-Type or empty string if not recognized.
func sniffImageType(data []byte) string {
	if len(data) < 4 {
		return ""
	}

	// PNG: 89 50 4E 47
	if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
		return "image/png"
	}

	// JPEG: FF D8 FF
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
		return "image/jpeg"
	}

	// GIF: 47 49 46
	if bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46}) {
		return "image/gif"
	}

	// WebP: RIFF ... WEBP
	if bytes.HasPrefix(data, []byte{0x52, 0x49, 0x46, 0x46}) && len(data) >= 12 &&
		bytes.Equal(data[8:12], []byte{0x57, 0x45, 0x42, 0x50}) {
		return "image/webp"
	}

	// SVG has no magic number, being XML text, so the check is structural:
	// the first element must actually be <svg>. Merely containing the string
	// "svg" is not enough — an HTML page mentioning it would pass, and the
	// operator would get a silently broken image instead of the startup
	// error this validation exists to produce.
	if isSVG(data) {
		return "image/svg+xml"
	}

	return ""
}

// skipXMLPrologue returns data with any leading whitespace, XML declaration,
// DOCTYPE, and comments removed, so the caller sees the root element. It
// returns nil if a prologue construct is left unterminated, which means the
// file is malformed and cannot be trusted as an image either way.
func skipXMLPrologue(data []byte) []byte {
	// Bounded rather than `for {}`: a malformed file should fail, not spin.
	for range 64 {
		data = bytes.TrimLeft(data, " \t\r\n")

		var closer []byte
		switch {
		case bytes.HasPrefix(data, []byte("<?")):
			closer = []byte("?>")
		case bytes.HasPrefix(data, []byte("<!--")):
			closer = []byte("-->")
		case bytes.HasPrefix(data, []byte("<!")):
			closer = []byte(">")
		default:
			return data
		}

		end := bytes.Index(data, closer)
		if end < 0 {
			return nil
		}
		data = data[end+len(closer):]
	}
	return nil
}

// isSVG reports whether data's first XML element is <svg>. SVG has no magic
// number, being XML text, so this is the structural equivalent: merely
// containing the string "svg" is not enough, since an HTML page mentioning
// it would pass and the operator would get a silently broken image instead
// of a startup error.
func isSVG(data []byte) bool {
	root := skipXMLPrologue(data)
	if !bytes.HasPrefix(root, []byte("<svg")) {
		return false
	}

	// Require a delimiter after the name so <svgfoo> is not mistaken for it.
	after := root[len("<svg"):]
	if len(after) == 0 {
		return false
	}
	switch after[0] {
	case ' ', '\t', '\r', '\n', '>', '/':
		return true
	default:
		return false
	}
}

// computeSimpleETag generates a weak ETag for the logo. Since the logo is
// small and immutable at runtime, we use a simple approach: length plus
// a hash of the first, middle, and last bytes.
func computeSimpleETag(data []byte) string {
	if len(data) == 0 {
		return `W/"0"`
	}
	if len(data) == 1 {
		return fmt.Sprintf(`W/"%d-%x"`, len(data), data[0])
	}
	// Simple hash: length + first + middle + last byte. Not cryptographically
	// strong, but sufficient to invalidate caches when the file changes.
	mid := len(data) / 2
	h := len(data) ^ int(data[0]) ^ int(data[mid]) ^ (int(data[len(data)-1]) << 8)
	return fmt.Sprintf(`W/"%d-%x"`, len(data), h)
}
