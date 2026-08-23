package config

// buildPolicyMap turns a flat map of canonical setting names (the same
// names as the YAML config keys, with the two sshkey fields addressed as
// "sshkey.type" and "sshkey.size") into the nested structure viper's
// MergeConfigMap expects — the same shape a YAML file with those settings
// would produce. Both the Windows and macOS policy readers build this flat
// form (each from their own source: registry values, plist keys) and
// convert through here, so the native-source-to-YAML-key mapping lives in
// each platform file, and the one nesting rule they share lives here once.
//
// Special case: forbidden_certificate_extensions (a list) is left as-is
// at the top level, not nested, because it's handled separately after
// unmarshaling (it's extracted and used to set ForbiddenCertificateExtensions
// directly on the Config struct, not merged through viper).
func buildPolicyMap(flat map[string]any) map[string]any {
	nested := make(map[string]any, len(flat))
	var sshkey map[string]any

	for key, value := range flat {
		switch key {
		case "sshkey.type", "sshkey.size":
			if sshkey == nil {
				sshkey = map[string]any{}
				nested["sshkey"] = sshkey
			}
			sshkey[key[len("sshkey."):]] = value
		default:
			nested[key] = value
		}
	}
	return nested
}
