package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/knoguchi/rag/internal/auth"
)

func TestAPIKeyHeaderMatcher(t *testing.T) {
	for _, variant := range []string{"X-API-Key", "x-api-key", "X-Api-Key"} {
		got, ok := apiKeyHeaderMatcher(variant)
		if !ok || got != auth.APIKeyHeader {
			t.Errorf("expected %q forwarded as %q, got %q ok=%v", variant, auth.APIKeyHeader, got, ok)
		}
	}

	// Arbitrary headers are not forwarded
	if _, ok := apiKeyHeaderMatcher("X-Random-Header"); ok {
		t.Error("expected arbitrary header to not be forwarded")
	}
}

func corsRequest(t *testing.T, origins []string, origin string) http.Header {
	t.Helper()
	handler := corsMiddleware(origins)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/query", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Header()
}

func TestCORS_WildcardHasNoCredentials(t *testing.T) {
	h := corsRequest(t, []string{"*"}, "https://evil.example")
	if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected literal *, got %q", got)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("wildcard CORS must not allow credentials")
	}
}

func TestCORS_ExactMatchEchoesOrigin(t *testing.T) {
	h := corsRequest(t, []string{"https://app.example"}, "https://app.example")
	if got := h.Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("expected matched origin echoed, got %q", got)
	}
	if h.Get("Vary") != "Origin" {
		t.Error("expected Vary: Origin on matched origin")
	}
}

func TestCORS_UnmatchedOriginGetsNoHeaders(t *testing.T) {
	h := corsRequest(t, []string{"https://app.example"}, "https://evil.example")
	if h.Get("Access-Control-Allow-Origin") != "" {
		t.Error("unmatched origin must not receive CORS headers")
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("unmatched origin must not receive credentials header")
	}
}
