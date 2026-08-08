package bootstrap

// taken from https://github.com/pocket-id/pocket-id/blob/main/backend/internal/bootstrap/observability_boostrap.go

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/italypaleale/go-kit/servicerunner"
	slogmulti "github.com/samber/slog-multi"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	globallog "go.opentelemetry.io/otel/log/global"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/mnestor/ssoossh/internal/version"
)

// defaultResource builds the OpenTelemetry resource identifying this
// service (name and version) to exporters.
func defaultResource() (*resource.Resource, error) {
	return resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(version.Name),
			semconv.ServiceVersion(version.Version),
		),
	)
}

// initObservability sets up OpenTelemetry-backed logging, tracing, and
// metrics based on a.config, and sets a.httpClient to an HTTP client
// instrumented for tracing. It returns the shutdown functions for any
// exporters that were started, which the caller must run on exit.
func (a *app) initObservability(ctx context.Context) (shutdownFns []servicerunner.Service, err error) {
	resource, err := defaultResource()
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenTelemetry resource: %w", err)
	}

	shutdownFns = make([]servicerunner.Service, 0, 2)

	httpClient := &http.Client{}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// Indicates a development-time error
		panic("Default transport is not of type *http.Transport")
	}
	httpClient.Transport = defaultTransport.Clone()

	// Logging
	err = initOtelLogging(ctx, resource)
	if err != nil {
		return nil, err
	}

	// Tracing
	tracingShutdownFn, err := initOtelTracing(ctx, a.config.Traces, resource, httpClient)
	if err != nil {
		return nil, err
	} else if tracingShutdownFn != nil {
		shutdownFns = append(shutdownFns, tracingShutdownFn)
	}

	// Metrics
	metricsShutdownFn, err := initOtelMetrics(ctx, a.config.Metrics, resource)
	if err != nil {
		return nil, err
	} else if metricsShutdownFn != nil {
		shutdownFns = append(shutdownFns, metricsShutdownFn)
	}

	a.httpClient = httpClient

	return shutdownFns, nil
}

// initOtelLogging fans slog output out to both the process's existing
// handler and an OpenTelemetry log exporter, then installs it as the
// default slog logger.
func initOtelLogging(ctx context.Context, resource *resource.Resource) error {
	// autoexport wants this set in ENV
	if os.Getenv("OTEL_LOGS_EXPORTER") == "" {
		os.Setenv("OTEL_LOGS_EXPORTER", "none")
	}
	exp, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize OpenTelemetry log exporter: %w", err)
	}

	// Create the logger provider
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(
			sdklog.NewBatchProcessor(exp),
		),
		sdklog.WithResource(resource),
	)

	// Set the logger provider globally
	globallog.SetLoggerProvider(provider)

	handler := slogmulti.Fanout(
		slog.With().Handler(),
		otelslog.NewHandler(version.Name, otelslog.WithLoggerProvider(provider)),
	)

	// Set the default slog to send logs to OTel and add the app name
	slog.SetDefault(slog.New(handler))

	return nil
}

// initOtelTracing installs an OpenTelemetry tracer provider and wraps
// httpClient's transport for trace propagation when traces is true;
// otherwise it installs a no-op tracer provider and returns a nil
// shutdown function.
func initOtelTracing(ctx context.Context, traces bool, resource *resource.Resource, httpClient *http.Client) (shutdownFn servicerunner.Service, err error) {
	if !traces {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		return nil, nil
	}

	tr, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize OpenTelemetry span exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource),
		sdktrace.WithBatcher(tr),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	shutdownFn = func(shutdownCtx context.Context) error { //nolint:contextcheck
		tpCtx, tpCancel := context.WithTimeout(shutdownCtx, 10*time.Second)
		defer tpCancel()
		shutdownErr := tp.Shutdown(tpCtx)
		if shutdownErr != nil {
			return fmt.Errorf("failed to gracefully shut down traces exporter: %w", shutdownErr)
		}
		return nil
	}

	// Add tracing to the HTTP client
	httpClient.Transport = otelhttp.NewTransport(httpClient.Transport)

	return shutdownFn, nil
}

// initOtelMetrics installs an OpenTelemetry meter provider when metrics is
// true; otherwise it installs a no-op meter provider and returns a nil
// shutdown function.
func initOtelMetrics(ctx context.Context, metrics bool, resource *resource.Resource) (shutdownFn servicerunner.Service, err error) {
	if !metrics {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		return nil, nil
	}

	mr, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize OpenTelemetry metric reader: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithResource(resource),
		metric.WithReader(mr),
	)
	otel.SetMeterProvider(mp)

	shutdownFn = func(shutdownCtx context.Context) error { //nolint:contextcheck
		mpCtx, mpCancel := context.WithTimeout(shutdownCtx, 10*time.Second)
		defer mpCancel()
		shutdownErr := mp.Shutdown(mpCtx)
		if shutdownErr != nil {
			return fmt.Errorf("failed to gracefully shut down metrics exporter: %w", shutdownErr)
		}
		return nil
	}

	return shutdownFn, nil
}
