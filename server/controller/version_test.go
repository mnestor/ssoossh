package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/version"
)

func TestGetVersionHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errorHandlerMiddlewareForTest())

	apiGroup := router.Group("/api")
	NewVersionController(apiGroup)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var body struct {
		Data struct {
			Version    string `json:"version"`
			Commit     string `json:"commit"`
			GithubURL  string `json:"github_url"`
			ReleaseURL string `json:"release_url"`
		} `json:"data"`
		Error any `json:"error"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Compared against the build stamp rather than a literal: a test binary
	// is unstamped, so the values are the defaults from internal/version,
	// and hardcoding them here would fail on a stamped build.
	if body.Data.Version != version.Version {
		t.Errorf("expected version %q, got %q", version.Version, body.Data.Version)
	}
	if body.Data.Commit != version.Commit {
		t.Errorf("expected commit %q, got %q", version.Commit, body.Data.Commit)
	}
	if body.Data.GithubURL != version.Github {
		t.Errorf("expected github_url %q, got %q", version.Github, body.Data.GithubURL)
	}
	if want := releaseURL(version.Github, version.Version); body.Data.ReleaseURL != want {
		t.Errorf("expected release_url %q, got %q", want, body.Data.ReleaseURL)
	}
	if body.Error != nil {
		t.Errorf("expected error to be nil, got %v", body.Error)
	}
}

func TestReleaseURL(t *testing.T) {
	const github = "https://github.com/mnestor/ssoossh"

	tests := []struct {
		name     string
		github   string
		version  string
		expected string
	}{
		{
			name:     "should build a tag URL when the version is a bare semver",
			github:   github,
			version:  "0.1.0",
			expected: github + "/releases/tag/v0.1.0",
		},
		{
			name:     "should not double the v when the version already carries one",
			github:   github,
			version:  "v0.1.0",
			expected: github + "/releases/tag/v0.1.0",
		},
		{
			name:     "should keep a prerelease suffix",
			github:   github,
			version:  "1.2.0-rc.1",
			expected: github + "/releases/tag/v1.2.0-rc.1",
		},
		{
			name:     "should return empty for an unstamped development build",
			github:   github,
			version:  "development",
			expected: "",
		},
		{
			name:     "should return empty when the version is empty",
			github:   github,
			version:  "",
			expected: "",
		},
		{
			name:     "should return empty when the version is only a v",
			github:   github,
			version:  "v",
			expected: "",
		},
		{
			name:     "should return empty when no repository is configured",
			github:   "",
			version:  "0.1.0",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseURL(tt.github, tt.version); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
