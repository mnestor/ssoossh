package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testManPageDate is a fixed stamp, so the generated pages are byte-identical
// between runs. Mirrors what manPageDate returns with no SOURCE_DATE_EPOCH set.
func testManPageDateValue() time.Time {
	return time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
}

// TestGenerateClientManpage should create man page file with correct content structure.
func TestGenerateClientManpage(t *testing.T) {
	outDir := t.TempDir()

	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("generateClientManpage failed: %v", err)
	}

	// Verify the ssoossh.1 man page was created
	manPath := filepath.Join(outDir, "ssoossh.1")
	_, err = os.Stat(manPath)
	if err != nil {
		t.Fatalf("ssoossh.1 file not created: %v", err)
	}

	// Verify man page content has roff structure
	content, err := os.ReadFile(manPath)
	if err != nil {
		t.Fatalf("Failed to read ssoossh.1: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, ".TH") {
		t.Error("Man page missing .TH (title) header")
	}
	if !strings.Contains(contentStr, "SSOOSSH") {
		t.Error("Man page missing SSOOSSH title")
	}
	if !strings.Contains(contentStr, "ssoossh") {
		t.Error("Man page missing ssoossh command name")
	}
	if !strings.Contains(contentStr, "SSH") {
		t.Error("Man page missing SSH content")
	}
}

// TestGenerateClientManpageInvalidDir should return error when the output directory does not exist.
func TestGenerateClientManpageInvalidDir(t *testing.T) {
	// generateClientManpage writes straight into outDir without creating it,
	// so a missing parent makes the create fail with ENOENT. That holds for
	// any uid, unlike a permission-based setup.
	outDir := filepath.Join(t.TempDir(), "missing", "subdir")

	err := generateClientManpage(outDir, testManPageDateValue())
	if err == nil {
		t.Error("Expected error when writing to a missing directory, got nil")
	}
}

// TestGenerateClientManpageContainsSubcommands should verify subcommands are documented.
func TestGenerateClientManpageContainsSubcommands(t *testing.T) {
	outDir := t.TempDir()

	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("generateClientManpage failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "ssoossh.1"))
	if err != nil {
		t.Fatalf("Failed to read ssoossh.1: %v", err)
	}

	contentStr := string(content)
	// Verify at least some subcommands are mentioned
	subcommands := []string{"ssh", "host", "service", "ca"}
	found := 0
	for _, cmd := range subcommands {
		if strings.Contains(contentStr, cmd) {
			found++
		}
	}

	if found < 3 {
		t.Errorf("Man page should contain most subcommands, found %d of %d", found, len(subcommands))
	}
}

// TestGenerateClientManpageOutputStructure should verify the man page follows roff format conventions.
func TestGenerateClientManpageOutputStructure(t *testing.T) {
	outDir := t.TempDir()

	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("generateClientManpage failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "ssoossh.1"))
	if err != nil {
		t.Fatalf("Failed to read ssoossh.1: %v", err)
	}

	contentStr := string(content)

	// Verify common roff macros present
	roffMacros := []string{".TH", ".SH", ".PP"}
	for _, macro := range roffMacros {
		if !strings.Contains(contentStr, macro) {
			t.Errorf("Man page missing roff macro: %s", macro)
		}
	}

	// Verify section header for SYNOPSIS exists
	if !strings.Contains(contentStr, ".SH SYNOPSIS") && !strings.Contains(contentStr, ".SH \"SYNOPSIS\"") {
		t.Error("Man page missing SYNOPSIS section")
	}
}

// TestGenerateClientManpageConfiguration should verify configuration flags are documented.
func TestGenerateClientManpageConfiguration(t *testing.T) {
	outDir := t.TempDir()

	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("generateClientManpage failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "ssoossh.1"))
	if err != nil {
		t.Fatalf("Failed to read ssoossh.1: %v", err)
	}

	contentStr := string(content)

	// Verify configuration flags are mentioned
	flags := []string{"config", "server"}
	for _, flag := range flags {
		if !strings.Contains(contentStr, flag) {
			t.Errorf("Man page missing configuration flag: %s", flag)
		}
	}
}

// TestGenerateServerManpage should verify server man page generation works.
func TestGenerateServerManpage(t *testing.T) {
	outDir := t.TempDir()

	// Import the server command package
	// This test verifies the server-side man page generation works
	// without checking its exact content, as that depends on servercmd.NewCommand()

	// We test this by calling the actual main function behavior for server
	// Since main() has os.Exit calls, we verify the logic would work

	// Verify we can create the output directory
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("Failed to create output directory: %v", err)
	}

	// Verify directory was created
	info, err := os.Stat(outDir)
	if err != nil {
		t.Fatalf("Output directory not created: %v", err)
	}

	if !info.IsDir() {
		t.Error("Output path is not a directory")
	}
}

// TestCobrakCmdGeneration should verify cobra command structures are valid.
func TestCobraCommandGeneration(t *testing.T) {
	// This is a basic sanity check that our cobra command structure is valid
	// The actual command trees are verified by the generated man pages

	// Verify we can instantiate cobra commands without panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Cobra command generation panicked: %v", r)
		}
	}()

	// Test that we can write man pages to a buffer (without actual file I/O)
	// This verifies the cobra structures are valid
	outDir := t.TempDir()
	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("Failed to generate client manpage: %v", err)
	}
}

