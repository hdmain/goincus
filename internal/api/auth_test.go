package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hdmain/goincus/internal/config"
)

func TestAPIKeyAuthRateLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.APIKeys = []string{"gic_test_key_exactly_32chars!!"}

	h := APIKeyAuth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
		req.RemoteAddr = "203.0.113.10:1234"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401 got %d", i, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 got %d", rr.Code)
	}

	ok := httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil)
	ok.RemoteAddr = "203.0.113.11:1"
	ok.Header.Set("X-API-Key", "gic_test_key_exactly_32chars!!")
	okRR := httptest.NewRecorder()
	h.ServeHTTP(okRR, ok)
	if okRR.Code != http.StatusOK {
		t.Fatalf("valid key: want 200 got %d", okRR.Code)
	}
}