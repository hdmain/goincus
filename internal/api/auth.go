package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hdmain/goincus/internal/config"
)

var errRateLimited = errMsg("rate limit exceeded: too many failed auth attempts")

type errMsg string

func (e errMsg) Error() string { return string(e) }

// APIKeyAuth rejects requests without a valid API key (constant-time via config.ValidAPIKey).
// Health endpoints remain public. Accepts Authorization: Bearer or X-API-Key.
// Failed auth is rate-limited per client IP.
func APIKeyAuth(cfg *config.Config) func(http.Handler) http.Handler {
	limiter := newAuthLimiter(20, time.Minute)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/healthz" || path == "/api/v1/health" {
				next.ServeHTTP(w, r)
				return
			}

			ip := clientIP(r)
			if limiter.blocked(ip) {
				writeError(w, http.StatusTooManyRequests, errRateLimited)
				return
			}

			key := extractAPIKey(r)
			if !cfg.ValidAPIKey(key) {
				limiter.fail(ip)
				writeError(w, http.StatusUnauthorized, errUnauthorized)
				return
			}
			limiter.success(ip)
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

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Real-IP"); xff != "" {
		return strings.TrimSpace(xff)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type authLimiter struct {
	mu       sync.Mutex
	maxFails int
	window   time.Duration
	hits     map[string]*authBucket
}

type authBucket struct {
	fails   int
	resetAt time.Time
}

func newAuthLimiter(maxFails int, window time.Duration) *authLimiter {
	return &authLimiter{
		maxFails: maxFails,
		window:   window,
		hits:     map[string]*authBucket{},
	}
}

func (l *authLimiter) bucket(ip string) *authBucket {
	now := time.Now()
	b, ok := l.hits[ip]
	if !ok || now.After(b.resetAt) {
		b = &authBucket{resetAt: now.Add(l.window)}
		l.hits[ip] = b
	}
	return b
}

func (l *authLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bucket(ip).fails >= l.maxFails
}

func (l *authLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bucket(ip).fails++
}

func (l *authLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, ip)
}
