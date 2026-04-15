package otel

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NewTracingTransport wraps the given http.RoundTripper with OpenTelemetry
// instrumentation. It creates child spans for outbound HTTP calls and injects
// Traceparent/Baggage headers into outgoing requests.
func NewTracingTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}
