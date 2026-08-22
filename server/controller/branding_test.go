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
		expectedCode    int
		expectedOrgName string
		expectedLogoURL string
		expectedNotice  string
	}{
		{
			name:            "should return empty branding when config is empty",
			branding:        config.BrandingSettings{},
			expectedCode:    http.StatusOK,
			expectedOrgName: "",
			expectedLogoURL: "",
			expectedNotice:  "",
		},
		{
			name: "should return all fields when configured",
			branding: config.BrandingSettings{
				OrgName:     "Acme Corp",
				LogoURL:     "https://example.com/logo.png",
				LoginNotice: "Please review our policies",
			},
			expectedCode:    http.StatusOK,
			expectedOrgName: "Acme Corp",
			expectedLogoURL: "https://example.com/logo.png",
			expectedNotice:  "Please review our policies",
		},
		{
			name: "should return partial branding when only some fields set",
			branding: config.BrandingSettings{
				OrgName: "Example Inc",
				LogoURL: "",
			},
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
			NewBrandingController(apiGroup, cfg)

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
				Error interface{} `json:"error"`
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
