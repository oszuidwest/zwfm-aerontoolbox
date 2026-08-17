package async

import (
	"sync/atomic"
	"testing"
	"time"
)

// mustReceiveWithin fails the test if ch does not receive (or close) within d.
func mustReceiveWithin(t *testing.T, ch <-chan struct{}, d time.Duration, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s timed out after %v", label, d)
	}
}

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

// TestClose_WaitsForTrackedGoroutines verifies the shared WaitGroup contract:
// a goroutine spawned via GoChild (from within an active Go() body) or via
// TryGoBackground before Close is waited on by Close(). This is the structural
// guarantee that allows S3 sync after backup to complete even when shutdown is
// requested during the backup.
func TestClose_WaitsForTrackedGoroutines(t *testing.T) {
	tests := []struct {
		name  string
		spawn func(t *testing.T, r *Runner, work func())
	}{
		{
			name: "GoChild",
			spawn: func(t *testing.T, r *Runner, work func()) {
				t.Helper()
				if !r.TryStart() {
					t.Fatal("TryStart failed")
				}
				r.Go(func() { r.GoChild(work) })
			},
		},
		{
			name: "TryGoBackground",
			spawn: func(t *testing.T, r *Runner, work func()) {
				t.Helper()
				if !r.TryGoBackground(work) {
					t.Fatal("TryGoBackground failed before Close")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()

			started := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})

			tt.spawn(t, r, func() {
				close(started)
				<-release
				close(finished)
			})

			<-started

			closeDone := make(chan struct{})
			go func() {
				r.Close()
				close(closeDone)
			}()

			// The worker is still blocked on release, so Close must not have
			// returned yet - that is the blocking behavior under test.
			select {
			case <-closeDone:
				t.Fatal("Close returned while tracked goroutine was still blocked")
			default:
			}

			close(release)
			mustReceiveWithin(t, closeDone, 2*time.Second, "Close waiting for tracked goroutine")

			select {
			case <-finished:
				// Tracked goroutine completed before Close returned.
			default:
				t.Fatal("Close returned before tracked goroutine finished")
			}
		})
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
	if called.Load() {
		t.Fatal("TryGoBackground should not have run fn after Close")
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

	mustReceiveWithin(t, done, time.Second, "second Close call")
}

// TestTryStart_AtomicWithClose verifies the core shutdown-safety guarantee:
// once Close returns, no goroutine launched via TryStart+Go can still be
// running. This catches the race where TryStart succeeds but Go has not yet
// added to the WaitGroup before wg.Wait() is called.
func TestTryStart_AtomicWithClose(t *testing.T) {
	for range 100 {
		r := New()

		closeDone := make(chan struct{})
		go func() {
			r.Close()
			close(closeDone)
		}()

		var fin chan struct{}
		if r.TryStart() {
			fin = make(chan struct{})
			r.Go(func() { close(fin) })
		}

		mustReceiveWithin(t, closeDone, 2*time.Second, "Close racing TryStart")
		if fin != nil {
			select {
			case <-fin:
				// Go body finished before Close returned, as guaranteed.
			default:
				t.Fatal("goroutine still running after Close returned")
			}
		}
	}
}
