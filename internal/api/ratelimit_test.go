package api

import (
	"sync"
	"testing"
	"time"
)

// TestAPIRateLimiterWindowLifecycle drives one limiter through a full window
// lifecycle: fill, deny (with the once-per-window log signal and a shrinking
// retry-after), an isolated second bucket, and the reset at window end.
func TestAPIRateLimiterWindowLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(1, func() time.Time {
		return now
	})

	steps := []struct {
		name           string
		advance        time.Duration
		key            string
		wantAllowed    bool
		wantRetryAfter time.Duration
		wantLogDenied  bool
	}{
		{
			name:        "first request is allowed",
			key:         "client-a",
			wantAllowed: true,
		},
		{
			name:           "first denial returns full window and log signal",
			key:            "client-a",
			wantRetryAfter: time.Minute,
			wantLogDenied:  true,
		},
		{
			name:           "second denial in same window is silent",
			key:            "client-a",
			wantRetryAfter: time.Minute,
		},
		{
			name:        "other bucket is unaffected",
			key:         "client-b",
			wantAllowed: true,
		},
		{
			name:           "retry-after shrinks as the window elapses",
			advance:        12*time.Second + 100*time.Millisecond,
			key:            "client-a",
			wantRetryAfter: 47*time.Second + 900*time.Millisecond,
		},
		{
			name:        "window reset allows again",
			advance:     47*time.Second + 900*time.Millisecond,
			key:         "client-a",
			wantAllowed: true,
		},
		{
			name:           "denial in new window logs again",
			key:            "client-a",
			wantRetryAfter: time.Minute,
			wantLogDenied:  true,
		},
	}

	for _, step := range steps {
		now = now.Add(step.advance)
		allowed, retryAfter, logDenied := limiter.allow(step.key)
		if allowed != step.wantAllowed || retryAfter != step.wantRetryAfter || logDenied != step.wantLogDenied {
			t.Fatalf("%s: allow(%q) = (%v, %s, %v), want (%v, %s, %v)",
				step.name, step.key, allowed, retryAfter, logDenied,
				step.wantAllowed, step.wantRetryAfter, step.wantLogDenied)
		}
	}
}

func TestAPIRateLimiterRetryAfterSecondsRoundsUp(t *testing.T) {
	t.Parallel()

	if got, want := retryAfterSeconds(47*time.Second+900*time.Millisecond), "48"; got != want {
		t.Fatalf("retryAfterSeconds(47.9s) = %q, want %q", got, want)
	}
	if got, want := retryAfterSeconds(0), "1"; got != want {
		t.Fatalf("retryAfterSeconds(0) = %q, want %q", got, want)
	}
}

func TestAPIRateLimiterAllowConcurrent(t *testing.T) {
	t.Parallel()

	const goroutines = 64

	now := time.Unix(1_700_000_000, 0)
	limiter := newTestRateLimiter(goroutines, func() time.Time {
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

	// The limit equals the number of goroutines, so a denial here proves every
	// concurrent increment landed.
	if allowed, _, _ := limiter.allow("client-a"); allowed {
		t.Fatal("request beyond the limit allowed = true, want false")
	}
}

func newTestRateLimiter(limit int, now func() time.Time) *apiRateLimiter {
	return &apiRateLimiter{
		limit:   limit,
		window:  time.Minute,
		clients: make(map[string]rateLimitState),
		now:     now,
	}
}
