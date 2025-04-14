package cors

import "net/http"

const (
	AccessControlAllowOrigin  = "Access-Control-Allow-Origin"
	AccessControlAllowMethods = "Access-Control-Allow-Methods"
	AccessControlAllowHeaders = "Access-Control-Allow-Headers"
)

var SupportedHeaders = []string{
	AccessControlAllowOrigin,
	AccessControlAllowMethods,
	AccessControlAllowHeaders,
}

// WithCorsMiddleware is a middleware that enables CORS for all requests.
func WithCorsMiddleware(corsHeaders map[string]string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range SupportedHeaders {
			w.Header().Set(header, corsHeaders[header])
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func DeleteCorsHeaders(response *http.Response) {
	// Remove CORS headers from proxied response since we handle them in middleware.
	for _, header := range SupportedHeaders {
		response.Header.Del(header)
	}
}
