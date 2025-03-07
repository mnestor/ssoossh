// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mnestor/ssoossh/internal/api/types"
	cfg "github.com/mnestor/ssoossh/internal/config"
)

func TestApiGetCAs(t *testing.T) {
	gConfig = mockGetConfig

	req, err := http.NewRequest("GET", "/ca", nil)

	if err != nil {
		t.Errorf("Error creating a new request: %v", err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(apiGetCAs)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code. Expected: %d. Got: %d.", http.StatusOK, status)
	}

	var ca types.ResponseCAList

	if err := json.NewDecoder(rr.Body).Decode(&ca); err != nil {
		t.Errorf("Error decoding response body: %v", err)
	}

	if ca.CA == "" {
		t.Errorf("Expected: ssh publickey")
	}
}

func mockGetConfig() *cfg.Config {
	return &cfg.Config{
		SshKey: `-----BEGIN OPENSSH PRIVATE KEY-----
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
-----END OPENSSH PRIVATE KEY-----`,
	}
}
