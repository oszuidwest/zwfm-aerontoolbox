package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAPIRateLimiterAllowResetsAfterWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(1, time.Minute, func() time.Time {
		return now
	})

	allowed, retryAfter, logDenied := limiter.allow("client-a")
	if !allowed {
		t.Fatal("first request allowed = false, want true")
	}
	if retryAfter != 0 {
		t.Fatalf("first retryAfter = %s, want 0", retryAfter)
	}
	if logDenied {
		t.Fatal("first logDenied = true, want false")
	}

	allowed, retryAfter, logDenied = limiter.allow("client-a")
	if allowed {
		t.Fatal("second request allowed = true, want false")
	}
	if retryAfter != time.Minute {
		t.Fatalf("second retryAfter = %s, want 1m0s", retryAfter)
	}
	if !logDenied {
		t.Fatal("second logDenied = false, want true")
	}

	now = now.Add(time.Minute)
	allowed, retryAfter, logDenied = limiter.allow("client-a")
	if !allowed {
		t.Fatal("request after window reset allowed = false, want true")
	}
	if retryAfter != 0 {
		t.Fatalf("request after window reset retryAfter = %s, want 0", retryAfter)
	}
	if logDenied {
		t.Fatal("request after window reset logDenied = true, want false")
	}
}

func TestAPIRateLimiterRetryAfterSeconds(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(1, time.Minute, func() time.Time {
		return now
	})

	allowed, _, _ := limiter.allow("client-a")
	if !allowed {
		t.Fatal("first request allowed = false, want true")
	}

	now = now.Add(12*time.Second + 100*time.Millisecond)
	allowed, retryAfter, _ := limiter.allow("client-a")
	if allowed {
		t.Fatal("second request allowed = true, want false")
	}
	if got, want := retryAfterSeconds(retryAfter), "48"; got != want {
		t.Fatalf("Retry-After = %q, want %q", got, want)
	}
}

func TestAPIRateLimiterBucketsAreIsolated(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(1, time.Minute, func() time.Time {
		return now
	})

	allowed, _, _ := limiter.allow("client-a")
	if !allowed {
		t.Fatal("first client-a request allowed = false, want true")
	}

	allowed, _, _ = limiter.allow("client-a")
	if allowed {
		t.Fatal("second client-a request allowed = true, want false")
	}

	allowed, _, _ = limiter.allow("client-b")
	if !allowed {
		t.Fatal("first client-b request allowed = false, want true")
	}
}

func TestAPIRateLimiterDenialLogSignalIsOncePerWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(1, time.Minute, func() time.Time {
		return now
	})

	allowed, _, logDenied := limiter.allow("client-a")
	if !allowed || logDenied {
		t.Fatalf("first request = allowed %v logDenied %v, want allowed true logDenied false", allowed, logDenied)
	}

	allowed, _, logDenied = limiter.allow("client-a")
	if allowed || !logDenied {
		t.Fatalf("first denial = allowed %v logDenied %v, want allowed false logDenied true", allowed, logDenied)
	}

	allowed, _, logDenied = limiter.allow("client-a")
	if allowed || logDenied {
		t.Fatalf("second denial = allowed %v logDenied %v, want allowed false logDenied false", allowed, logDenied)
	}

	now = now.Add(time.Minute)
	allowed, _, logDenied = limiter.allow("client-a")
	if !allowed || logDenied {
		t.Fatalf("new window request = allowed %v logDenied %v, want allowed true logDenied false", allowed, logDenied)
	}

	allowed, _, logDenied = limiter.allow("client-a")
	if allowed || !logDenied {
		t.Fatalf("new window denial = allowed %v logDenied %v, want allowed false logDenied true", allowed, logDenied)
	}
}

func TestAPIRateLimiterAllowConcurrent(t *testing.T) {
	const goroutines = 64

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(goroutines, time.Minute, func() time.Time {
		return now
	})

	var wg sync.WaitGroup
	results := make(chan bool, goroutines)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, _ := limiter.allow("client-a")
			results <- allowed
		}()
	}
	wg.Wait()
	close(results)

	var allowedCount int
	for allowed := range results {
		if !allowed {
			t.Fatal("concurrent request allowed = false, want true")
		}
		allowedCount++
	}
	if allowedCount != goroutines {
		t.Fatalf("allowed count = %d, want %d", allowedCount, goroutines)
	}

	limiter.mu.Lock()
	count := limiter.clients["client-a"].count
	limiter.mu.Unlock()
	if count != goroutines {
		t.Fatalf("stored count = %d, want %d", count, goroutines)
	}
}

func TestRateLimitMiddlewareSkipsPublicHealthPaths(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(0, time.Minute, func() time.Time {
		return now
	})
	server := &Server{}
	handler := server.rateLimitMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{"/health", "/api/health"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			for range 2 {
				req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
				rec := httptest.NewRecorder()

				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusNoContent {
					t.Fatalf("status code = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
				}
			}
		})
	}
}

func newTestRateLimiter(limit int, window time.Duration, now func() time.Time) *apiRateLimiter {
	return &apiRateLimiter{
		limit:   limit,
		window:  window,
		clients: make(map[string]rateLimitState),
		now:     now,
	}
}