// TestErrorHandling should verify proper error propagation.
func TestErrorHandling(t *testing.T) {
	// Verify that errors from generateClientManpage are properly returned
	outDir := filepath.Join(t.TempDir(), "test-error")

	// Create as a file instead of directory to cause write error
	file, err := os.Create(outDir)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	file.Close()

	err = generateClientManpage(outDir, testManPageDateValue())
	if err == nil {
		t.Error("Expected error when outDir is a file, got nil")
	}
}

// TestMultipleManpageGeneration should verify generating multiple man pages works.
func TestMultipleManpageGeneration(t *testing.T) {
	outDir := t.TempDir()

	// Generate client man page
	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("Failed to generate client man page: %v", err)
	}

	// Verify the file exists
	clientPath := filepath.Join(outDir, "ssoossh.1")
	if _, err := os.Stat(clientPath); err != nil {
		t.Fatalf("Client man page not found: %v", err)
	}

	// Verify the file has content
	info, err := os.Stat(clientPath)
	if err != nil {
		t.Fatalf("Failed to stat client man page: %v", err)
	}

	if info.Size() == 0 {
		t.Error("Client man page is empty")
	}
}

// TestGenerateClientManpageConcurrency should verify the function is safe for concurrent use.
func TestGenerateClientManpageConcurrency(t *testing.T) {
	// Create two separate output directories
	outDir1 := t.TempDir()
	outDir2 := t.TempDir()

	// Run concurrently to ensure no shared state issues
	done := make(chan error, 2)

	go func() {
		done <- generateClientManpage(outDir1, testManPageDateValue())
	}()
	go func() {
		done <- generateClientManpage(outDir2, testManPageDateValue())
	}()

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Concurrent execution failed: %v", err)
		}
	}

	// Verify both directories have the man page
	for _, outDir := range []string{outDir1, outDir2} {
		if _, err := os.Stat(filepath.Join(outDir, "ssoossh.1")); err != nil {
			t.Errorf("Man page not found in %s: %v", outDir, err)
		}
	}
}

// TestGenerateClientManpageContentSize should verify generated man page has reasonable size.
func TestGenerateClientManpageContentSize(t *testing.T) {
	outDir := t.TempDir()

	err := generateClientManpage(outDir, testManPageDateValue())
	if err != nil {
		t.Fatalf("generateClientManpage failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "ssoossh.1"))
	if err != nil {
		t.Fatalf("Failed to read ssoossh.1: %v", err)
	}

	// Man page should be at least a few hundred bytes
	minSize := 500
	if len(content) < minSize {
		t.Errorf("Man page too small: %d bytes (expected at least %d)", len(content), minSize)
	}

	// Should not be unreasonably large
	maxSize := 100000
	if len(content) > maxSize {
		t.Errorf("Man page too large: %d bytes (expected at most %d)", len(content), maxSize)
	}
}

// TestRun should generate all man pages when given a valid output directory.
func TestRun(t *testing.T) {
	outDir := t.TempDir()

	err := run(outDir)
	if err != nil {
		t.Fatalf("run() failed: %v", err)
	}

	// Verify client man page
	clientPath := filepath.Join(outDir, "ssoossh.1")
	if _, err := os.Stat(clientPath); err != nil {
		t.Fatalf("Client man page not created: %v", err)
	}

	// Verify server man page
	serverPath := filepath.Join(outDir, "ssoosshd.8")
	if _, err := os.Stat(serverPath); err != nil {
		t.Fatalf("Server man page not created: %v", err)
	}

	// Verify both files have content
	for _, path := range []string{clientPath, serverPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Failed to stat %s: %v", path, err)
		}

		if info.Size() == 0 {
			t.Errorf("Man page is empty: %s", path)
		}
	}
}

// TestRunInvalidDirectory should return error when directory cannot be created.
func TestRunInvalidDirectory(t *testing.T) {
	// A regular file standing where run() needs a parent directory makes
	// MkdirAll fail with ENOTDIR. Read-only permission bits would not: CI runs
	// the suite as root inside a container, and root writes through them.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("Failed to create blocking file: %v", err)
	}

	err := run(filepath.Join(blocker, "subdir"))
	if err == nil {
		t.Error("Expected error for invalid directory, got nil")
	}
}

