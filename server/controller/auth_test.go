package controller

// Test methodology: table-driven unit test for the open-redirect guard.

import "testing"

func TestIsSafeReturnURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "should accept a bare path", url: "/dashboard", want: true},
		{name: "should accept a path with a query string", url: "/certs?type=user", want: true},
		{name: "should reject an empty string", url: "", want: false},
		{name: "should reject a path not starting with a slash", url: "dashboard", want: false},
		{name: "should reject an absolute http URL", url: "http://evil.example.com/", want: false},
		{name: "should reject an absolute https URL", url: "https://evil.example.com/", want: false},
		{name: "should reject a protocol-relative URL", url: "//evil.example.com/", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isSafeReturnURL(tt.url); got != tt.want {
				t.Errorf("isSafeReturnURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
