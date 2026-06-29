// Package async implements single-flight background runners with shutdown tracking.
package async

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Runner gates one primary async operation and tracks related goroutines.
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

// New returns an idle Runner.
func New() *Runner {
	return &Runner{
		done: make(chan struct{}),
	}
}

// Close signals shutdown and waits for tracked work to finish.
// After Close returns, all subsequent TryStart() and TryGoBackground() calls
// return false. Contexts returned by Context() are cancelled.
// Close is safe to call repeatedly.
func (r *Runner) Close() {
	r.closeOnce.Do(func() {
		r.closeMu.Lock()
		r.closed = true
		close(r.done)
		r.closeMu.Unlock()
	})
	r.wg.Wait()
}

// IsRunning reports whether the primary operation is active.
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

// TryStart reserves the single primary-operation slot.
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

// Done releases the TryStart slot for synchronous operations that do not call Go.
func (r *Runner) Done() {
	r.running.Store(false)
	r.wg.Done()
}

// Context returns a context cancelled by timeout or Runner shutdown.
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

// Go runs the reserved primary operation in a goroutine.
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

// TryGoBackground starts independent background work outside a primary run.
// It returns false without spawning if Close() has already started.
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
