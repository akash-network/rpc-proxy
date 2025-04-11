package metrics

import (
	"net/http"

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
)

func init() {
	prometheus.MustRegister(NodeCounts)
	prometheus.MustRegister(RequestCount)
}

// UpdateNodeCount updates the node count for a specific type
func UpdateNodeCount(nodeType string, count float64) {
	NodeCounts.WithLabelValues(nodeType).Set(count)
}

// IncrementRequestCount increments the request count for a specific type and node
func IncrementRequestCount(requestType, node string) {
	RequestCount.WithLabelValues(requestType, node).Inc()
}

// PrepareMetricsServer prepares a new HTTP server for Prometheus metrics
func PrepareMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return srv
}
