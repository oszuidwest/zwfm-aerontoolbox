package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mustReceive waits up to one second for a value on ch and fails the test if
// none arrives.
func mustReceive[T any](t *testing.T, ch <-chan T, label string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		panic("unreachable")
	}
}

// mustNotReceive asserts that ch stays silent for the given duration.
func mustNotReceive[T any](t *testing.T, ch <-chan T, wait time.Duration, label string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("unexpected receive on %s", label)
	case <-time.After(wait):
	}
}

// waitFor polls cond until it returns true or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

// writeFileAt writes a small file (creating parent directories) with the given
// modification time.
func writeFileAt(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
