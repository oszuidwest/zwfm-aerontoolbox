package service

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
)

// newTestService creates a FileMonitorService for testing.
// The notification service has no email configured so it never sends.
func newTestService(checks []config.FileMonitorCheckConfig) *FileMonitorService {
	cfg := &config.Config{}
	cfg.FileMonitor = config.FileMonitorConfig{
		Enabled: true,
		Checks:  checks,
	}
	notifySvc := notify.New(cfg)
	return newFileMonitorService(cfg, notifySvc)
}

func TestGraceRun_NoAlertsOnFirstRun(t *testing.T) {
	// Create a file that is stale (mod time in the past).
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// First run = grace run.
	svc.Run()

	status := svc.Status()
	if len(status.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(status.Checks))
	}

	// The file IS stale, so IsStale should be true.
	if !status.Checks[0].IsStale {
		t.Error("expected file to be reported as stale during grace run")
	}

	// But InAlert should be false because grace run doesn't set alert state.
	if status.Checks[0].InAlert {
		t.Error("expected InAlert=false during grace run (no alert state should be set)")
	}
}

func TestGraceRun_AlertOnSecondRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// First run = grace run (no alerts).
	svc.Run()

	// Second run should detect the stale file as a new alert.
	svc.Run()

	status := svc.Status()
	if !status.Checks[0].InAlert {
		t.Error("expected InAlert=true on second run for stale file")
	}
}

func TestGraceRun_NoPhantomRecovery(t *testing.T) {
	// File is stale during grace run, then becomes fresh before second run.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// Grace run: file is stale.
	svc.Run()

	// Touch the file so it's fresh.
	touchFile(t, path)

	// Second run: file is now fresh. Since grace run didn't set alertState,
	// wasInAlert=false, so no recovery should be triggered.
	svc.Run()

	status := svc.Status()
	if status.Checks[0].IsStale {
		t.Error("file should be fresh after touch")
	}
	if status.Checks[0].InAlert {
		t.Error("expected InAlert=false — no phantom recovery should occur")
	}
}

func TestAlertAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// Grace run.
	svc.Run()

	// Second run: file is stale → enters alert.
	svc.Run()
	if !svc.Status().Checks[0].InAlert {
		t.Fatal("expected InAlert=true after second run")
	}

	// Third run: file still stale → stays in alert (no duplicate alert).
	svc.Run()
	if !svc.Status().Checks[0].InAlert {
		t.Error("expected InAlert=true to persist for still-stale file")
	}

	// Touch the file → should recover.
	touchFile(t, path)

	svc.Run()
	if svc.Status().Checks[0].InAlert {
		t.Error("expected InAlert=false after file recovery")
	}
	if svc.Status().Checks[0].IsStale {
		t.Error("expected IsStale=false after file recovery")
	}
}

func TestMissingFile_FileExistsFalse(t *testing.T) {
	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Ghost", Path: "/nonexistent/path/file.mp3", MaxAgeMinutes: 10},
	})

	// Grace run.
	svc.Run()
	// Real run.
	svc.Run()

	result := svc.Status().Checks[0]
	if result.FileExists == nil {
		t.Fatal("expected file_exists to be non-nil for ENOENT")
	}
	if *result.FileExists {
		t.Error("expected file_exists=false for missing file")
	}
	if !result.IsStale {
		t.Error("expected missing file to be stale")
	}
	if result.Error != "" {
		t.Errorf("expected no error string for ENOENT, got %q", result.Error)
	}
}

func TestStatError_FileExistsNull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "restricted.mp3")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Remove all permissions from the parent directory so stat fails
	// with permission denied rather than ENOENT.
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: directories need execute bit for cleanup

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Restricted", Path: path, MaxAgeMinutes: 10},
	})

	// Grace run.
	svc.Run()
	// Real run.
	svc.Run()

	result := svc.Status().Checks[0]
	if result.FileExists != nil {
		t.Errorf("expected file_exists=null for permission error, got %v", *result.FileExists)
	}
	if result.Error == "" {
		t.Error("expected error field to contain the stat error")
	}
	if !result.IsStale {
		t.Error("expected stat error to be treated as stale")
	}
}

func TestFreshFile_NotStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})

	svc.Run() // grace
	svc.Run() // real

	result := svc.Status().Checks[0]
	if result.IsStale {
		t.Error("fresh file should not be stale")
	}
	if result.FileExists == nil || !*result.FileExists {
		t.Error("expected file_exists=true for existing fresh file")
	}
	if result.InAlert {
		t.Error("expected InAlert=false for fresh file")
	}
}

func TestStaleCount(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.mp3")
	stale := filepath.Join(dir, "stale.mp3")
	touchFile(t, fresh)
	writeAndAge(t, stale, 120*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Fresh", Path: fresh, MaxAgeMinutes: 30},
		{Name: "Stale", Path: stale, MaxAgeMinutes: 10},
	})

	svc.Run() // grace
	svc.Run() // real

	if got := svc.StaleCount(); got != 1 {
		t.Errorf("StaleCount() = %d, want 1", got)
	}
}

