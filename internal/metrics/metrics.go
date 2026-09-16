package metrics

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

var (
	serviceName  string
	serviceMutex sync.RWMutex

	// customRegistry is the registry that will hold all metrics.
	customRegistry *prometheus.Registry
)

// labeledGatherer wraps a prometheus.Gatherer to add service labels to all metrics
type labeledGatherer struct {
	gatherer prometheus.Gatherer
}

func (lg *labeledGatherer) Gather() ([]*dto.MetricFamily, error) {
	metrics, err := lg.gatherer.Gather()
	if err != nil {
		return nil, err
	}

	serviceMutex.RLock()
	currentServiceName := serviceName
	serviceMutex.RUnlock()

	if currentServiceName != "" {
		for _, mf := range metrics {
			for _, metric := range mf.Metric {
				metric.Label = append(metric.Label, &dto.LabelPair{
					Name:  stringPtr("service"),
					Value: stringPtr(currentServiceName),
				})
			}
		}
	}

	return metrics, nil
}

func stringPtr(s string) *string {
	return &s
}

// NodeCounts is a gauge that tracks the number of available nodes for each type
var NodeCounts *prometheus.GaugeVec

// RequestCount is a counter that tracks the number of requests per node for each type.
// It has the following labels:
//   - type: The type of request (rest/rpc/grpc)
//   - node: The node handling the request
var RequestCount *prometheus.CounterVec

// RequestStatusCount is a counter that tracks the number of requests by status code.
// It has the following labels:
//   - type: The type of request (rest/rpc/grpc)
//   - node: The node handling the request
//   - status_code: The HTTP status code of the response
var RequestStatusCount *prometheus.CounterVec

// NodeHealth is a gauge that tracks the health status of each node
// It has the following labels:
//   - type: The type of node (rest/rpc/grpc)
//   - node: The node identifier (usually the host)
//
// Value is 1 if healthy, 0 if unhealthy
var NodeHealth *prometheus.GaugeVec

// UpstreamErrors is a counter that tracks upstream transport failures per node.
// These are requests that never received a response from the peer (reset streams,
// dial timeouts, exceeded proxy-request-timeout), as opposed to responses with an
// error status. It has the following labels:
//   - type: The type of request (rest/rpc/grpc)
//   - node: The node that failed
var UpstreamErrors *prometheus.CounterVec

func init() {
	customRegistry = prometheus.NewRegistry()

	NodeCounts = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_node_count",
			Help: "Number of healthy nodes for each type",
		},
		[]string{"type"},
	)

	RequestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_request_count",
			Help: "Number of requests per node for each type",
		},
		[]string{"type", "node"},
	)

	RequestStatusCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_request_status_count",
			Help: "Number of requests by status code",
		},
		[]string{"type", "node", "status_code"},
	)

	NodeHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_node_health",
			Help: "Health status of each node (1 = healthy, 0 = unhealthy)",
		},
		[]string{"type", "node"},
	)

	UpstreamErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_upstream_error_count",
			Help: "Number of upstream transport failures per node",
		},
		[]string{"type", "node"},
	)

	customRegistry.MustRegister(NodeCounts)
	customRegistry.MustRegister(RequestCount)
	customRegistry.MustRegister(RequestStatusCount)
	customRegistry.MustRegister(NodeHealth)
	customRegistry.MustRegister(UpstreamErrors)
}

// SetServiceName sets the global service name for all metrics.
func SetServiceName(name string) {
	serviceMutex.Lock()
	defer serviceMutex.Unlock()
	serviceName = name
}

// UpdateNodeCount updates the node count for a specific type.
func UpdateNodeCount(nodeType string, count float64) {
	NodeCounts.WithLabelValues(nodeType).Set(count)
}

// IncrementRequestCount increments the request count for a specific type and node.
func IncrementRequestCount(requestType, node string) {
	RequestCount.WithLabelValues(requestType, node).Inc()
}

// IncrementRequestStatusCount increments the request count for a specific type, node, and status code.
func IncrementRequestStatusCount(requestType, node string, statusCode int) {
	RequestStatusCount.WithLabelValues(requestType, node, strconv.Itoa(statusCode)).Inc()
}

// IncrementUpstreamError increments the upstream transport failure count for a specific type and node.
func IncrementUpstreamError(requestType, node string) {
	UpstreamErrors.WithLabelValues(requestType, node).Inc()
}

// UpdateNodeHealth updates the health status for a specific node.
func UpdateNodeHealth(nodeType, node string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	NodeHealth.WithLabelValues(nodeType, node).Set(value)
}

// PrepareMetricsServer prepares a new HTTP server for Prometheus metrics.
func PrepareMetricsServer(addr string, path string) *http.Server {
	mux := http.NewServeMux()

	labeledGatherer := &labeledGatherer{gatherer: customRegistry}
	handler := promhttp.HandlerFor(labeledGatherer, promhttp.HandlerOpts{})

	mux.Handle(path, handler)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return srv
}

// RegisterMetric registers a new metric with the custom registry.
// This ensures all future metrics automatically get service labels.
func RegisterMetric(metric prometheus.Collector) error {
	return customRegistry.Register(metric)
}
