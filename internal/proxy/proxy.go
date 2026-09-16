package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/akash-network/rpc-proxy/internal/halt"
	"github.com/akash-network/rpc-proxy/internal/metrics"
	proxyotel "github.com/akash-network/rpc-proxy/internal/otel"
	"github.com/akash-network/rpc-proxy/internal/proxy/cors"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Proxy struct {
	cfg  config.HealthConfig
	log  *slog.Logger
	init sync.Once
	ch   chan seed.Seed

	servers []*Server

	initialized  atomic.Bool
	shuttingDown atomic.Bool
	lb           LoadBalancer
	haltDetector *halt.Detector
}

type Updater func(s seed.Seed)

func (p *Proxy) Ready() bool { return p.initialized.Load() }

type haltErrorResponse struct {
	Error string `json:"error"`
}

func (p *Proxy) writeHaltResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "30")
	w.WriteHeader(http.StatusServiceUnavailable)
	return json.NewEncoder(w).Encode(haltErrorResponse{Error: p.haltDetector.HaltMessage()})
}

func (p *Proxy) Live() bool { return !p.shuttingDown.Load() && p.initialized.Load() }

func (p *Proxy) Stats() []ServerStat {
	var result []ServerStat
	for _, s := range p.servers {
		reqCount := s.requestCount.Load()
		result = append(result, ServerStat{
			Name:        s.name,
			URL:         s.Url.String(),
			Avg:         s.pings.Last(),
			Degraded:    !s.Healthy(),
			Initialized: reqCount > 0,
			Requests:    reqCount,
			ErrorRate:   s.ErrorRate(),
		})
	}
	sort.Sort(serverStats(result))
	return result
}

func (p *Proxy) doUpdate(providers []seed.Node) error {

	// add new servers
	for _, provider := range providers {
		// handle schemeless urls
		if !strings.HasPrefix(provider.Address, "http://") && !strings.HasPrefix(provider.Address, "https://") {
			provider.Address = fmt.Sprintf("https://%s", provider.Address)
		}

		target, err := url.Parse(provider.Address)
		if err != nil {
			return err
		}

		idx := slices.IndexFunc(p.servers, func(srv *Server) bool { return srv.name == provider.Provider })
		if idx == -1 {
			srv, err := newServer(
				provider.Provider,
				target,
				p.log.With("server_address", provider.Address),
				provider,
				newBreaker(p.cfg.EjectionThreshold, p.cfg.EjectionCooldown),
			)
			if err != nil {
				return err
			}

			p.servers = append(p.servers, srv)
		}
	}

	// remove deleted servers
	p.servers = slices.DeleteFunc(p.servers, func(srv *Server) bool {
		for _, provider := range providers {
			if provider.Provider == srv.name {
				if !provider.Healthy() { // provider matches but is unhealthy.
					p.log.Info("unhealthy server removed from pool",
						"name", srv.name,
						"catching_up", provider.Status.CatchingUp,
						"reachable", provider.Status.Reachable,
						"latest_block", provider.Status.IsLatestBlock)
					return true
				}
				return false
			}
		}
		p.log.Info("server was removed from pool", "name", srv.name)
		return true
	})

	p.initialized.Store(true)
	p.lb.Update(p.servers)

	return nil
}

// Start initializes and begins the proxy's update loop.
// It ensures the loop is started only once using sync.Once.
// The loop continuously listens for new seed data from the channel and calls the provided update function.
// When the context is cancelled, it marks the proxy as shutting down and exits.
func (p *Proxy) Start(ctx context.Context, update Updater) {
	p.init.Do(func() {
		go func() {
			for {
				select {
				case seed := <-p.ch:
					update(seed)
				case <-ctx.Done():
					p.shuttingDown.Store(true)
					return
				}
			}
		}()
	})
}