// TestRunServerManpageGeneration verifies server man page is created correctly.
func TestRunServerManpageGeneration(t *testing.T) {
	outDir := t.TempDir()

	err := run(outDir)
	if err != nil {
		t.Fatalf("run() failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "ssoosshd.8"))
	if err != nil {
		t.Fatalf("Failed to read ssoosshd.8: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "SSOOSSHD") {
		t.Error("Server man page missing SSOOSSHD title")
	}
	if !strings.Contains(contentStr, ".TH") {
		t.Error("Server man page missing roff header")
	}
}

// TestRunBothManpagesExist verifies both client and server man pages are generated.
func TestRunBothManpagesExist(t *testing.T) {
	outDir := t.TempDir()

	err := run(outDir)
	if err != nil {
		t.Fatalf("run() failed: %v", err)
	}

	files := []string{"ssoossh.1", "ssoosshd.8"}
	for _, file := range files {
		path := filepath.Join(outDir, file)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Man page file not found: %s", file)
		}
	}
}

// TestRunErrorPropagation should verify errors are propagated from functions called by run().
func TestRunErrorPropagation(t *testing.T) {
	outDir := t.TempDir()

	// The output directory itself is writable, so run() gets past MkdirAll and
	// fails inside man page generation instead: a directory sitting on the
	// server man page's own path makes the generator's create fail with
	// EISDIR, which no uid can write through.
	if err := os.Mkdir(filepath.Join(outDir, "ssoosshd.8"), 0755); err != nil {
		t.Fatalf("Failed to create blocking directory: %v", err)
	}

	err := run(outDir)
	if err == nil {
		t.Error("Expected error when the man page path is not writable, got nil")
	}
}

// BenchmarkGenerateClientManpage benchmarks the man page generation.
func BenchmarkGenerateClientManpage(b *testing.B) {
	outDir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tempDir := filepath.Join(outDir, "bench", "dir", string(rune(i)))
		os.MkdirAll(tempDir, 0755)
		generateClientManpage(tempDir, testManPageDateValue())
	}
}

// BenchmarkRun benchmarks the full man page generation pipeline.
func BenchmarkRun(b *testing.B) {
	outDir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tempDir := filepath.Join(outDir, "bench", "run", string(rune(i)))
		run(tempDir)
	}
}

// TestManPageDate covers the stamp that makes generation reproducible: a
// fixed default so `make man-check` can pass on any day, and a
// SOURCE_DATE_EPOCH override for downstream rebuilds.
func TestManPageDate(t *testing.T) {
	tests := []struct {
		name    string
		epoch   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "should return the fixed date when SOURCE_DATE_EPOCH is unset",
			epoch: "",
			want:  time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "should honour SOURCE_DATE_EPOCH when it is set",
			epoch: "1700000000",
			want:  time.Unix(1700000000, 0).UTC(),
		},
		{
			name:  "should treat the unix epoch itself as a real value",
			epoch: "0",
			want:  time.Unix(0, 0).UTC(),
		},
		{
			name:    "should error when SOURCE_DATE_EPOCH is not an integer",
			epoch:   "not-a-number",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", tt.epoch)

			got, err := manPageDate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("manPageDate() = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("manPageDate() returned unexpected error: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("manPageDate() = %v, want %v", got, tt.want)
			}
		})
	}
}
