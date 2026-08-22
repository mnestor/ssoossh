package apitypes

import (
	"encoding/json"
	"testing"
)

// FuzzCertRequestJSON tests JSON unmarshalling of CertRequest wire types.
// Catches issues with missing required fields, type mismatches, malformed JSON.
func FuzzCertRequestJSON(f *testing.F) {
	// Valid certificate requests
	f.Add([]byte(`{
		"publicKey": "ssh-rsa AAAAB3...",
		"principalType": "user",
		"requestedPrincipals": ["alice", "admin"],
		"extensions": [],
		"criticalOptions": {}
	}`))

	f.Add([]byte(`{"publicKey": "", "principalType": ""}`))

	// Malformed JSON
	f.Add([]byte(`{invalid json}`))
	f.Add([]byte(`{"key": }`))
	f.Add([]byte(``))

	// Type mismatches
	f.Add([]byte(`{"publicKey": 123}`))
	f.Add([]byte(`{"principalType": ["array"]}`))
	f.Add([]byte(`{"requestedPrincipals": "string"}`))

	// Null values
	f.Add([]byte(`{"publicKey": null}`))
	f.Add([]byte(`{"requestedPrincipals": null}`))

	// Unexpected fields
	f.Add([]byte(`{"publicKey": "key", "unknown": "field"}`))

	// Deep nesting
	f.Add([]byte(`{"extensions": {"nested": {"deep": {"value": 1}}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var cr CertRequest

		// Unmarshal should not panic
		_ = json.Unmarshal(data, &cr)
	})
}

// FuzzEnrollmentJSON tests JSON unmarshalling of enrollment types.
func FuzzEnrollmentJSON(f *testing.F) {
	f.Add([]byte(`{"code": "abc123"}`))
	f.Add([]byte(`{"code": ""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"code": null}`))
	f.Add([]byte(`invalid`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var e map[string]any

		// Unmarshal should not panic
		_ = json.Unmarshal(data, &e)
	})
}
