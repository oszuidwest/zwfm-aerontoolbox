package api

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

type rateLimitState struct {
	windowStart time.Time
	count       int
	denyLogged  bool
}

type apiRateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	clients     map[string]rateLimitState
	lastCleanup time.Time
	now         func() time.Time
}

func newAPIRateLimiter(cfg *config.APIConfig) *apiRateLimiter {
	if !cfg.RateLimitEnabled {
		return nil
	}
	return &apiRateLimiter{
		limit:   cfg.GetRateLimitRequests(),
		window:  cfg.GetRateLimitWindow(),
		clients: make(map[string]rateLimitState),
		now:     time.Now,
	}
}

func (l *apiRateLimiter) allow(key string) (bool, time.Duration, bool) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.clients[key]
	if st.windowStart.IsZero() || now.Sub(st.windowStart) >= l.window {
		st = rateLimitState{windowStart: now}
	}

	allowed := st.count < l.limit
	var retryAfter time.Duration
	var logDenied bool
	if allowed {
		st.count++
	} else {
		logDenied = !st.denyLogged
		st.denyLogged = true
		retryAfter = st.windowStart.Add(l.window).Sub(now)
	}

	l.clients[key] = st
	if !allowed {
		return false, retryAfter, logDenied
	}

	l.cleanupLocked(now)
	return true, 0, false
}

// cleanupLocked sweeps idle entries at most once per window, so a large client
// map costs one O(n) scan per window instead of one per request.
func (l *apiRateLimiter) cleanupLocked(now time.Time) {
	if len(l.clients) < 1024 || now.Sub(l.lastCleanup) < l.window {
		return
	}
	l.lastCleanup = now
	maxIdle := 2 * l.window
	for key, st := range l.clients {
		if now.Sub(st.windowStart) > maxIdle {
			delete(l.clients, key)
		}
	}
}

func (s *Server) rateLimitMiddleware(limiter *apiRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublicHealthPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			key := s.rateLimitKey(r)
			ok, retryAfter, logDenied := limiter.allow(key)
			if !ok {
				if logDenied {
					slog.Warn("Rate limit exceeded",
						"bucket", key,
						"path", r.URL.Path,
						"method", r.Method)
				}
				w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
				respondError(w, http.StatusTooManyRequests, "Rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) rateLimitKey(r *http.Request) string {
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		if sum := sha256.Sum256([]byte(apiKey)); s.isValidAPIKeyHash(sum) {
			return "api-key:" + hex.EncodeToString(sum[:])
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	return "remote:" + host
}

func retryAfterSeconds(d time.Duration) string {
	return strconv.Itoa(max(1, int(math.Ceil(d.Seconds()))))
}
