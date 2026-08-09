package api

// Test methodology: unit tests for readCertificateEvent's SSE parsing.
// Table-driven, tests run in parallel.

import (
	"strings"
	"testing"
)

func TestReadCertificateEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stream     string
		wantStatus string
		wantCert   string
		wantErr    bool
	}{
		{
			name:       "should decode an approved event with a certificate",
			stream:     "event:approved\ndata:{\"certificate\":\"ssh-ed25519-cert-v01@openssh.com AAAA...\"}\n\n",
			wantStatus: "approved",
			wantCert:   "ssh-ed25519-cert-v01@openssh.com AAAA...",
		},
		{
			name:       "should decode a denied event with no certificate",
			stream:     "event:denied\ndata:{}\n\n",
			wantStatus: "denied",
		},
		{
			name:    "should error on an empty stream",
			stream:  "",
			wantErr: true,
		},
		{
			name:    "should error on malformed JSON data",
			stream:  "event:approved\ndata:not-json\n\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readCertificateEvent(strings.NewReader(tt.stream))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("got status %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Certificate != tt.wantCert {
				t.Errorf("got certificate %q, want %q", got.Certificate, tt.wantCert)
			}
		})
	}
}
