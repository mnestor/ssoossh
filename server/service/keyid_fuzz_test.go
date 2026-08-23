package service

import (
	"testing"
)

// FuzzParseKeyIDTemplate tests the key ID template parser for robustness against
// malformed, oversized, and malicious template strings. This catches template
// injection vulnerabilities, denial-of-service conditions (unbounded loops/expansion),
// and panics on edge cases.
func FuzzParseKeyIDTemplate(f *testing.F) {
	// Seed corpus with valid templates
	f.Add("{{.Username}}", "user")
	f.Add("{{.Email}}", "email")
	f.Add("{{.ClientIP}}", "clientip")
	f.Add("{{.Subject}}", "subject")
	f.Add("{{.UniqueID}}", "uniqueid")
	f.Add("pam:{{.Username}}", "pam")
	f.Add("{{.Extra.dept}}", "extra")
	f.Add(`{{join .Extra.accounts ";"}}`, "extra_join")
	f.Add(`{{index .Extra "odd-name"}}`, "extra_index")

	// Seed with common malformed cases
	f.Add("{{.Username", "unclosed")
	f.Add(".Username}}", "mismatched")
	f.Add("{{.NonexistentField}}", "nonexistent")
	f.Add("{{range .}}", "range")
	f.Add("{{if true}}{{end}}", "conditional")
	f.Add("{{.}}", "dot")
	f.Add("", "empty")

	// Seed with attempted injection / complexity
	f.Add("{{.Username | html}}", "pipeline")
	f.Add("{{ .Username | printf }}", "spaces")
	f.Add("{{printf \"%s\" .Username}}", "function_call")

	// Seed with very long strings to catch DOS
	f.Add(repeatString("{{.Username}}", 100), "repeated")
	f.Add(repeatString("x", 10000), "large_literal")

	f.Fuzz(func(t *testing.T, tmplSrc string, typeName string) {
		// Should not panic, should produce a valid template or an error message
		tmpl, err := parseKeyIDTemplate(typeName, tmplSrc)

		// Regardless of success or failure, the function should not panic
		// and should complete in reasonable time.

		if err != nil {
			// Error is acceptable and expected for malformed input
			return
		}

		// If we got a template, it should be usable
		if tmpl != nil {
			data := keyIDTemplateData{
				Username: "alice",
				Subject:  "alice@example.com",
				Email:    "alice@example.com",
				ClientIP: "192.0.2.1",
				UniqueID: "uuid-12345",
				Extra: map[string]extraValue{
					"dept":     scalarExtra("eng"),
					"accounts": listExtra([]string{"a", "b"}),
				},
			}

			result, execErr := executeKeyIDTemplate(tmpl, data)

			// Execution should not panic
			if execErr != nil {
				// Error is acceptable, but shouldn't be a panic
				return
			}

			// Result should be a reasonable string (catch unbounded expansion)
			if len(result) > 100000 {
				t.Fatalf("template execution produced unreasonably large output: %d bytes", len(result))
			}
		}
	})
}

// Helper to create repeated strings for DOS testing.
func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
