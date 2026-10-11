package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// silentServer accepts every request and never answers until the test ends,
// the failure the request timeout exists for.
func silentServer(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(func() {
		close(done)
		ts.Close()
	})
	return ts
}

func TestDoJSON_ShouldReturnATransportErrorWhenTheServerNeverAnswers(t *testing.T) {
	t.Parallel()

	ts := silentServer(t)
	c, err := NewClient(Config{ServerURL: ts.URL, RequestTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected a *TransportError, got %v", err)
	}
}

func TestDoJSON_ShouldNameTheTimeoutWhenTheServerNeverAnswers(t *testing.T) {
	t.Parallel()

	ts := silentServer(t)
	c, err := NewClient(Config{ServerURL: ts.URL, RequestTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())

	if err == nil || !strings.Contains(err.Error(), "no response within 50ms") {
		t.Errorf("got %v, want it to name the 50ms timeout", err)
	}
}

func TestNewClient_ShouldDefaultTheRequestTimeout(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{ServerURL: "https://ssh.example.test"})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}
	if c.http.Timeout != DefaultRequestTimeout {
		t.Errorf("Timeout = %s, want %s", c.http.Timeout, DefaultRequestTimeout)
	}
}

func TestDescribeTransportError_ShouldPassThroughAnUnrecognisedError(t *testing.T) {
	t.Parallel()

	c := &HTTPClient{serverURL: "https://ssh.example.test"}
	original := errors.New("something else")

	if got := c.describeTransportError(original); got != original {
		t.Errorf("got %v, want the original error unchanged", got)
	}
}
