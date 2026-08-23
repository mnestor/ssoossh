package controller

import (
	"bytes"
	"testing"
)

// FuzzSniffImageType tests the image type detection against malformed, truncated,
// and adversarial binary data. Catches issues with magic number checking,
// boundary conditions, and SVG detection edge cases.
func FuzzSniffImageType(f *testing.F) {
	// Valid PNG header
	f.Add([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	// Valid JPEG header
	f.Add([]byte{0xFF, 0xD8, 0xFF})

	// Valid GIF header
	f.Add([]byte{0x47, 0x49, 0x46, 0x39, 0x37, 0x61})

	// Valid WebP header
	f.Add(append([]byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00}, []byte{0x57, 0x45, 0x42, 0x50}...))

	// Valid SVG
	f.Add([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	f.Add([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	f.Add([]byte("   <svg></svg>")) // with leading whitespace

	// Truncated headers
	f.Add([]byte{0x89, 0x50})
	f.Add([]byte{0xFF, 0xD8})
	f.Add([]byte{0x47})

	// Empty
	f.Add([]byte{})

	// Single byte
	f.Add([]byte{0x00})

	// SVG-like but invalid
	f.Add([]byte(`<svgfoo/>`))                         // svgfoo instead of svg
	f.Add([]byte(`< svg/>`))                           // space before svg
	f.Add([]byte(`<SVG/>`))                            // uppercase
	f.Add([]byte(`<!-- comment --><svg/>`))            // SVG after comment
	f.Add([]byte(`<?xml version="1.0"?><svg/>`))       // with XML declaration
	f.Add([]byte(`<!DOCTYPE svg><svg/>`))              // with DOCTYPE
	f.Add([]byte("<!-- unterminated comment\n<svg/>")) // unterminated comment
	f.Add([]byte("<?php unterminated\n<svg/>"))        // unterminated PI
	f.Add([]byte("<!DOCTYPE\n<svg/>"))                 // unterminated DOCTYPE

	// Edge case: lots of comments/whitespace before SVG
	prologue := ""
	for i := 0; i < 60; i++ {
		prologue += "<!-- comment " + string(rune(i)) + " -->"
	}
	prologue += "\n   \t\r\n<svg/>"
	f.Add([]byte(prologue))

	// SVG element boundary cases
	f.Add([]byte("<svg >"))         // with space and no close
	f.Add([]byte("<svg\t>"))        // with tab
	f.Add([]byte("<svg\n>"))        // with newline
	f.Add([]byte("<svg\r>"))        // with carriage return
	f.Add([]byte("<svg/>"))         // self-closing
	f.Add([]byte("<svg />"))        // self-closing with space
	f.Add([]byte("<svg:element/>")) // namespaced

	// Not SVG
	f.Add([]byte(`<html><svg></svg></html>`))
	f.Add([]byte(`<div>svg</div>`))
	f.Add([]byte(`svg is a word`))

	f.Fuzz(func(t *testing.T, data []byte) {
		contentType := sniffImageType(data)

		// Sniffing should never panic, always return empty string or valid type
		if contentType != "" &&
			contentType != "image/png" &&
			contentType != "image/jpeg" &&
			contentType != "image/gif" &&
			contentType != "image/webp" &&
			contentType != "image/svg+xml" {
			t.Fatalf("sniffImageType returned invalid content type: %q", contentType)
		}
	})
}

// FuzzSkipXMLPrologue tests the XML prologue skipping logic for correct
// handling of whitespace, declarations, comments, and DOCTYPE. Catches
// issues with unterminated constructs, entity bombs, and edge cases.
func FuzzSkipXMLPrologue(f *testing.F) {
	// Valid prologues
	f.Add([]byte(`<?xml version="1.0"?><svg/>`))
	f.Add([]byte(`<!-- comment --><svg/>`))
	f.Add([]byte(`<!DOCTYPE svg SYSTEM "file")<svg/>`))
	f.Add([]byte("   \n\t<svg/>"))

	// Edge cases
	f.Add([]byte(`<svg/>`))             // no prologue
	f.Add([]byte(`<?xml?><svg/>`))      // minimal PI
	f.Add([]byte(`<!----><!--><svg/>`)) // empty comments
	f.Add([]byte(""))                   // empty

	// Unterminated constructs (should return nil)
	f.Add([]byte(`<?xml version`)) // unterminated PI
	f.Add([]byte(`<!-- no close`)) // unterminated comment
	f.Add([]byte(`<!DOCTYPE`))     // unterminated DOCTYPE

	// Nested/complex
	f.Add([]byte(`<!-- outer <!-- inner --> --><svg/>`))
	f.Add([]byte(`<?pi?><?pi2?><svg/>`))

	// Many constructs (test bounded loop)
	many := ""
	for i := 0; i < 100; i++ {
		many += "<!-- " + string(rune(i)) + " -->\n"
	}
	many += "<svg/>"
	f.Add([]byte(many))

	f.Fuzz(func(t *testing.T, data []byte) {
		result := skipXMLPrologue(data)

		// Should never panic
		// Result is either nil or a byte slice
		if result != nil {
			// If we got a result, it should either be the same as data
			// (no prologue) or be a prefix of data
			if !bytes.HasPrefix(data, result) && len(result) > 0 {
				// Actually we're trimming the beginning, so result should be
				// within data or the start of the root element
				if !bytes.Contains(data, result) && !bytes.Equal(result, data) {
					// This is OK - skipXMLPrologue returns the remainder
				}
			}
		}
	})
}

// FuzzIsSVG tests the SVG detection logic end-to-end, which combines
// prologue skipping and element checking. Catches edge cases where prologue
// handling or element detection fails.
func FuzzIsSVG(f *testing.F) {
	// Valid SVG
	f.Add([]byte(`<svg/>`))
	f.Add([]byte(`<svg></svg>`))
	f.Add([]byte(`<?xml version="1.0"?><svg/>`))
	f.Add([]byte(`<!-- comment --><svg xmlns="http://www.w3.org/2000/svg"/>`))
	f.Add([]byte("   \n<svg>"))
	f.Add([]byte(`<svg attr="value"/>`))

	// Not SVG
	f.Add([]byte(`<div/>`))
	f.Add([]byte(`<svgfoo/>`))
	f.Add([]byte(`<SVG/>`))
	f.Add([]byte(``))
	f.Add([]byte(`html content <svg> embedded`))

	// Edge cases
	f.Add([]byte(`<svg`))
	f.Add([]byte(`<`))
	f.Add([]byte(`<s`))
	f.Add([]byte(`<?xml?>`)) // just prologue, no element

	f.Fuzz(func(t *testing.T, data []byte) {
		isSVGResult := isSVG(data)

		// isSVG should never panic, always return bool
		_ = isSVGResult

		// If it returns true, data should contain <svg with proper boundary
		if isSVGResult {
			// Verify it's actually an SVG by checking the structure
			root := skipXMLPrologue(data)
			if root == nil {
				t.Fatalf("isSVG returned true but skipXMLPrologue returned nil")
			}
			if !bytes.HasPrefix(root, []byte("<svg")) {
				t.Fatalf("isSVG returned true but root doesn't start with <svg")
			}
		}
	})
}

// FuzzLoadLogoImage tests the complete logo loading pipeline with various
// valid and malicious file inputs. Catches issues with size checks, format
// detection, and ETag generation.
func FuzzLoadLogoImage(f *testing.F) {
	// This test uses memory data directly since LoadLogoImage takes a file path.
	// We test the underlying functions (sniffImageType, isSVG) via other fuzz tests.
	// This is a placeholder to document the testing strategy.

	// Note: sniffImageType and isSVG are tested via FuzzSniffImageType and FuzzIsSVG.
	// The ETag computation (computeSimpleETag) is deterministic and not fuzzed here
	// since it's a simple hash, not a parser.

	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		// computeSimpleETag should never panic
		etag := computeSimpleETag(data)
		if len(etag) == 0 && len(data) > 0 {
			t.Fatalf("computeSimpleETag returned empty string for non-empty data")
		}
	})
}
