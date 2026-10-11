package api

// Test methodology: unit tests for NewClient and its TLS verification,
// using httptest.NewTLSServer for a real (self-signed) TLS endpoint to
// connect to. Tests run in parallel where they don't share server state.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient_ShouldErrorWhenServerURLEmpty(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("expected an error for an empty ServerURL, got nil")
	}
}

func TestNewClient_TLSVerification(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"ca":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIP/7mq9cQq4DRbk0doDBs9CsbCTEmTI1bSk1z8s1AdWc test-ca"},"error":null}`))
	}))
	// t.Cleanup, not defer: the subtests below call t.Parallel(), which
	// pauses them and returns control to this function immediately — a
	// deferred Close() would run right then, before the subtests actually
	// execute. t.Cleanup runs after every subtest (parallel or not)
	// finishes instead.
	t.Cleanup(ts.Close)

	t.Run("should fail without SkipVerifySSL against a self-signed cert", func(t *testing.T) {
		t.Parallel()

		c, err := NewClient(Config{ServerURL: ts.URL})
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		if _, err := c.GetCA(context.Background()); err == nil {
			t.Error("expected the request to fail standard TLS verification against a self-signed cert, got nil")
		}
	})

	t.Run("should succeed with SkipVerifySSL against a self-signed cert", func(t *testing.T) {
		t.Parallel()

		c, err := NewClient(Config{ServerURL: ts.URL, SkipVerifySSL: true})
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		if _, err := c.GetCA(context.Background()); err != nil {
			t.Errorf("unexpected error with SkipVerifySSL set: %v", err)
		}
	})
}

// The transport-level failures below are the ones an operator actually
// hits — a server that is down, behind a proxy that mangles the response,
// or misconfigured — so each has to come back as an error the caller can
// report rather than a zero value that looks like success.

func TestDoJSON_ShouldReturnTheTransportErrorWhenNothingIsListening(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close()

	c, err := NewClient(Config{ServerURL: url})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	if _, err := c.GetCA(context.Background()); err == nil {
		t.Error("expected an error connecting to a closed port, got nil")
	}
}

func TestDoJSON_ShouldErrorWhenTheResponseIsNotJSON(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>hello from a captive portal</html>"))
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())
	if err == nil {
		t.Fatal("expected an error decoding a non-JSON 200, got nil")
	}
	if !strings.Contains(err.Error(), "decode response body") {
		t.Errorf("got %q, want an error naming the failed decode", err)
	}
}

// A response that promises more body than it delivers fails while being
// read, after the status line has already arrived.
func TestDoJSON_ShouldErrorWhenTheResponseBodyIsTruncated(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "512")
		_, _ = w.Write([]byte(`{"data":{"ca":"ssh-ed`))
		w.(http.Flusher).Flush()    // headers and the partial body reach the client
		panic(http.ErrAbortHandler) // then drop the connection mid-body
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())
	if err == nil {
		t.Fatal("expected an error reading a truncated body, got nil")
	}
	if !strings.Contains(err.Error(), "read response body") {
		t.Errorf("got %q, want an error naming the failed read", err)
	}
}

// TestNewClient_ShouldRejectAnUnusableServerURL covers the addresses that
// used to be normalised into a request to somewhere else, or into one no
// URL parser accepts.
func TestNewClient_ShouldRejectAnUnusableServerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "should reject a scheme other than http or https", in: "ftp://x", want: `invalid server "ftp://x": the scheme must be http or https`},
		{name: "should reject a scheme with no host", in: "https://", want: `invalid server "https://": it names no host`},
		{name: "should reject an address with a space in the host", in: "not a url", want: `invalid server "not a url": not a valid URL`},
		{name: "should reject a control character in the host", in: "http://exa\x7fmple.com", want: "invalid server \"http://exa\\x7fmple.com\": not a valid URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewClient(Config{ServerURL: tt.in})
			if err == nil || err.Error() != tt.want {
				t.Errorf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNewClient_ShouldTreatAWhitespaceServerURLAsMissing(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(Config{ServerURL: "   "}); !errors.Is(err, ErrNoServerURL) {
		t.Errorf("got %v, want ErrNoServerURL", err)
	}
}
