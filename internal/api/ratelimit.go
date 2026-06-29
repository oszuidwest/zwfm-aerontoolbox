package api

import (
	"crypto/sha256"
	"encoding/hex"
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
	lastSeen    time.Time
	count       int
}

type apiRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]rateLimitState
	now     func() time.Time
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

func (l *apiRateLimiter) allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.clients[key]
	if st.windowStart.IsZero() || now.Sub(st.windowStart) >= l.window {
		st = rateLimitState{windowStart: now}
	}
	st.lastSeen = now

	if st.count >= l.limit {
		l.clients[key] = st
		return false, st.windowStart.Add(l.window).Sub(now)
	}

	st.count++
	l.clients[key] = st
	l.cleanupLocked(now)
	return true, 0
}

func (l *apiRateLimiter) cleanupLocked(now time.Time) {
	if len(l.clients) < 1024 {
		return
	}
	maxIdle := 2 * l.window
	for key, st := range l.clients {
		if now.Sub(st.lastSeen) > maxIdle {
			delete(l.clients, key)
		}
	}
}

func (s *Server) rateLimitMiddleware(limiter *apiRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retryAfter := limiter.allow(s.rateLimitKey(r))
			if !ok {
				w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
				respondError(w, http.StatusTooManyRequests, "Rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) rateLimitKey(r *http.Request) string {
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" && s.isValidAPIKey(apiKey) {
		sum := sha256.Sum256([]byte(apiKey))
		return "api-key:" + hex.EncodeToString(sum[:])
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	return "remote:" + host
}

func retryAfterSeconds(d time.Duration) string {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
