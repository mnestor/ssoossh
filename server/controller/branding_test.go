package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

func TestGetBrandingHandler(t *testing.T) {
	tests := []struct {
		name            string
		branding        config.BrandingSettings
		logoImg         *logoImage
		expectedCode    int
		expectedOrgName string
		expectedLogoURL string
		expectedNotice  string
	}{
		{
			name:            "should return empty branding when config is empty and no logo",
			branding:        config.BrandingSettings{},
			logoImg:         nil,
			expectedCode:    http.StatusOK,
			expectedOrgName: "",
			expectedLogoURL: "",
			expectedNotice:  "",
		},
		{
			name: "should return all fields when configured with logo",
			branding: config.BrandingSettings{
				OrgName:     "Acme Corp",
				LoginNotice: "Please review our policies",
			},
			logoImg: &logoImage{
				bytes:       []byte{0x89, 0x50, 0x4E, 0x47},
				contentType: "image/png",
				etag:        `W/"test"`,
			},
			expectedCode:    http.StatusOK,
			expectedOrgName: "Acme Corp",
			expectedLogoURL: "/api/branding/logo",
			expectedNotice:  "Please review our policies",
		},
		{
			name: "should omit logo_url when no logo is configured",
			branding: config.BrandingSettings{
				OrgName: "Example Inc",
			},
			logoImg:         nil,
			expectedCode:    http.StatusOK,
			expectedOrgName: "Example Inc",
			expectedLogoURL: "",
			expectedNotice:  "",
		},
		{
			name: "should handle multiline login notice",
			branding: config.BrandingSettings{
				LoginNotice: "Line 1\nLine 2\nLine 3",
			},
			logoImg:         nil,
			expectedCode:    http.StatusOK,
			expectedOrgName: "",
			expectedLogoURL: "",
			expectedNotice:  "Line 1\nLine 2\nLine 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(errorHandlerMiddlewareForTest())

			cfg := &config.Config{
				Branding: tt.branding,
			}

			apiGroup := router.Group("/api")
			NewBrandingController(apiGroup, cfg, tt.logoImg)

			req := httptest.NewRequest(http.MethodGet, "/api/branding", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("expected status %d, got %d", tt.expectedCode, w.Code)
			}

			var body struct {
				Data struct {
					OrgName     string `json:"org_name"`
					LogoURL     string `json:"logo_url"`
					LoginNotice string `json:"login_notice"`
				} `json:"data"`
				Error any `json:"error"`
			}

			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}

			if body.Data.OrgName != tt.expectedOrgName {
				t.Errorf("expected org_name %q, got %q", tt.expectedOrgName, body.Data.OrgName)
			}
			if body.Data.LogoURL != tt.expectedLogoURL {
				t.Errorf("expected logo_url %q, got %q", tt.expectedLogoURL, body.Data.LogoURL)
			}
			if body.Data.LoginNotice != tt.expectedNotice {
				t.Errorf("expected login_notice %q, got %q", tt.expectedNotice, body.Data.LoginNotice)
			}
			if body.Error != nil {
				t.Errorf("expected error to be nil, got %v", body.Error)
			}
		})
	}
}

func TestGetLogoHandler(t *testing.T) {
	tests := []struct {
		name                string
		logoImg             *logoImage
		expectedCode        int
		expectedContentType string
	}{
		{
			name: "should serve logo with PNG content type",
			logoImg: &logoImage{
				bytes:       []byte{0x89, 0x50, 0x4E, 0x47},
				contentType: "image/png",
				etag:        `W/"test-png"`,
			},
			expectedCode:        http.StatusOK,
			expectedContentType: "image/png",
		},
		{
			name: "should serve SVG with CSP header",
			logoImg: &logoImage{
				bytes:       []byte("<svg></svg>"),
				contentType: "image/svg+xml",
				etag:        `W/"test-svg"`,
			},
			expectedCode:        http.StatusOK,
			expectedContentType: "image/svg+xml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(errorHandlerMiddlewareForTest())

			cfg := &config.Config{}
			apiGroup := router.Group("/api")
			NewBrandingController(apiGroup, cfg, tt.logoImg)

			req := httptest.NewRequest(http.MethodGet, "/api/branding/logo", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("expected status %d, got %d", tt.expectedCode, w.Code)
			}

			if ct := w.Header().Get("Content-Type"); ct != tt.expectedContentType {
				t.Errorf("expected content type %q, got %q", tt.expectedContentType, ct)
			}

			if et := w.Header().Get("ETag"); et == "" {
				t.Errorf("expected ETag header to be set")
			}

			if cc := w.Header().Get("Cache-Control"); cc == "" {
				t.Errorf("expected Cache-Control header to be set")
			}

			// SVG should have CSP header
			if tt.logoImg.contentType == "image/svg+xml" {
				if csp := w.Header().Get("Content-Security-Policy"); csp == "" {
					t.Errorf("expected Content-Security-Policy header for SVG")
				}
			}
		})
	}
}

func TestGetLogoHandler_NotFound(t *testing.T) {
	// Test that /api/branding/logo returns 404 when no logo is configured
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errorHandlerMiddlewareForTest())

	cfg := &config.Config{}
	apiGroup := router.Group("/api")
	NewBrandingController(apiGroup, cfg, nil) // No logo

	req := httptest.NewRequest(http.MethodGet, "/api/branding/logo", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d when no logo configured, got %d", http.StatusNotFound, w.Code)
	}
}

func TestGetLogoHandler_ETag(t *testing.T) {
	// Test that ETag-based caching works
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(errorHandlerMiddlewareForTest())

	logoImg := &logoImage{
		bytes:       []byte{0x89, 0x50, 0x4E, 0x47},
		contentType: "image/png",
		etag:        `W/"abc123"`,
	}

	cfg := &config.Config{}
	apiGroup := router.Group("/api")
	NewBrandingController(apiGroup, cfg, logoImg)

	req := httptest.NewRequest(http.MethodGet, "/api/branding/logo", nil)
	req.Header.Set("If-None-Match", `W/"abc123"`)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Errorf("expected status %d for matching ETag, got %d", http.StatusNotModified, w.Code)
	}
}

// errorHandlerMiddlewareForTest provides basic error handling for tests.
func errorHandlerMiddlewareForTest() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": c.Errors[0].Error(),
				"data":  nil,
			})
		}
	}
}
