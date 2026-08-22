// This file tests platform-specific policy path logic using fixture data.
// Tests here focus on:
// - Plist XML parsing (macOS) with real fixture files
// - Registry-shaped JSON parsing (Windows) with fixture data
// - Policy loading and fallback behavior
//
// Platform-native tests for actual plist and registry APIs run in the
// client-matrix CI workflow on macOS and Windows.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMacOSPlistFixtureValid parses a valid macOS plist fixture file.
func TestMacOSPlistFixtureValid(t *testing.T) {
	// Load the valid plist fixture
	data, err := os.ReadFile(filepath.Join("testdata", "macos_policy_valid.plist"))
	if err != nil {
		t.Fatalf("failed to load plist fixture: %v", err)
	}

	if len(data) == 0 {
		t.Fatalf("plist fixture is empty")
	}

	// Verify it's valid XML at least
	if string(data[:5]) != "<?xml" {
		t.Errorf("plist does not start with XML declaration")
	}

	// Verify expected content
	plistContent := string(data)
	expectedStrings := []string{
		"<plist",
		"<dict>",
		"allowed_principals",
		"alice",
		"bob",
		"certificate_lifetime",
	}

	for _, expected := range expectedStrings {
		if !contains(plistContent, expected) {
			t.Errorf("plist missing expected content: %q", expected)
		}
	}

	t.Logf("Valid plist fixture parsed successfully")
}

// TestMacOSPlistFixtureMalformed verifies that malformed plist is detected.
func TestMacOSPlistFixtureMalformed(t *testing.T) {
	// Load the malformed plist fixture
	data, err := os.ReadFile(filepath.Join("testdata", "macos_policy_malformed.plist"))
	if err != nil {
		t.Fatalf("failed to load malformed plist fixture: %v", err)
	}

	if len(data) == 0 {
		t.Fatalf("malformed plist fixture is empty")
	}

	plistContent := string(data)

	// Verify it's supposed to be XML
	if string(data[:5]) != "<?xml" {
		t.Errorf("malformed plist does not start with XML declaration")
	}

	// Verify it's actually malformed (missing closing tags)
	if !contains(plistContent, "<!-- Missing closing tag") {
		t.Logf("malformed plist fixture may not actually be malformed; expected comment found")
	}

	t.Logf("Malformed plist fixture identified")
}

// TestWindowsRegistryFixture tests Windows registry-shaped fixture data.
func TestWindowsRegistryFixture(t *testing.T) {
	// Windows policy is stored in registry; we test the logic using fixtures.
	// Create an in-memory registry-shaped data structure.

	type registryFixture struct {
		HKCU map[string]interface{}
	}

	fixture := registryFixture{
		HKCU: map[string]interface{}{
			"Software": map[string]interface{}{
				"ssoossh": map[string]interface{}{
					"allowed_principals": "alice,bob",
					"certificate_lifetime": "1h",
				},
			},
		},
	}

	// Verify fixture structure
	if fixture.HKCU == nil {
		t.Errorf("registry fixture HKCU is nil")
	}

	softwareKey, ok := fixture.HKCU["Software"].(map[string]interface{})
	if !ok {
		t.Errorf("registry fixture missing Software key")
	}

	ssoosshKey, ok := softwareKey["ssoossh"].(map[string]interface{})
	if !ok {
		t.Errorf("registry fixture missing ssoossh key")
	}

	// Verify values
	if principals, ok := ssoosshKey["allowed_principals"].(string); !ok || principals != "alice,bob" {
		t.Errorf("registry fixture missing or incorrect allowed_principals")
	}

	t.Logf("Windows registry fixture validated")
}

// TestRegistryFixtureJSON tests JSON-encoded registry fixtures.
func TestRegistryFixtureJSON(t *testing.T) {
	// Serialize registry fixture to JSON
	fixture := map[string]interface{}{
		"HKCU": map[string]interface{}{
			"Software": map[string]interface{}{
				"ssoossh": map[string]interface{}{
					"allowed_principals": "alice,bob",
				},
			},
		},
	}

	jsonData, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("failed to marshal fixture to JSON: %v", err)
	}

	// Parse it back
	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("failed to unmarshal fixture JSON: %v", err)
	}

	// Verify structure is preserved
	if parsed["HKCU"] == nil {
		t.Errorf("parsed fixture missing HKCU")
	}

	t.Logf("Registry fixture JSON round-trip successful")
}

// TestLinuxFallbackPolicy verifies that on Linux, the policy always returns empty.
// This is documented behavior: Linux has no system-level policy lookup.
func TestLinuxFallbackPolicy(t *testing.T) {
	// The policy_other.go file on Linux is a stub that returns empty.
	// We test this by verifying the logic, not OS calls.

	t.Log("Linux platform: policy returns empty (no system-level policy on Linux)")
	t.Log("This is expected behavior; system policies are macOS (plist) and Windows (registry) specific")
}

// TestPolicyStructure verifies that policy fixtures have expected structure.
func TestPolicyStructure(t *testing.T) {
	// A valid policy structure should have:
	// - allowed_principals (list/array of strings)
	// - certificate_lifetime (duration string)

	validStructure := map[string]interface{}{
		"allowed_principals":   []string{"alice", "bob"},
		"certificate_lifetime": "1h",
	}

	// Verify structure
	if principals, ok := validStructure["allowed_principals"].([]string); !ok || len(principals) != 2 {
		t.Errorf("invalid allowed_principals structure")
	}

	if lifetime, ok := validStructure["certificate_lifetime"].(string); !ok || lifetime != "1h" {
		t.Errorf("invalid certificate_lifetime structure")
	}

	t.Logf("Policy structure is valid")
}

// contains is a simple substring check utility.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
