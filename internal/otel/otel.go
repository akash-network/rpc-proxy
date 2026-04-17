package otel

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/akash-network/rpc-proxy/internal/config"
)

const tracerName = "akash-rpc-proxy"

// Init initializes the OpenTelemetry tracing pipeline. If cfg.Enabled is false,
// it returns a no-op shutdown function. If OTEL is enabled but no endpoint is
// configured, it returns an error since the caller likely misconfigured OTEL.
func Init(ctx context.Context, cfg config.OTELConfig, fallbackServiceName string) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("OTEL is enabled but no endpoint is configured")
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = fallbackServiceName
	}
	if serviceName == "" {
		serviceName = os.Getenv("HOSTNAME")
	}
	if serviceName == "" {
		serviceName = tracerName
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTEL resource: %w", err)
	}

	var exporter sdktrace.SpanExporter
	switch cfg.ExporterType {
	case "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exporter, err = otlptracehttp.New(ctx, opts...)
	case "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unknown OTEL exporter type: %q (expected \"http\" or \"grpc\")", cfg.ExporterType)
	}
	if err != nil {
		return nil, fmt.Errorf("creating OTEL exporter: %w", err)
	}

	sampleRate := cfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = 0
	}
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns a named tracer for creating spans.
// If no name is provided, it defaults to tracerName.
func Tracer(name ...string) trace.Tracer {
	n := tracerName
	if len(name) > 0 && name[0] != "" {
		n = name[0]
	}
	return otel.Tracer(n)
}
