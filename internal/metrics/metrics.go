package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// NodeCounts is a gauge that tracks the number of available nodes for each type
	NodeCounts = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_node_count",
			Help: "Number of healthy nodes for each type",
		},
		[]string{"type"},
	)

	// RequestCount is a counter that tracks the number of requests per node for each type.
	// It has the following labels:
	//   - type: The type of request (rest/rpc/grpc)
	//   - node: The node handling the request
	RequestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_request_count",
			Help: "Number of requests per node for each type",
		},
		[]string{"type", "node"},
	)

	// RequestStatusCount is a counter that tracks the number of requests by status code.
	// It has the following labels:
	//   - type: The type of request (rest/rpc/grpc)
	//   - node: The node handling the request
	//   - status_code: The HTTP status code of the response
	RequestStatusCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_request_status_count",
			Help: "Number of requests by status code",
		},
		[]string{"type", "node", "status_code"},
	)

	// NodeHealth is a gauge that tracks the health status of each node
	// It has the following labels:
	//   - type: The type of node (rest/rpc/grpc)
	//   - node: The node identifier (usually the host)
	// Value is 1 if healthy, 0 if unhealthy
	NodeHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_node_health",
			Help: "Health status of each node (1 = healthy, 0 = unhealthy)",
		},
		[]string{"type", "node"},
	)
)

func init() {
	prometheus.MustRegister(NodeCounts)
	prometheus.MustRegister(RequestCount)
	prometheus.MustRegister(RequestStatusCount)
	prometheus.MustRegister(NodeHealth)
}

// UpdateNodeCount updates the node count for a specific type
func UpdateNodeCount(nodeType string, count float64) {
	NodeCounts.WithLabelValues(nodeType).Set(count)
}

// IncrementRequestCount increments the request count for a specific type and node
func IncrementRequestCount(requestType, node string) {
	RequestCount.WithLabelValues(requestType, node).Inc()
}

// IncrementRequestStatusCount increments the request count for a specific type, node, and status code
func IncrementRequestStatusCount(requestType, node string, statusCode int) {
	RequestStatusCount.WithLabelValues(requestType, node, strconv.Itoa(statusCode)).Inc()
}

// UpdateNodeHealth updates the health status for a specific node
func UpdateNodeHealth(nodeType, node string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	NodeHealth.WithLabelValues(nodeType, node).Set(value)
}

// PrepareMetricsServer prepares a new HTTP server for Prometheus metrics
func PrepareMetricsServer(addr string, path string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(path, promhttp.Handler())

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return srv
}
