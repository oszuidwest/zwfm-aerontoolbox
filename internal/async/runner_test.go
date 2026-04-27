package async

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestTryStart_BlockedAfterClose verifies that TryStart returns false once
// Close has been called.
func TestTryStart_BlockedAfterClose(t *testing.T) {
	r := New()
	r.Close()
	if r.TryStart() {
		t.Fatal("TryStart should return false after Close")
	}
}

// TestTryStart_BlockedWhenRunning verifies that a second TryStart call fails
// while a primary operation is running.
func TestTryStart_BlockedWhenRunning(t *testing.T) {
	r := New()
	if !r.TryStart() {
		t.Fatal("first TryStart should succeed")
	}
	if r.TryStart() {
		t.Fatal("second TryStart should fail while running")
	}
	r.Done()
}

// TestGoChild_WaitedOnByClose verifies the GoChild contract: a goroutine
// spawned via GoChild from within an active Go() body is waited on by Close().
// This is the structural guarantee that allows S3 sync after backup to complete
// even when shutdown is requested during the backup.
func TestGoChild_WaitedOnByClose(t *testing.T) {
	r := New()

	childStarted := make(chan struct{})
	childFinished := make(chan struct{})

	if !r.TryStart() {
		t.Fatal("TryStart failed")
	}
	r.Go(func() {
		r.GoChild(func() {
			close(childStarted)
			// Simulate S3 upload work.
			time.Sleep(50 * time.Millisecond)
			close(childFinished)
		})
	})

	<-childStarted

	closeDone := make(chan struct{})
	go func() {
		r.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close timed out waiting for GoChild goroutine")
	}

	select {
	case <-childFinished:
		// GoChild goroutine completed before Close returned.
	default:
		t.Fatal("Close returned before GoChild goroutine finished")
	}
}

// TestGoChild_DoesNotCheckClosed verifies that GoChild is ungated: calling it
// after Close does not block or panic, consistent with its contract that it is
// only safe to call from within an active primary (where Close is already
// blocking on the WaitGroup).
func TestGoChild_DoesNotCheckClosed(t *testing.T) {
	r := New()
	r.Close()

	// GoChild has no closed gate — it simply calls wg.Go. Calling it after
	// Close is a caller contract violation, but it must not panic.
	done := make(chan struct{})
	r.GoChild(func() { close(done) })

	select {
	case <-done:
		// Goroutine ran — GoChild did not block or panic.
	case <-time.After(time.Second):
		t.Fatal("GoChild goroutine did not run")
	}
}

// TestTryGoBackground_ReturnsFalseAfterClose verifies that TryGoBackground
// is gated: it returns false and does not spawn a goroutine after Close.
func TestTryGoBackground_ReturnsFalseAfterClose(t *testing.T) {
	r := New()
	r.Close()

	var called atomic.Bool
	if r.TryGoBackground(func() { called.Store(true) }) {
		t.Fatal("TryGoBackground should return false after Close")
	}

	// Give a moment to confirm no goroutine was spawned.
	time.Sleep(20 * time.Millisecond)
	if called.Load() {
		t.Fatal("TryGoBackground should not have spawned a goroutine after Close")
	}
}

// TestTryGoBackground_WaitedOnByClose verifies that a goroutine started via
// TryGoBackground before Close is waited on by Close — the same WaitGroup
// guarantee as GoChild.
func TestTryGoBackground_WaitedOnByClose(t *testing.T) {
	r := New()

	started := make(chan struct{})
	finished := make(chan struct{})

	if !r.TryGoBackground(func() {
		close(started)
		time.Sleep(50 * time.Millisecond)
		close(finished)
	}) {
		t.Fatal("TryGoBackground failed before Close")
	}

	<-started

	closeDone := make(chan struct{})
	go func() {
		r.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close timed out waiting for TryGoBackground goroutine")
	}

	select {
	case <-finished:
	default:
		t.Fatal("Close returned before TryGoBackground goroutine finished")
	}
}

// TestClose_Idempotent verifies that calling Close more than once does not
// panic and that both calls return.
func TestClose_Idempotent(t *testing.T) {
	r := New()
	r.Close()

	done := make(chan struct{})
	go func() {
		r.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Close call hung")
	}
}

// TestTryStart_AtomicWithClose verifies the core shutdown-safety guarantee:
// once Close returns, no goroutine launched via TryStart+Go can still be
// running. This catches the race where TryStart succeeds but Go has not yet
// added to the WaitGroup before wg.Wait() is called.
func TestTryStart_AtomicWithClose(t *testing.T) {
	const iterations = 500
	for range iterations {
		r := New()

		var running atomic.Bool

		closeDone := make(chan struct{})
		go func() {
			r.Close()
			close(closeDone)
		}()

		if r.TryStart() {
			r.Go(func() {
				running.Store(true)
				time.Sleep(time.Millisecond)
				running.Store(false)
			})
		}

		<-closeDone
		if running.Load() {
			t.Fatal("goroutine still running after Close returned")
		}
	}
}
