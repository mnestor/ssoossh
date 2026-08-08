package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
)

// Test methodology: Tests verify OpenTelemetry initialization. Mutate
// process-global state (slog's default logger, OTel providers, OTEL_* env),
// so none run in parallel. Each test restores prior state via t.Cleanup.
// Uses helper function saveSlogDefault to prevent deadlock in slog's default
// handler (stdlib log -> slog re-entrance on non-reentrant mutex).

// saveSlogDefault snapshots slog's default logger, replaces it with a
// throwaway one writing to io.Discard, and restores the original when the
// test finishes. Installing the throwaway logger mirrors production (where
// logging.Setup installs a real handler before initObservability runs) and
// is load-bearing: initOtelLogging fans out to whatever the default handler
// is at call time, and capturing slog's built-in default handler there
// deadlocks the process — that handler writes through the stdlib log
// package, whose output is in turn bridged back into slog, re-entering the
// log package's non-reentrant mutex.
func saveSlogDefault(t *testing.T) {
	t.Helper()

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestDefaultResource_ShouldIncludeServiceNameAndVersion(t *testing.T) {
	t.Parallel()

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var gotName, gotVersion string
	for _, attr := range res.Attributes() {
		switch attr.Key {
		case semconv.ServiceNameKey:
			gotName = attr.Value.AsString()
		case semconv.ServiceVersionKey:
			gotVersion = attr.Value.AsString()
		}
	}
	if gotName != version.Name {
		t.Errorf("got service.name %q, want %q", gotName, version.Name)
	}
	if gotVersion != version.Version {
		t.Errorf("got service.version %q, want %q", gotVersion, version.Version)
	}
}

func TestInitOtelLogging_ShouldDefaultExporterEnvToNoneAndInstallLogger(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	prev := slog.Default()
	if err := initOtelLogging(context.Background(), res); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if slog.Default() == prev {
		t.Error("expected initOtelLogging to install a new default slog logger")
	}
}

func TestInitOtelTracing_ShouldReturnNilShutdownWhenDisabled(t *testing.T) {
	saveSlogDefault(t)

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	httpClient := &http.Client{Transport: http.DefaultTransport}
	prevTransport := httpClient.Transport

	shutdownFn, err := initOtelTracing(context.Background(), false, res, httpClient)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if shutdownFn != nil {
		t.Error("expected a nil shutdown function when tracing is disabled")
	}
	if httpClient.Transport != prevTransport {
		t.Error("expected the HTTP client transport to be left untouched when tracing is disabled")
	}
}

func TestInitOtelTracing_ShouldReturnShutdownFnAndWrapTransportWhenEnabled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_TRACES_EXPORTER", "console")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	httpClient := &http.Client{Transport: http.DefaultTransport}
	prevTransport := httpClient.Transport

	shutdownFn, err := initOtelTracing(context.Background(), true, res, httpClient)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if shutdownFn == nil {
		t.Fatal("expected a non-nil shutdown function when tracing is enabled")
	}
	if httpClient.Transport == prevTransport {
		t.Error("expected the HTTP client transport to be wrapped for tracing")
	}

	if err := shutdownFn(context.Background()); err != nil {
		t.Errorf("expected the shutdown function to succeed, got %v", err)
	}
}

func TestInitOtelTracing_ShouldErrorWhenExporterUnknown(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_TRACES_EXPORTER", "not-a-real-exporter")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	_, err = initOtelTracing(context.Background(), true, res, &http.Client{})
	if err == nil {
		t.Fatal("expected an error for an unknown span exporter, got nil")
	}
}

func TestInitOtelMetrics_ShouldReturnNilShutdownWhenDisabled(t *testing.T) {
	saveSlogDefault(t)

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	shutdownFn, err := initOtelMetrics(context.Background(), false, res)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if shutdownFn != nil {
		t.Error("expected a nil shutdown function when metrics are disabled")
	}
}

func TestInitOtelMetrics_ShouldReturnShutdownFnWhenEnabled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "console")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	shutdownFn, err := initOtelMetrics(context.Background(), true, res)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if shutdownFn == nil {
		t.Fatal("expected a non-nil shutdown function when metrics are enabled")
	}

	if err := shutdownFn(context.Background()); err != nil {
		t.Errorf("expected the shutdown function to succeed, got %v", err)
	}
}

func TestInitOtelMetrics_ShouldErrorWhenExporterUnknown(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "not-a-real-exporter")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	_, err = initOtelMetrics(context.Background(), true, res)
	if err == nil {
		t.Fatal("expected an error for an unknown metric reader, got nil")
	}
}

func TestInitObservability_ShouldSetHTTPClientAndNoShutdownFnsWhenAllDisabled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	a := &app{config: &config.Config{}} // Traces and Metrics default to false

	shutdownFns, err := a.initObservability(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(shutdownFns) != 0 {
		t.Errorf("expected no shutdown functions when traces and metrics are disabled, got %d", len(shutdownFns))
	}
	if a.httpClient == nil {
		t.Error("expected initObservability to set the app's HTTP client")
	}
}

func TestInitObservability_ShouldCollectShutdownFnsWhenTracesAndMetricsEnabled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	t.Setenv("OTEL_METRICS_EXPORTER", "console")

	a := &app{config: &config.Config{Traces: true, Metrics: true}}

	shutdownFns, err := a.initObservability(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(shutdownFns) != 2 {
		t.Fatalf("expected one shutdown function each for traces and metrics, got %d", len(shutdownFns))
	}
	for i, fn := range shutdownFns {
		if err := fn(context.Background()); err != nil {
			t.Errorf("shutdown function %d: expected no error, got %v", i, err)
		}
	}
}

func TestInitObservability_ShouldErrorWhenTracingExporterInvalid(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_TRACES_EXPORTER", "not-a-real-exporter")

	a := &app{config: &config.Config{Traces: true}}

	_, err := a.initObservability(context.Background())
	if err == nil {
		t.Fatal("expected an error when the span exporter cannot be created, got nil")
	}
}

func TestInitOtelLogging_ShouldErrorWhenExporterUnknown(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "not-a-real-exporter")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	if err := initOtelLogging(context.Background(), res); err == nil {
		t.Fatal("expected an error for an unknown log exporter, got nil")
	}
}

func TestInitObservability_ShouldErrorWhenMetricsExporterInvalid(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "not-a-real-exporter")

	a := &app{config: &config.Config{Metrics: true}}

	_, err := a.initObservability(context.Background())
	if err == nil {
		t.Fatal("expected an error when the metric reader cannot be created, got nil")
	}
}

func TestInitOtelTracing_ShouldReportShutdownErrorWhenContextCanceled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_TRACES_EXPORTER", "console")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	shutdownFn, err := initOtelTracing(context.Background(), true, res, &http.Client{Transport: http.DefaultTransport})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := shutdownFn(ctx); err == nil {
		t.Fatal("expected a shutdown error with a canceled context, got nil")
	}
}

func TestInitOtelMetrics_ShouldReportShutdownErrorWhenContextCanceled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "console")

	res, err := defaultResource()
	if err != nil {
		t.Fatalf("failed to build resource: %v", err)
	}

	shutdownFn, err := initOtelMetrics(context.Background(), true, res)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := shutdownFn(ctx); err == nil {
		t.Fatal("expected a shutdown error with a canceled context, got nil")
	}
}
