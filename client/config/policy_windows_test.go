//go:build windows

package config

import (
	"math"
	"strings"
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

// GetIntegerValue reads REG_QWORD as well as REG_DWORD, so a policy value
// can carry more than the int a setting is held as. Converting it anyway
// would land a negative number in the config as a key size, which is worse
// than refusing: the point of a policy key is that what it says applies.
func TestLoadPolicyFrom_ShouldRefuseAnIntegerTooWideForTheSetting(t *testing.T) {
	path := withTestPolicyKey(t)
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("open test key: %v", err)
	}
	defer key.Close()

	if err := key.SetQWordValue("SSHKeySize", math.MaxInt32+1); err != nil {
		t.Fatalf("set SSHKeySize: %v", err)
	}

	policy, err := loadPolicyFrom(registry.CURRENT_USER, path)
	if err == nil {
		t.Fatalf("expected an out-of-range value to be an error, got policy %#v", policy)
	}
	if !strings.Contains(err.Error(), "SSHKeySize") {
		t.Errorf("got %q, want it to name the registry value", err.Error())
	}
}

// The guard must reject only what genuinely will not fit. A QWORD holding a
// real key size is a legitimate way for an administrator to write one.
func TestLoadPolicyFrom_ShouldReadAQwordThatFits(t *testing.T) {
	path := withTestPolicyKey(t)
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("open test key: %v", err)
	}
	defer key.Close()

	if err := key.SetQWordValue("SSHKeySize", 521); err != nil {
		t.Fatalf("set SSHKeySize: %v", err)
	}

	policy, err := loadPolicyFrom(registry.CURRENT_USER, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sshkey, _ := policy["sshkey"].(map[string]any)
	if sshkey["size"] != 521 {
		t.Errorf("got sshkey.size %#v, want 521", sshkey["size"])
	}
}
