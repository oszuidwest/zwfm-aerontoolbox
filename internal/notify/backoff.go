package notify

import (
	"math/rand/v2"
	"sync"
	"time"
)

const (
	// backoffFactor is the multiplier for exponential backoff.
	backoffFactor = 2.0
	// backoffJitter adds randomness to prevent thundering herd (0.5 = up to 50%).
	backoffJitter = 0.5
)

// Backoff provides retry delay calculation with exponential growth.
type Backoff struct {
	mu       sync.Mutex
	current  time.Duration
	maxDelay time.Duration
}

// NewBackoff returns a new Backoff with the given initial and maximum delays.
func NewBackoff(initial, maxDelay time.Duration) *Backoff {
	return &Backoff{
		current:  initial,
		maxDelay: maxDelay,
	}
}

// Next returns the current delay and advances to the next value.
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	delay := b.current
	b.current = min(time.Duration(float64(b.current)*backoffFactor), b.maxDelay)

	// Add jitter to prevent thundering herd
	jitter := time.Duration(rand.Int64N(int64(float64(delay) * backoffJitter))) //nolint:gosec // Non-security random for jitter
	delay += jitter

	return delay
}