// newRedirectFollowingReverseProxy creates a configured httputil.ReverseProxy that automatically follows 301 redirects.
// It handles 301 redirects by following them automatically and returning the final response status and content.
func newRedirectFollowingReverseProxy(srv *Server, log *slog.Logger, proxyType string) *httputil.ReverseProxy {
	// Create a custom HTTP client that doesn't follow redirects automatically
	redirectClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Stop automatic redirect following
			return http.ErrUseLastResponse
		},
	}

	return &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			request.URL.Scheme = srv.Url.Scheme
			request.URL.Host = srv.Url.Host
			request.URL.Path = srv.Url.Path + request.URL.Path
			request.Host = srv.Url.Host
		},
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		ModifyResponse: func(response *http.Response) error {
			srv.recordSuccess()
			cors.DeleteCorsHeaders(response)

			metrics.IncrementRequestStatusCount(proxyType, srv.Url.String(), response.StatusCode)

			// Handle redirect responses by following them and returning content with final status
			if isRedirect(response.StatusCode) {

				location := response.Header.Get("Location")
				if location != "" {
					redirectURL, err := response.Request.URL.Parse(location)
					if err != nil {
						return fmt.Errorf("failed to parse redirect location %q: %w", location, err)
					}

					log.Info("following redirect", "original_status", response.StatusCode, "location", location, "resolved_url", redirectURL.String())

					// Only follow redirects for safe/idempotent methods to avoid body consumption issues
					if !isIdempotentMethod(response.Request.Method) {
						log.Warn("skipping redirect for non-idempotent method", "method", response.Request.Method)
						metrics.IncrementRequestStatusCount(proxyType, srv.Url.String(), response.StatusCode)
						return nil
					}

					ctx, span := proxyotel.Tracer().Start(response.Request.Context(), "proxy.redirect_follow",
						trace.WithAttributes(
							attribute.Int("redirect.status_code", response.StatusCode),
							attribute.String("redirect.location", location),
							attribute.String("redirect.resolved_url", redirectURL.String()),
						))
					defer span.End()

					redirectReq, err := http.NewRequestWithContext(ctx, response.Request.Method, redirectURL.String(), nil)
					if err != nil {
						return fmt.Errorf("failed to create redirect request to %q: %w", redirectURL.String(), err)
					}

					copyHeaders(response.Request.Header, redirectReq.Header)

					if response.Body != nil {
						response.Body.Close()
					}

					redirectResp, err := redirectClient.Do(redirectReq)
					if err != nil {
						return fmt.Errorf("failed to follow redirect to %q: %w", redirectURL.String(), err)
					}

					response.StatusCode = redirectResp.StatusCode
					response.Status = redirectResp.Status
					response.Body = redirectResp.Body
					response.ContentLength = redirectResp.ContentLength
					response.Header = redirectResp.Header.Clone()

					cors.DeleteCorsHeaders(response)

					metrics.IncrementRequestStatusCount(proxyType, srv.Url.String(), response.StatusCode)
				} else {
					log.Warn("redirect without Location header, serving as-is", "status", response.StatusCode)
					metrics.IncrementRequestStatusCount(proxyType, srv.Url.String(), response.StatusCode)
				}
			}

			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			// A client hangup cancels the request context. That is not the peer's
			// fault, so it must not count toward ejection.
			if !errors.Is(err, context.Canceled) {
				srv.recordFailure()
				metrics.IncrementUpstreamError(proxyType, srv.Url.String())
			}
			log.Error("reverse proxy error", "error", err)
			http.Error(writer, "could not proxy request", http.StatusInternalServerError)
		},
	}
}

// isRedirect checks if the status code represents a redirect
func isRedirect(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// isIdempotentMethod checks if the HTTP method is safe to replay without side effects
func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// copyHeaders copies headers from src to dst, excluding hop-by-hop headers
func copyHeaders(src, dst http.Header) {
	// Hop-by-hop headers that should not be forwarded
	hopByHopHeaders := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailer":             true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
		"Proxy-Connection":    true,
	}

	for name, headers := range src {
		if !hopByHopHeaders[name] {
			for _, h := range headers {
				dst.Add(name, h)
			}
		}
	}
}

// maxBufferedBody caps how much of a request body we buffer to enable
// transparent retries (1 MiB). JSON-RPC / REST payloads (including tx
// broadcasts) are tiny; anything larger falls back to the streaming,
// non-retryable path.
const maxBufferedBody = 1024 * 1024

// enableRetry buffers r.Body and installs r.GetBody so the reverse proxy's
// transport can rewind and replay the request.
//
// Inbound server requests never carry a GetBody, and httputil.ReverseProxy
// clones the request without inventing one. Without GetBody, Go's HTTP/2
// transport cannot retry a request whose body was already written when the
// upstream sends a GOAWAY (graceful connection drain), so it surfaces a 500
// ("could not proxy request"). Supplying GetBody lets the transport retry the
// request on a fresh connection.
//
// The retry is safe even for non-idempotent broadcasts: the HTTP/2 transport
// only replays a GOAWAY'd request when the stream sat above the frame's
// LastStreamID, i.e. the server guaranteed it never began processing it.
func enableRetry(r *http.Request) error {
	if r.Body == nil || r.Body == http.NoBody || r.GetBody != nil {
		return nil
	}
	// Skip bodies we know up front are too large to buffer safely.
	if r.ContentLength > maxBufferedBody {
		return nil
	}

	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBufferedBody+1))
	if err != nil {
		return err
	}
	if int64(len(buf)) > maxBufferedBody {
		// Body exceeds the cap (its length was unknown). Restore an equivalent
		// stream (buffered head + untouched tail) without enabling retry.
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		return nil
	}

	_ = r.Body.Close()
	r.ContentLength = int64(len(buf))
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	return nil
}