// hangingStat installs an osStat stub that hangs forever for the listed paths
// (until t.Cleanup releases it) and returns os.ErrNotExist for any other path.
// It returns per-path call counters so tests can assert single-flight behavior
// deterministically without touching runtime.NumGoroutine().
//
// async.Runner.Close() does not track these stat goroutines, so without the
// t.Cleanup release channel each "hang forever" stub would leak a goroutine
// for the rest of the test process. Always use t.Cleanup.
func hangingStat(t *testing.T, paths ...string) map[string]*atomic.Int64 {
	t.Helper()
	counters := make(map[string]*atomic.Int64, len(paths))
	hangSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		counters[p] = &atomic.Int64{}
		hangSet[p] = struct{}{}
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	prev := osStat
	t.Cleanup(func() { osStat = prev })

	osStat = func(path string) (os.FileInfo, error) {
		if _, hangs := hangSet[path]; !hangs {
			return nil, os.ErrNotExist
		}
		counters[path].Add(1)
		<-release
		return nil, errors.New("released by t.Cleanup")
	}
	return counters
}

func TestStatTimeout_TriggersStaleWithTimeoutKind(t *testing.T) {
	path := "/hang/news.mp3"
	hangingStat(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	})

	svc.Run()

	result := svc.Status().Checks[0]
	if !result.IsStale {
		t.Error("expected IsStale=true on stat timeout")
	}
	if result.ErrorKind != "stat_timeout" {
		t.Errorf("expected ErrorKind=%q, got %q", "stat_timeout", result.ErrorKind)
	}
	if result.Error == "" {
		t.Error("expected Error message to mention timeout")
	}
}

func TestSingleFlight_RepeatedRunsCallStatAtMostOnce(t *testing.T) {
	path := "/hang/news.mp3"
	counters := hangingStat(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	})

	for i := 0; i < 10; i++ {
		svc.Run()
	}

	if got := counters[path].Load(); got != 1 {
		t.Errorf("osStat called %d times for %q, want exactly 1 (single-flight broken)", got, path)
	}
}

func TestSingleFlight_DifferentPathsAreIndependent(t *testing.T) {
	pathA := "/hang/a.mp3"
	pathB := "/hang/b.mp3"
	counters := hangingStat(t, pathA, pathB)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "A", Path: pathA, MaxAgeMinutes: 10, StatTimeoutSec: 1},
		{Name: "B", Path: pathB, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	})

	svc.Run()

	if got := counters[pathA].Load(); got != 1 {
		t.Errorf("osStat for %q called %d times, want 1", pathA, got)
	}
	if got := counters[pathB].Load(); got != 1 {
		t.Errorf("osStat for %q called %d times, want 1", pathB, got)
	}
}

func TestSingleFlight_JoinAfterTimeoutReturnsImmediately(t *testing.T) {
	path := "/hang/news.mp3"
	hangingStat(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	})

	// First Run() establishes the flight and waits the full StatTimeout.
	svc.Run()

	// Second Run() must observe remaining<=0 on the still-hanging flight and
	// return immediately rather than waiting another full StatTimeout.
	start := time.Now()
	svc.Run()
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("second Run() took %v, expected near-instant return on already-budgeted flight", elapsed)
	}
}

func TestParallelChecks_FastFailNotBlockedBySlowStat(t *testing.T) {
	slow := "/hang/slow.mp3"
	fastA := "/missing/a.mp3"
	fastB := "/missing/b.mp3"
	hangingStat(t, slow) // only slow hangs; fastA/fastB get ErrNotExist immediately

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Slow", Path: slow, MaxAgeMinutes: 10, StatTimeoutSec: 1},
		{Name: "FastA", Path: fastA, MaxAgeMinutes: 10, StatTimeoutSec: 1},
		{Name: "FastB", Path: fastB, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	})

	start := time.Now()
	svc.Run()
	elapsed := time.Since(start)

	// Total should be ~1s (slow timeout), not ~3s (sequential).
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Run() took %v, expected ~1s — fast checks appear serialized behind slow stat", elapsed)
	}

	results := svc.Status().Checks
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.IsStale {
			t.Errorf("expected all checks stale, got %+v", r)
		}
	}
}

func TestStatTimeout_DefaultIsFiveSeconds(t *testing.T) {
	c := config.FileMonitorCheckConfig{StatTimeoutSec: 0}
	if got := c.StatTimeout(); got != 5*time.Second {
		t.Errorf("StatTimeout() = %v, want 5s", got)
	}

	c.StatTimeoutSec = -1
	if got := c.StatTimeout(); got != 5*time.Second {
		t.Errorf("negative StatTimeoutSec → StatTimeout() = %v, want 5s", got)
	}

	c.StatTimeoutSec = 12
	if got := c.StatTimeout(); got != 12*time.Second {
		t.Errorf("StatTimeoutSec=12 → StatTimeout() = %v, want 12s", got)
	}
}

// writeAndAge creates a file and sets its modification time to the past.
func writeAndAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-age)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

// touchFile creates or updates a file's modification time to now.
func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}
