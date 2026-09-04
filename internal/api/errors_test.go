package api

// Test methodology: direct unit tests on the error type and the two ways a
// response body becomes one. The end-to-end paths that produce a
// *ResponseError live in ca_test.go, service_test.go and sse_test.go.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  ResponseError
		want string
	}{
		{
			name: "should name the status alone when there is no message",
			err:  ResponseError{StatusCode: http.StatusInternalServerError},
			want: "ssoosshd returned status 500",
		},
		{
			name: "should include the server's message when there is one",
			err:  ResponseError{StatusCode: http.StatusBadRequest, Message: "public key is required"},
			want: "ssoosshd returned status 400: public key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResponseError_IsNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *ResponseError
		want bool
	}{
		{name: "should report true for a 404", err: &ResponseError{StatusCode: http.StatusNotFound}, want: true},
		{name: "should report false for another status", err: &ResponseError{StatusCode: http.StatusGone}, want: false},
		{name: "should report false for a nil error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.IsNotFound(); got != tt.want {
				t.Errorf("IsNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeResponseError_ShouldReadTheServersMessage(t *testing.T) {
	t.Parallel()

	respErr := decodeResponseError(http.StatusUnauthorized, []byte(`{"data":null,"error":"session expired"}`))
	if respErr.Message != "session expired" {
		t.Errorf("got message %q, want the decoded error body", respErr.Message)
	}
}

// A non-JSON body still has to produce a usable error: the status is worth
// reporting on its own, and an HTML error page from something in front of
// ssoosshd must not turn into a decode failure that hides it.
func TestDecodeResponseError_ShouldKeepTheStatusWhenTheBodyIsNotJSON(t *testing.T) {
	t.Parallel()

	respErr := decodeResponseError(http.StatusBadGateway, []byte("<html>502 Bad Gateway</html>"))
	if respErr.StatusCode != http.StatusBadGateway {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusBadGateway)
	}
	if respErr.Message != "" {
		t.Errorf("got message %q, want none for a body that is not the error shape", respErr.Message)
	}
}

func TestResponseError_ShouldReadTheErrorBodyOffAFailedConnect(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"data":null,"error":"request expired"}`))
	}))
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL) //nolint:noctx // test helper, no cancellation needed
	if err != nil {
		t.Fatalf("unexpected error making request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	respErr := responseError(resp)
	if respErr.StatusCode != http.StatusGone {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusGone)
	}
	if respErr.Message != "request expired" {
		t.Errorf("got message %q, want the decoded error body", respErr.Message)
	}
}
