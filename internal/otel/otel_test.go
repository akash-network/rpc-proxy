package otel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	proxyotel "github.com/akash-network/rpc-proxy/internal/otel"
)

func TestOtelHTTPHandlerExportsRootSpan(t *testing.T) {
	// Use an in-memory exporter to capture spans
	exporter := tracetest.NewInMemoryExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter), // synchronous so spans appear immediately
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer tp.Shutdown(context.Background())

	// Set globals exactly like Init() does
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Inner handler creates a child span, same as RPCProxy.ServeHTTP
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := proxyotel.Tracer().Start(r.Context(), "lb.select_backend",
			trace.WithAttributes(attribute.String("proxy.type", "rpc")))
		defer span.End()
		_ = ctx
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with otelhttp.NewHandler, same as cmd/main.go
	handler := otelhttp.NewHandler(inner, "akash-proxy")

	// Send a request (no Traceparent header, like `hey`)
	req := httptest.NewRequest("GET", "/rpc/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Inspect exported spans
	spans := exporter.GetSpans()
	t.Logf("exported %d spans:", len(spans))
	for i, s := range spans {
		t.Logf("  [%d] name=%q kind=%s traceID=%s spanID=%s parentSpanID=%s",
			i, s.Name, s.SpanKind, s.SpanContext.TraceID(), s.SpanContext.SpanID(), s.Parent.SpanID())
	}

	if len(spans) < 2 {
		t.Fatalf("expected at least 2 spans (root + child), got %d", len(spans))
	}

	// Find the root span (no parent)
	var rootFound bool
	for _, s := range spans {
		if !s.Parent.SpanID().IsValid() {
			rootFound = true
			t.Logf("root span: name=%q kind=%s", s.Name, s.SpanKind)
		}
	}
	if !rootFound {
		t.Error("no root span found — this reproduces the Tempo issue")
	}

	// Verify all spans share the same trace ID
	traceID := spans[0].SpanContext.TraceID()
	for _, s := range spans {
		if s.SpanContext.TraceID() != traceID {
			t.Errorf("span %q has different traceID: got %s, want %s", s.Name, s.SpanContext.TraceID(), traceID)
		}
	}
}
