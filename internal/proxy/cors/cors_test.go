package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithCorsMiddleware(t *testing.T) {
	tests := []struct {
		name            string
		corsHeaders     map[string]string
		requestMethod   string
		expectedStatus  int
		expectedHeaders map[string]string
	}{
		{
			name: "regular request with CORS headers",
			corsHeaders: map[string]string{
				AccessControlAllowOrigin:  "*",
				AccessControlAllowMethods: "GET, POST",
				AccessControlAllowHeaders: "Content-Type",
			},
			requestMethod:  "GET",
			expectedStatus: http.StatusOK,
			expectedHeaders: map[string]string{
				AccessControlAllowOrigin:  "*",
				AccessControlAllowMethods: "GET, POST",
				AccessControlAllowHeaders: "Content-Type",
			},
		},
		{
			name: "OPTIONS request with CORS headers",
			corsHeaders: map[string]string{
				AccessControlAllowOrigin:  "*",
				AccessControlAllowMethods: "GET, POST",
				AccessControlAllowHeaders: "Content-Type",
			},
			requestMethod:  "OPTIONS",
			expectedStatus: http.StatusOK,
			expectedHeaders: map[string]string{
				AccessControlAllowOrigin:  "*",
				AccessControlAllowMethods: "GET, POST",
				AccessControlAllowHeaders: "Content-Type",
			},
		},
		{
			name:            "empty CORS headers",
			corsHeaders:     map[string]string{},
			requestMethod:   "GET",
			expectedStatus:  http.StatusOK,
			expectedHeaders: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			handler := WithCorsMiddleware(tt.corsHeaders, nextHandler)

			req := httptest.NewRequest(tt.requestMethod, "http://example.com/foo", nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			for header, expectedValue := range tt.expectedHeaders {
				got := w.Header().Get(header)
				if got != expectedValue {
					t.Errorf("expected header %s to be %q, got %q", header, expectedValue, got)
				}
			}
		})
	}
}

func TestDeleteCorsHeaders(t *testing.T) {
	tests := []struct {
		name           string
		headers        map[string]string
		expectedDelete bool
	}{
		{
			name: "delete all supported CORS headers",
			headers: map[string]string{
				AccessControlAllowOrigin:  "*",
				AccessControlAllowMethods: "GET, POST",
				AccessControlAllowHeaders: "Content-Type",
			},
			expectedDelete: true,
		},
		{
			name: "no supported CORS headers to delete",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			expectedDelete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &http.Response{
				Header: make(http.Header),
			}
			for k, v := range tt.headers {
				response.Header.Set(k, v)
			}

			DeleteCorsHeaders(response)

			for _, header := range SupportedHeaders {
				if tt.expectedDelete && response.Header.Get(header) != "" {
					t.Errorf("expected header %s to be deleted, but it still exists", header)
				}
			}

			if !tt.expectedDelete {
				for k, v := range tt.headers {
					if response.Header.Get(k) != v {
						t.Errorf("expected header %s to remain with value %s, got %s", k, v, response.Header.Get(k))
					}
				}
			}
		})
	}
}
