package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testBody = `{"jsonrpc":"2.0","method":"broadcast_tx_sync","params":["deadbeef"],"id":1}`

// serverReq builds a request that mimics an inbound server request: it has a
// body but no GetBody (net/http only populates GetBody for client-constructed
// requests, and httptest.NewRequest sets it for known reader types).
func serverReq(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.GetBody = nil
	return r
}

// readAll reads r fully, failing the test on error.
func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestEnableRetry(t *testing.T) {
	t.Run("small body becomes replayable", func(t *testing.T) {
		r := serverReq(t, testBody)
		if err := enableRetry(r); err != nil {
			t.Fatalf("enableRetry: %v", err)
		}

		if r.GetBody == nil {
			t.Fatal("GetBody was not set")
		}
		if r.ContentLength != int64(len(testBody)) {
			t.Errorf("ContentLength = %d, want %d", r.ContentLength, len(testBody))
		}
		if got := readAll(t, r.Body); got != testBody {
			t.Errorf("body = %q, want %q", got, testBody)
		}
	})

	t.Run("nil body is a no-op", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.GetBody = nil
		if err := enableRetry(r); err != nil {
			t.Fatalf("enableRetry: %v", err)
		}
		if r.GetBody != nil {
			t.Error("GetBody should stay nil for a bodyless request")
		}
	})

	t.Run("existing GetBody is left untouched", func(t *testing.T) {
		r := serverReq(t, testBody)
		const marker = "sentinel-get-body"
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(marker)), nil
		}

		if err := enableRetry(r); err != nil {
			t.Fatalf("enableRetry: %v", err)
		}

		if r.GetBody == nil {
			t.Fatal("GetBody was cleared")
		}
		rc, err := r.GetBody()
		if err != nil {
			t.Fatalf("GetBody(): %v", err)
		}
		if got := readAll(t, rc); got != marker {
			t.Errorf("existing GetBody was replaced: got %q, want %q", got, marker)
		}
	})

	t.Run("oversized known length skips buffering but preserves body", func(t *testing.T) {
		big := string(bytes.Repeat([]byte("a"), maxBufferedBody+10))
		r := serverReq(t, big) // ContentLength is set to len(big) > cap
		if r.ContentLength <= maxBufferedBody {
			t.Fatalf("precondition: ContentLength %d not above cap", r.ContentLength)
		}

		if err := enableRetry(r); err != nil {
			t.Fatalf("enableRetry: %v", err)
		}

		if r.GetBody != nil {
			t.Error("retry should not be enabled for oversized bodies")
		}
		if got := readAll(t, r.Body); got != big {
			t.Error("body must remain fully readable")
		}
	})

	t.Run("oversized unknown length skips buffering but preserves body", func(t *testing.T) {
		big := string(bytes.Repeat([]byte("b"), maxBufferedBody+10))
		r := serverReq(t, big)
		r.ContentLength = -1 // length unknown up front
		r.Body = io.NopCloser(strings.NewReader(big))

		if err := enableRetry(r); err != nil {
			t.Fatalf("enableRetry: %v", err)
		}

		if r.GetBody != nil {
			t.Error("retry should not be enabled for oversized bodies")
		}
		if got := readAll(t, r.Body); got != big {
			t.Error("buffered head + untouched tail must reassemble")
		}
	})
}

// TestEnableRetry_ReplayAfterConsume is the core property: after the body has
// been fully written (as the transport would on a first, GOAWAY'd attempt),
// GetBody still yields the complete body again for the retry — repeatedly.
func TestEnableRetry_ReplayAfterConsume(t *testing.T) {
	r := serverReq(t, testBody)
	if err := enableRetry(r); err != nil {
		t.Fatalf("enableRetry: %v", err)
	}

	if got := readAll(t, r.Body); got != testBody {
		t.Fatalf("first read = %q, want %q", got, testBody)
	}

	if r.GetBody == nil {
		t.Fatal("GetBody was not set")
	}
	for attempt := 0; attempt < 2; attempt++ {
		rc, err := r.GetBody()
		if err != nil {
			t.Fatalf("GetBody() attempt %d: %v", attempt, err)
		}
		if got := readAll(t, rc); got != testBody {
			t.Errorf("replay attempt %d = %q, want %q", attempt, got, testBody)
		}
	}
}

// TestEnableRetry_ClonePreservesGetBody documents the assumption that
// httputil.ReverseProxy's req.Clone carries GetBody through to the outbound
// request the transport sees.
func TestEnableRetry_ClonePreservesGetBody(t *testing.T) {
	r := serverReq(t, testBody)
	if err := enableRetry(r); err != nil {
		t.Fatalf("enableRetry: %v", err)
	}

	clone := r.Clone(context.Background())
	if clone.GetBody == nil {
		t.Fatal("clone lost GetBody")
	}
	rc, err := clone.GetBody()
	if err != nil {
		t.Fatalf("clone GetBody(): %v", err)
	}
	if got := readAll(t, rc); got != testBody {
		t.Errorf("clone body = %q, want %q", got, testBody)
	}
}

// TestEnableRetry_ForwardsBodyIntact is a regression guard: routing a
// retry-enabled request through the real reverse proxy still delivers the full
// body to the backend.
func TestEnableRetry_ForwardsBodyIntact(t *testing.T) {
	var received string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = readAll(t, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, received)
	}))
	t.Cleanup(backend.Close)

	targetURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	srv := &Server{Url: targetURL}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := newRedirectFollowingReverseProxy(srv, logger, "rpc")

	r := serverReq(t, testBody)
	if err := enableRetry(r); err != nil {
		t.Fatalf("enableRetry: %v", err)
	}
	if r.GetBody == nil {
		t.Fatal("GetBody was not set")
	}

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if received != testBody {
		t.Errorf("backend received %q, want %q", received, testBody)
	}
	if got := w.Body.String(); got != testBody {
		t.Errorf("response body = %q, want %q", got, testBody)
	}
}
