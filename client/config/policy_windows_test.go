//go:build windows

package config

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// withTestPolicyKey creates a throwaway key under HKEY_CURRENT_USER (no
// administrator rights needed, unlike the real HKLM policy location) and
// returns its path for loadPolicyFrom to read.
func withTestPolicyKey(t *testing.T) string {
	t.Helper()
	path := `Software\ssoossh-policy-test\` + t.Name()

	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("create test key: %v", err)
	}
	t.Cleanup(func() {
		key.Close()
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
	})
	return path
}

func TestLoadPolicyFrom_ShouldReturnNilWhenTheKeyDoesNotExist(t *testing.T) {
	policy, err := loadPolicyFrom(registry.CURRENT_USER, `Software\ssoossh-policy-test\does-not-exist`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy != nil {
		t.Errorf("got %#v, want nil for a missing key", policy)
	}
}

func TestLoadPolicyFrom_ShouldReadStringAndDwordValues(t *testing.T) {
	path := withTestPolicyKey(t)
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("open test key: %v", err)
	}
	defer key.Close()

	if err := key.SetStringValue("Server", "https://ssh.example.com"); err != nil {
		t.Fatalf("set Server: %v", err)
	}
	if err := key.SetDWordValue("FIPS", 1); err != nil {
		t.Fatalf("set FIPS: %v", err)
	}
	if err := key.SetDWordValue("SSHKeySize", 384); err != nil {
		t.Fatalf("set SSHKeySize: %v", err)
	}

	policy, err := loadPolicyFrom(registry.CURRENT_USER, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy["server"] != "https://ssh.example.com" {
		t.Errorf("got server %#v, want the string value", policy["server"])
	}
	if policy["fips"] != true {
		t.Errorf("got fips %#v, want the DWORD 1 read as true", policy["fips"])
	}
	sshkey, _ := policy["sshkey"].(map[string]any)
	if sshkey["size"] != 384 {
		t.Errorf("got sshkey.size %#v, want 384", sshkey["size"])
	}
}
