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
//
// There are two patterns for background goroutines, with different shutdown semantics:
//
//   - GoChild: secondary work spawned from within an active Go() or Done() body.
//     The primary operation already holds a WaitGroup slot, so Close() is already
//     blocking - adding a child is always safe and will be waited on.
//
//   - TryGoBackground: independent background dispatch from outside any active
//     primary operation (e.g. an HTTP handler, a scheduler job). Returns false
//     and does nothing if Close() has already been called, so callers can log
//     or handle the drop explicitly instead of spawning a loose goroutine.
//
// Shutdown-safety guarantee: once Close() returns, no new goroutine launched
// via TryStart()+Go(), TryStart()+Done(), or TryGoBackground() can still be
// running. This is enforced by closeMu: each of those methods holds the lock
// while checking the closed flag and incrementing the WaitGroup, so Close()
// cannot finish wg.Wait() while a caller has passed the closed check but not
// yet reserved its WaitGroup slot.
//
// Close() is idempotent: the done channel is closed at most once (via
// closeOnce) and subsequent calls simply re-wait, returning immediately once
// the WaitGroup has already drained.
type Runner struct {
	done      chan struct{}
	wg        sync.WaitGroup
	running   atomic.Bool
	closeOnce sync.Once

	// closeMu guards closed and every wg.Add that must be atomic with Close().
	closeMu sync.Mutex
	closed  bool
}

// New creates a new Runner.
func New() *Runner {
	return &Runner{
		done: make(chan struct{}),
	}
}

// Close signals shutdown and waits for any running operations to complete.
// After Close returns, all subsequent TryStart() and TryGoBackground() calls
// will return false. The context returned by Context() will be cancelled.
// Safe to call more than once; the done channel is closed exactly once.
func (r *Runner) Close() {
	r.closeOnce.Do(func() {
		r.closeMu.Lock()
		r.closed = true
		close(r.done)
		r.closeMu.Unlock()
	})
	r.wg.Wait()
}

// IsRunning returns true if an operation is currently running.
func (r *Runner) IsRunning() bool {
	return r.running.Load()
}

// tryReserve atomically checks the closed flag and, if not closed, increments
// the WaitGroup. Returns false without touching the WaitGroup if closed.
// This is the shared building block for TryStart and TryGoBackground.
func (r *Runner) tryReserve() bool {
	r.closeMu.Lock()
	if r.closed {
		r.closeMu.Unlock()
		return false
	}
	r.wg.Add(1)
	r.closeMu.Unlock()
	return true
}

// TryStart attempts to start a new operation.
// Returns false if an operation is already running or Close() has been called.
// On success it reserves a WaitGroup slot before returning, so a concurrent
// Close() calling wg.Wait() will block until the paired Go() or Done() call
// releases the slot.
func (r *Runner) TryStart() bool {
	if !r.tryReserve() {
		return false
	}
	if !r.running.CompareAndSwap(false, true) {
		r.wg.Done()
		return false
	}
	return true
}

// Done marks the current operation as complete and releases the WaitGroup
// slot reserved by TryStart(). Use this for synchronous operations that
// don't use Go().
func (r *Runner) Done() {
	r.running.Store(false)
	r.wg.Done()
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

	// Track this goroutine to prevent leaks during shutdown.
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
// Must be called after a successful TryStart(); it consumes the WaitGroup
// slot reserved by TryStart().
func (r *Runner) Go(fn func()) {
	go func() {
		defer r.wg.Done()
		defer r.running.Store(false)
		fn()
	}()
}

// GoChild starts a secondary goroutine tracked by the runner's WaitGroup.
// Unlike Go, this does not affect the running flag.
//
// Use this for follow-up work spawned from within an active Go() or Done()
// body (e.g. async S3 upload after a backup completes). Because the primary
// operation already holds a WaitGroup slot, Close() is already blocked -
// adding a child here is always safe and will be waited on.
//
// Do NOT use this for independent dispatches from outside a primary operation;
// use TryGoBackground instead.
func (r *Runner) GoChild(fn func()) {
	r.wg.Go(fn)
}

// TryGoBackground starts a background goroutine as an independent dispatch -
// that is, from outside any currently active Go() or Done() body.
// Returns false and does nothing if Close() has already been called, allowing
// callers to log the drop explicitly.
func (r *Runner) TryGoBackground(fn func()) bool {
	if !r.tryReserve() {
		return false
	}
	go func() {
		defer r.wg.Done()
		fn()
	}()
	return true
}
