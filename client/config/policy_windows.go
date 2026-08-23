//go:build windows

package config

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// policyRegistryPath is where an admin-managed value for ssoossh lives:
// under SOFTWARE\Policies, the conventional location for settings pushed by
// Group Policy (as opposed to a vendor's own non-policy HKLM key), and only
// writable by administrators/SYSTEM — the same trust boundary
// systemConfigDir relies on for /etc and %ProgramData%.
const policyRegistryPath = `SOFTWARE\Policies\com.github.mnestor\ssoossh`

// policyStringValues names the REG_SZ values under policyRegistryPath and
// the canonical setting name (matching the YAML config keys, with the two
// sshkey fields as "sshkey.type"/"sshkey.size") each maps to. See
// docs/client-settings-enforcement.md for the full mapping.
var policyStringValues = map[string]string{
	"Server":      "server",
	"CAPubkey":    "capubkey",
	"KeyFilename": "key_filename",
	"SSHKeyType":  "sshkey.type",
}

// policyMultiStringValues names the REG_MULTI_SZ values under
// policyRegistryPath. Each maps a registry value name to a canonical
// setting that takes a string slice.
var policyMultiStringValues = map[string]string{
	"ForbiddenCertificateExtensions": "forbidden_certificate_extensions",
}

// policyDwordValues names the REG_DWORD values under policyRegistryPath.
// dwordAsBool marks which of them are boolean settings (0/1) rather than
// plain integers.
var policyDwordValues = map[string]string{
	"SkipVerifySSL":     "insecure_skip_verify",
	"UseAgent":          "use_agent",
	"FallbackFileAgent": "fallback_file_agent",
	"TryOpenBrowser":    "try_open_browser",
	"FIPS":              "fips",
	"SSHKeySize":        "sshkey.size",
}

var dwordAsBool = map[string]bool{
	"insecure_skip_verify": true,
	"use_agent":            true,
	"fallback_file_agent":  true,
	"try_open_browser":     true,
	"fips":                 true,
}

// loadPlatformPolicy reads locked ssoossh settings from the registry key an
// administrator manages via Group Policy Preferences, Intune, or a login
// script.
func loadPlatformPolicy() (map[string]any, error) {
	return loadPolicyFrom(registry.LOCAL_MACHINE, policyRegistryPath)
}

// loadPolicyFrom is loadPlatformPolicy with the hive and path as
// parameters, so tests can point it at a throwaway HKEY_CURRENT_USER key
// instead of requiring administrator rights to write HKLM.
func loadPolicyFrom(root registry.Key, path string) (map[string]any, error) {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open registry key: %w", err)
	}
	defer key.Close()

	flat := map[string]any{}
	for name, canonical := range policyStringValues {
		if v, _, err := key.GetStringValue(name); err == nil {
			flat[canonical] = v
		}
	}
	for name, canonical := range policyDwordValues {
		v, _, err := key.GetIntegerValue(name)
		if err != nil {
			continue
		}
		if dwordAsBool[canonical] {
			flat[canonical] = v != 0
		} else {
			flat[canonical] = int(v)
		}
	}
	for name, canonical := range policyMultiStringValues {
		if v, _, err := key.GetStringsValue(name); err == nil {
			flat[canonical] = v
		}
	}

	if len(flat) == 0 {
		return nil, nil
	}
	return buildPolicyMap(flat), nil
}
