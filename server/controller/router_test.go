package controller

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
)

// TestRouterConstruction_CertificatesAndCertRequestsDoNotCollide verifies
// that the new GET /api/certs/:id route for certificate details does not
// shadow the existing GET /api/certs/requests/:id routes for request details.
// Gin panics at startup on ambiguous wildcard routes, so this must pass
// every time the routes are registered.
func TestRouterConstruction_CertificatesAndCertRequestsDoNotCollide(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Admin: config.AdminConfig{
			AuditorGroup: "auditors",
		},
	}

	// Create a fake service
	fakeCertSvc := &fakeCertificateService{}

	// Create a router and register all certificate-related controllers
	// If routes collide or are ambiguous, Gin will panic during registration
	r := gin.New()
	apiGroup := r.Group("/api")

	// This should not panic despite having overlapping prefixes
	NewCertificateController(apiGroup, fakeCertSvc, func(c *gin.Context) { c.Next() }, cfg)

	// Verify the router was created successfully
	if r == nil {
		t.Fatal("router creation failed")
	}

	// Verify that routes exist by checking the route list
	// (Gin's panic happens during registration, so reaching here proves success)
	t.Log("✓ Router construction succeeded with non-colliding certificate routes")
}

// TestRouterConstruction_AdminRoutesRegister verifies that the admin controller
// can be registered without conflicts.
func TestRouterConstruction_AdminRoutesRegister(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Admin: config.AdminConfig{
			RequireGroup: "admins",
			AuditorGroup: "auditors",
		},
	}

	// Create a router
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())

	apiGroup := r.Group("/api")

	// Register admin controller
	// If routes collide, Gin will panic
	NewAdminController(
		apiGroup,
		cfg,
		mockDB(),
		func(c *gin.Context) { c.Next() }, // sessionAuth
		func(c *gin.Context) { c.Next() }, // adminAuth
		func(c *gin.Context) { c.Next() }, // auditorAuth
		func(c *gin.Context) { c.Next() }, // csrf
	)

	if r == nil {
		t.Fatal("router creation failed")
	}

	t.Log("✓ Router construction succeeded with admin routes")
}
