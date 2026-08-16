//go:build exclude_frontend

package frontend

// Test methodology: the exclude_frontend build of this package has exactly
// one behavior worth pinning — RegisterFrontend refuses rather than serving
// anything — so this file is deliberately small. Its real job is to make the
// package testable under the tag at all: frontend_test.go covers the
// included build and cannot compile here, because the symbols it exercises
// only exist in frontend_included.go.

import (
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterFrontend_ShouldReturnNotIncludedWhenBuiltWithExcludeTag pins the
// contract the excluded build exists to provide: a caller gets a typed,
// recognizable refusal instead of a nil error and an unserved route.
func TestRegisterFrontend_ShouldReturnNotIncludedWhenBuiltWithExcludeTag(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	err := RegisterFrontend(gin.New())

	if !errors.Is(err, ErrFrontendNotIncluded) {
		t.Errorf("got error %v, want ErrFrontendNotIncluded", err)
	}
}
