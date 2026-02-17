// Package async provides utilities for managing async operations with graceful shutdown.
package async

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Runner manages a single async operation with graceful shutdown support.
// It ensures only one operation runs at a time and provides context integration
// for timeout and shutdown signaling.
type Runner struct {
	done    chan struct{}
	wg      sync.WaitGroup
	running atomic.Bool
}

// New creates a new Runner.
func New() *Runner {
	return &Runner{
		done: make(chan struct{}),
	}
}

// Close signals shutdown and waits for any running operations to complete.
// The context returned by Context() will be cancelled when Close is called.
func (r *Runner) Close() {
	close(r.done)
	r.wg.Wait()
}

// IsRunning returns true if an operation is currently running.
func (r *Runner) IsRunning() bool {
	return r.running.Load()
}

// TryStart attempts to start a new operation.
// Returns true if successful, false if an operation is already running.
func (r *Runner) TryStart() bool {
	return r.running.CompareAndSwap(false, true)
}

// Done marks the current operation as complete.
// Use this for synchronous operations that don't use Go().
func (r *Runner) Done() {
	r.running.Store(false)
}

// Context returns a context that is cancelled when either:
// - The timeout expires
// - Close() is called (shutdown requested)
//
// The caller must call the returned cancel function when done.
// The internal goroutine is tracked by the Runner's WaitGroup to ensure
// graceful shutdown waits for all context watchers to complete.
func (r *Runner) Context(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	// Track this goroutine to prevent leaks during shutdown
	r.wg.Go(func() {
		select {
		case <-r.done:
			cancel()
		case <-ctx.Done():
		}
	})

	return ctx, cancel
}

// Go starts the primary operation in a goroutine.
// The running flag is automatically cleared when fn returns.
// Use this for the main operation (backup, vacuum, etc).
func (r *Runner) Go(fn func()) {
	r.wg.Go(func() {
		defer r.running.Store(false)
		fn()
	})
}

// GoBackground starts a secondary operation in a goroutine.
// Unlike Go, this does not affect the running flag.
// Use this for follow-up operations like S3 sync or cleanup.
func (r *Runner) GoBackground(fn func()) {
	r.wg.Go(func() {
		fn()
	})
}
