package notify

import (
	"math/rand/v2"
	"sync"
	"time"
)

const (
	// backoffFactor is the multiplier for each retry delay.
	backoffFactor = 2.0
	// backoffJitter adds up to 50% random delay to avoid synchronized retries.
	backoffJitter = 0.5
)

// Backoff calculates retry delays with capped exponential growth.
type Backoff struct {
	mu       sync.Mutex
	current  time.Duration
	maxDelay time.Duration
}

// NewBackoff returns a Backoff starting at initial and capped at maxDelay.
func NewBackoff(initial, maxDelay time.Duration) *Backoff {
	return &Backoff{
		current:  initial,
		maxDelay: maxDelay,
	}
}

// Next returns the current jittered delay and advances the sequence.
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	delay := b.current
	b.current = min(time.Duration(float64(b.current)*backoffFactor), b.maxDelay)

	jitter := time.Duration(rand.Int64N(int64(float64(delay) * backoffJitter))) //nolint:gosec // Non-security random for jitter
	delay += jitter

	return delay
}
