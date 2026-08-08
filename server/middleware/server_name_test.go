package middleware

// Test methodology: Unit tests for ServerName validation middleware. Tests
// run in parallel (t.Parallel()). Verifies Host/SNI validation against
// configured server name and 421 Misdirected Request response. Uses helper
// function to build middleware chain with error handler for realistic
// response rendering.

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newServerNameTestRouter builds an engine with the error handler and
// server-name middleware plus one GET route, mirroring the production chain
// so rejections render as real 421 responses.
func newServerNameTestRouter(serverName string) *gin.Engine {
	r := gin.New()
	r.Use(NewErrorHandlerMiddleware().Add())
	r.Use(NewServerNameMiddleware().Add(serverName))
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

// doServerNameRequest performs a GET /x with the given Host header and
// optional TLS connection state.
func doServerNameRequest(r *gin.Engine, host string, tlsState *tls.ConnectionState) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = host
	req.TLS = tlsState
	r.ServeHTTP(w, req)
	return w
}

func TestServerNameMiddleware_ShouldPassThroughWhenServerNameEmpty(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("")

	w := doServerNameRequest(r, "anything.example.com", nil)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNameMiddleware_ShouldAllowWhenHostMatches(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	w := doServerNameRequest(r, "nlapd-api.gsfc.nasa.gov", nil)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNameMiddleware_ShouldAllowWhenHostMatchesCaseInsensitively(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	w := doServerNameRequest(r, "NLAPD-API.GSFC.NASA.GOV", nil)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNameMiddleware_ShouldAllowWhenHostCarriesPort(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	w := doServerNameRequest(r, "nlapd-api.gsfc.nasa.gov:8443", nil)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNameMiddleware_ShouldAllowWhenHostIsBracketedIPv6Literal(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("::1")

	w := doServerNameRequest(r, "[::1]:8443", nil)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNameMiddleware_ShouldRejectWhenHostDiffers(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	w := doServerNameRequest(r, "other.example.com", nil)
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusMisdirectedRequest)
	}
}

func TestServerNameMiddleware_ShouldRejectWhenSNIDiffers(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	// Host matches but the TLS handshake carried a different SNI.
	w := doServerNameRequest(r, "nlapd-api.gsfc.nasa.gov", &tls.ConnectionState{ServerName: "other.example.com"})
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusMisdirectedRequest)
	}
}

func TestServerNameMiddleware_ShouldAllowWhenSNIMatches(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	w := doServerNameRequest(r, "nlapd-api.gsfc.nasa.gov", &tls.ConnectionState{ServerName: "nlapd-api.gsfc.nasa.gov"})
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerNameMiddleware_ShouldAllowWhenSNIAbsent(t *testing.T) {
	t.Parallel()

	r := newServerNameTestRouter("nlapd-api.gsfc.nasa.gov")

	// Clients connecting by IP send no SNI; the Host check still applies,
	// but an absent SNI alone must not reject.
	w := doServerNameRequest(r, "nlapd-api.gsfc.nasa.gov", &tls.ConnectionState{})
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}
