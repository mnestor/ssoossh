package controller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLogoImage(t *testing.T) {
	tests := []struct {
		name          string
		filePath      string
		fileContent   []byte
		shouldFail    bool
		expectedError string
	}{
		{
			name:     "should return nil when path is empty",
			filePath: "",
			shouldFail: false,
		},
		{
			name:          "should fail when file does not exist",
			filePath:      "/nonexistent/logo.png",
			shouldFail:    true,
			expectedError: "failed to read logo file",
		},
		{
			name:          "should fail when file is empty",
			filePath:      "empty.png",
			fileContent:   []byte{},
			shouldFail:    true,
			expectedError: "is empty",
		},
		{
			name:     "should load valid PNG",
			filePath: "valid.png",
			// PNG magic bytes
			fileContent: append([]byte{0x89, 0x50, 0x4E, 0x47}, make([]byte, 100)...),
			shouldFail:  false,
		},
		{
			name:     "should load valid JPEG",
			filePath: "valid.jpg",
			// JPEG magic bytes
			fileContent: append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, 100)...),
			shouldFail:  false,
		},
		{
			name:     "should load valid GIF",
			filePath: "valid.gif",
			// GIF magic bytes
			fileContent: append([]byte{0x47, 0x49, 0x46}, make([]byte, 100)...),
			shouldFail:  false,
		},
		{
			name:     "should load valid WebP",
			filePath: "valid.webp",
			// WebP magic bytes: RIFF ... WEBP
			fileContent: append([]byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50}, make([]byte, 100)...),
			shouldFail:  false,
		},
		{
			name:     "should load valid SVG",
			filePath: "valid.svg",
			// SVG: <svg> tag
			fileContent: []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"),
			shouldFail:  false,
		},
		{
			name:          "should fail when file is not an image",
			filePath:      "notimage.txt",
			fileContent:   []byte("This is just text"),
			shouldFail:    true,
			expectedError: "not a recognized image format",
		},
		{
			name:          "should fail when file is too large",
			filePath:      "toolarge.png",
			fileContent:   append([]byte{0x89, 0x50, 0x4E, 0x47}, make([]byte, maxLogoSize+1)...),
			shouldFail:    true,
			expectedError: "too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file if content is provided
			if tt.fileContent != nil {
				tmpFile, err := os.CreateTemp(t.TempDir(), filepath.Base(tt.filePath))
				if err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				defer tmpFile.Close()

				if err := os.WriteFile(tmpFile.Name(), tt.fileContent, 0600); err != nil {
					t.Fatalf("failed to write temp file: %v", err)
				}

				tt.filePath = tmpFile.Name()
			}

			img, err := LoadLogoImage(tt.filePath)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				if tt.expectedError != "" && !contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if tt.filePath == "" && img != nil {
					t.Errorf("expected nil image for empty path, got %v", img)
				}
				if tt.filePath != "" && img == nil {
					t.Errorf("expected image to be loaded, got nil")
				}
				if img != nil {
					if img.contentType == "" {
						t.Errorf("expected content type to be set")
					}
					if img.etag == "" {
						t.Errorf("expected etag to be set")
					}
					if len(img.bytes) == 0 {
						t.Errorf("expected bytes to be loaded")
					}
				}
			}
		})
	}
}

func TestSniffImageType(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		expectedType string
	}{
		{
			name:         "should detect PNG",
			data:         []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			expectedType: "image/png",
		},
		{
			name:         "should detect JPEG",
			data:         []byte{0xFF, 0xD8, 0xFF, 0xE0},
			expectedType: "image/jpeg",
		},
		{
			name:         "should detect GIF",
			data:         []byte{0x47, 0x49, 0x46, 0x39, 0x39, 0x61},
			expectedType: "image/gif",
		},
		{
			name:         "should detect WebP",
			data:         []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50},
			expectedType: "image/webp",
		},
		{
			name:         "should detect SVG",
			data:         []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"),
			expectedType: "image/svg+xml",
		},
		{
			name:         "should reject non-image",
			data:         []byte("This is text"),
			expectedType: "",
		},
		{
			name:         "should reject empty",
			data:         []byte{},
			expectedType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sniffImageType(tt.data)
			if got != tt.expectedType {
				t.Errorf("expected %q, got %q", tt.expectedType, got)
			}
		})
	}
}

func TestComputeSimpleETag(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		shouldFail bool
	}{
		{
			name:   "should compute etag for data",
			data:   []byte("test data"),
			shouldFail: false,
		},
		{
			name:   "should compute etag for single byte",
			data:   []byte("x"),
			shouldFail: false,
		},
		{
			name:   "should compute etag for empty",
			data:   []byte{},
			shouldFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			etag1 := computeSimpleETag(tt.data)
			etag2 := computeSimpleETag(tt.data)

			if etag1 != etag2 {
				t.Errorf("etag should be deterministic: %q != %q", etag1, etag2)
			}

			if etag1 == "" {
				t.Errorf("etag should not be empty")
			}

			// Verify it's a weak etag
			if !contains(etag1, "W/") {
				t.Errorf("etag should be weak (contain W/): %q", etag1)
			}

			// Changing data should change etag
			if len(tt.data) > 0 {
				modifiedData := make([]byte, len(tt.data))
				copy(modifiedData, tt.data)
				modifiedData[0] = modifiedData[0] + 1
				etag3 := computeSimpleETag(modifiedData)
				if etag1 == etag3 {
					t.Errorf("etag should change when data changes: %q == %q", etag1, etag3)
				}
			}
		})
	}
}

// contains is a simple helper to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
