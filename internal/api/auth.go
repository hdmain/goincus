package api

import (
	"net/http"
	"strings"

	"github.com/hdmain/goincus/internal/config"
)

// APIKeyAuth rejects requests without a valid API key.
// Health endpoints remain public. Accepts Authorization: Bearer or X-API-Key.
func APIKeyAuth(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/healthz" || path == "/api/v1/health" {
				next.ServeHTTP(w, r)
				return
			}

			key := extractAPIKey(r)
			if !cfg.ValidAPIKey(key) {
				writeError(w, http.StatusUnauthorized, errUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractAPIKey(r *http.Request) string {
	if h := r.Header.Get("X-API-Key"); h != "" {
		return strings.TrimSpace(h)
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if strings.HasPrefix(auth, prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}
