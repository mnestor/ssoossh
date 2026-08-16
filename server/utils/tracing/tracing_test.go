package tracing

// Test methodology: the package-level tracer is a proxy obtained once at
// package init (otel.Tracer(...)); the OTel SDK only ever repoints that
// proxy's delegate on the *first* call to otel.SetTracerProvider in a
// process (later calls just change what otel.GetTracerProvider returns,
// not where already-issued Tracers actually send spans — see
// go.opentelemetry.io/otel/internal/global's delegateTraceOnce). So tests
// install a single real SDK TracerProvider, backed by an in-memory
// exporter, exactly once for the whole test binary, and each test resets
// that shared exporter before acting rather than installing a fresh
// provider per test. Because the exporter is shared mutable state, tests
// that touch it do not run in parallel with each other (no t.Parallel());
// TestJobID_ShouldReturnAttributeWithGivenValue is the one exception, since
// it's a pure function test that never touches the tracer or exporter.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var (
	sharedExporter      = tracetest.NewInMemoryExporter()
	installProviderOnce sync.Once
)

// installTestTracerProvider installs a real SDK TracerProvider backed by
// sharedExporter as the global provider (once per process; see the
// package methodology comment above) and resets sharedExporter so this
// test only sees spans it generates itself.
func installTestTracerProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()

	installProviderOnce.Do(func() {
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(sharedExporter))
		otel.SetTracerProvider(tp)
	})
	sharedExporter.Reset()

	return sharedExporter
}

func TestStart_ShouldStartASpanRoutedToTheGlobalProvider(t *testing.T) {
	exporter := installTestTracerProvider(t)

	_, span := Start(context.Background(), "test-op")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d exported spans, want 1", len(spans))
	}
	if spans[0].Name != "test-op" {
		t.Errorf("got span name %q, want %q", spans[0].Name, "test-op")
	}
}

func TestEnd_ShouldNotMarkSpanFailedWhenErrNil(t *testing.T) {
	exporter := installTestTracerProvider(t)

	_, span := Start(context.Background(), "test-op")
	End(span, nil)

	spans := exporter.GetSpans()
	if got := spans[0].Status.Code; got != codes.Unset {
		t.Errorf("got status code %v, want %v", got, codes.Unset)
	}
}

func TestEnd_ShouldRecordErrorAndMarkSpanFailedWhenErrNonNil(t *testing.T) {
	exporter := installTestTracerProvider(t)

	_, span := Start(context.Background(), "test-op")
	End(span, errors.New("boom"))

	spans := exporter.GetSpans()
	if got := spans[0].Status.Code; got != codes.Error {
		t.Errorf("got status code %v, want %v", got, codes.Error)
	}
	if got := spans[0].Status.Description; got != "boom" {
		t.Errorf("got status description %q, want %q", got, "boom")
	}
	if len(spans[0].Events) != 1 {
		t.Errorf("got %d events, want 1 (the recorded error)", len(spans[0].Events))
	}
}

func TestEndExpected_ShouldTreatMatchingBenignErrorAsSuccess(t *testing.T) {
	exporter := installTestTracerProvider(t)
	errNotFound := errors.New("not found")

	_, span := Start(context.Background(), "test-op")
	EndExpected(span, errNotFound, errNotFound)

	spans := exporter.GetSpans()
	if got := spans[0].Status.Code; got != codes.Unset {
		t.Errorf("got status code %v, want %v (benign error should not fail the span)", got, codes.Unset)
	}
}

func TestEndExpected_ShouldFailSpanWhenErrDoesNotMatchAnyBenignError(t *testing.T) {
	exporter := installTestTracerProvider(t)

	_, span := Start(context.Background(), "test-op")
	EndExpected(span, errors.New("boom"), errors.New("not found"))

	spans := exporter.GetSpans()
	if got := spans[0].Status.Code; got != codes.Error {
		t.Errorf("got status code %v, want %v", got, codes.Error)
	}
}

func TestEndExpected_ShouldFailSpanWhenErrNonNilAndNoBenignErrorsGiven(t *testing.T) {
	exporter := installTestTracerProvider(t)

	_, span := Start(context.Background(), "test-op")
	EndExpected(span, errors.New("boom"))

	spans := exporter.GetSpans()
	if got := spans[0].Status.Code; got != codes.Error {
		t.Errorf("got status code %v, want %v", got, codes.Error)
	}
}

func TestFail_ShouldSetErrorStatusOnSpanFromContext(t *testing.T) {
	exporter := installTestTracerProvider(t)

	ctx, span := Start(context.Background(), "test-op")
	Fail(ctx, "structured protocol error")
	span.End()

	spans := exporter.GetSpans()
	if got := spans[0].Status.Code; got != codes.Error {
		t.Errorf("got status code %v, want %v", got, codes.Error)
	}
	if got := spans[0].Status.Description; got != "structured protocol error" {
		t.Errorf("got status description %q, want %q", got, "structured protocol error")
	}
}

func TestJobID_ShouldReturnAttributeWithGivenValue(t *testing.T) {
	t.Parallel() // pure function, doesn't touch the shared tracer provider

	attr := JobID("job-123")

	if got := string(attr.Key); got != "pocketid.job.id" {
		t.Errorf("got key %q, want %q", got, "pocketid.job.id")
	}
	if got := attr.Value.AsString(); got != "job-123" {
		t.Errorf("got value %q, want %q", got, "job-123")
	}
}
