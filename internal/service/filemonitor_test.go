package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
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

// hangingStatReleasable is like hangingStat but exposes the release channel
// so a test can resume the stubbed osStat mid-test rather than only during
// t.Cleanup. The returned release function is idempotent.
func hangingStatReleasable(t *testing.T, paths ...string) func() {
	t.Helper()
	hangSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		hangSet[p] = struct{}{}
	}
	var once sync.Once
	releaseCh := make(chan struct{})
	release := func() { once.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)

	prev := osStat
	t.Cleanup(func() { osStat = prev })

	osStat = func(path string) (os.FileInfo, error) {
		if _, hangs := hangSet[path]; !hangs {
			return nil, os.ErrNotExist
		}
		<-releaseCh
		return nil, errors.New("released by test")
	}
	return release
}

// waitForCompleted polls Status() until the given runID is completed and the
// service is idle. Fails the test after a short deadline.
func waitForCompleted(t *testing.T, svc *FileMonitorService, runID uint64) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		st := svc.Status()
		if !st.Running && st.CompletedRunID >= runID {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %d to complete", runID)
}

func TestTriggerCheck_RunsAndUpdatesStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})
	t.Cleanup(svc.Close)

	runID, err := svc.TriggerCheck()
	if err != nil {
		t.Fatalf("TriggerCheck: %v", err)
	}
	if runID != 1 {
		t.Errorf("expected first runID=1, got %d", runID)
	}

	// RunID is visible immediately.
	if got := svc.Status().RunID; got != runID {
		t.Errorf("Status().RunID = %d, want %d", got, runID)
	}

	waitForCompleted(t, svc, runID)

	st := svc.Status()
	if st.Running {
		t.Error("Status().Running should be false after completion")
	}
	if st.CompletedRunID != runID {
		t.Errorf("Status().CompletedRunID = %d, want %d", st.CompletedRunID, runID)
	}
	if st.LastCheckAt == nil {
		t.Error("Status().LastCheckAt should be set after a completed run")
	}
	if st.StartedAt == nil {
		t.Error("Status().StartedAt should be set after a completed run")
	}
	if len(st.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(st.Checks))
	}
}

func TestTriggerCheck_ReturnsConflictWhenAlreadyRunning(t *testing.T) {
	path := "/hang/news.mp3"
	release := hangingStatReleasable(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 5},
	})

	runID1, err := svc.TriggerCheck()
	if err != nil {
		t.Fatalf("first TriggerCheck: %v", err)
	}
	if runID1 != 1 {
		t.Errorf("first runID = %d, want 1", runID1)
	}

	runID2, err := svc.TriggerCheck()
	if err == nil {
		t.Fatalf("expected ConflictError on overlapping trigger, got runID=%d", runID2)
	}
	if runID2 != 0 {
		t.Errorf("conflict runID = %d, want 0", runID2)
	}
	var conflict *types.ConflictError
	if !errors.As(err, &conflict) {
		t.Errorf("expected *types.ConflictError, got %T: %v", err, err)
	}

	// runID counter should not advance on conflict.
	if got := svc.Status().RunID; got != runID1 {
		t.Errorf("Status().RunID = %d, want %d (counter must not advance on conflict)", got, runID1)
	}

	// Release before Close so Close() doesn't block on the hanging stat.
	release()
	waitForCompleted(t, svc, runID1)
	svc.Close()
}

func TestStatus_ShowsRunningTrueDuringRun(t *testing.T) {
	path := "/hang/news.mp3"
	release := hangingStatReleasable(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 30},
	})

	runID, err := svc.TriggerCheck()
	if err != nil {
		t.Fatalf("TriggerCheck: %v", err)
	}

	st := svc.Status()
	if !st.Running {
		t.Error("Status().Running should be true while run is active")
	}
	if st.RunID != runID {
		t.Errorf("Status().RunID = %d, want %d", st.RunID, runID)
	}
	if st.CompletedRunID >= runID {
		t.Errorf("Status().CompletedRunID = %d, want < %d (run not yet complete)", st.CompletedRunID, runID)
	}
	if st.StartedAt == nil {
		t.Error("Status().StartedAt should be set during active run")
	}

	// Release the stub so the run completes, then verify post-completion state.
	release()
	waitForCompleted(t, svc, runID)

	st = svc.Status()
	if st.Running {
		t.Error("Status().Running should be false after release")
	}
	if st.CompletedRunID != runID {
		t.Errorf("Status().CompletedRunID = %d, want %d after release", st.CompletedRunID, runID)
	}
	svc.Close()
}

func TestTriggerCheck_RunIDIsMonotonic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})
	defer svc.Close()

	for want := uint64(1); want <= 3; want++ {
		got, err := svc.TriggerCheck()
		if err != nil {
			t.Fatalf("TriggerCheck #%d: %v", want, err)
		}
		if got != want {
			t.Errorf("TriggerCheck #%d returned runID=%d, want %d", want, got, want)
		}
		waitForCompleted(t, svc, want)

		st := svc.Status()
		if st.RunID != want {
			t.Errorf("after run #%d, Status().RunID = %d, want %d", want, st.RunID, want)
		}
		if st.CompletedRunID != want {
			t.Errorf("after run #%d, Status().CompletedRunID = %d, want %d", want, st.CompletedRunID, want)
		}
	}
}

func TestCompletedRunID_LagsBehindRunIDDuringActiveRun(t *testing.T) {
	path := "/hang/news.mp3"
	release := hangingStatReleasable(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 30},
	})

	runID, err := svc.TriggerCheck()
	if err != nil {
		t.Fatalf("TriggerCheck: %v", err)
	}

	st := svc.Status()
	if st.RunID != runID {
		t.Errorf("Status().RunID = %d, want %d", st.RunID, runID)
	}
	// First-ever active run: completedRunID is still 0.
	if st.CompletedRunID != 0 {
		t.Errorf("Status().CompletedRunID = %d, want 0 (first active run)", st.CompletedRunID)
	}
	if st.CompletedRunID >= st.RunID {
		t.Errorf("CompletedRunID (%d) should lag RunID (%d) during active run", st.CompletedRunID, st.RunID)
	}

	release()
	waitForCompleted(t, svc, runID)
	svc.Close()
}

func TestScheduler_RunFileMonitor_TriggersCheck(t *testing.T) {
	// Exercise the actual code path used by cron: Scheduler.runFileMonitor.
	// A regression that breaks runFileMonitor (e.g. dropping TriggerCheck)
	// should fail this test, not just contract tests on FileMonitorService.
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	fmSvc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})
	defer fmSvc.Close()

	sch := &Scheduler{service: &AeronService{FileMonitor: fmSvc}}

	sch.runFileMonitor(context.Background())
	waitForCompleted(t, fmSvc, 1)

	st := fmSvc.Status()
	if st.RunID != 1 || st.CompletedRunID != 1 {
		t.Errorf("after scheduled tick: RunID=%d CompletedRunID=%d, want 1/1", st.RunID, st.CompletedRunID)
	}
}

func TestScheduler_RunFileMonitor_SkipsWhenAlreadyActive(t *testing.T) {
	// A scheduled tick that fires while a manual run is in flight must not
	// start a second run (would race + duplicate alert/recovery emails).
	// Tests Scheduler.runFileMonitor's conflict-swallowing behavior, not
	// just the underlying TryStart() contract.
	path := "/hang/news.mp3"
	release := hangingStatReleasable(t, path)

	fmSvc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 30},
	})

	sch := &Scheduler{service: &AeronService{FileMonitor: fmSvc}}

	manualID, err := fmSvc.TriggerCheck()
	if err != nil {
		t.Fatalf("manual TriggerCheck: %v", err)
	}

	// Cron tick fires while the manual run hangs. Must not error or panic,
	// and must not advance the run counter.
	sch.runFileMonitor(context.Background())

	if got := fmSvc.Status().RunID; got != manualID {
		t.Errorf("scheduler started a second run: RunID went %d → %d", manualID, got)
	}

	release()
	waitForCompleted(t, fmSvc, manualID)
	fmSvc.Close()
}

func TestStatus_NoTornSnapshotUnderConcurrentReads(t *testing.T) {
	// Real interleaving test for the torn-snapshot bug. Concurrent readers
	// snapshot Status() while a writer triggers many runs back-to-back. The
	// invariant under the fix: every observed snapshot pairs CompletedRunID
	// with the LastCheckAt that run actually published.
	//
	// Why this catches the old bug. The two-lock implementation published
	// lastCheck under statusMu, then bumped completedRunID under runStateMu
	// via a separate `defer markCompleted`. A reader landing between those
	// two unlocks would observe (CompletedRunID=N-1, LastCheckAt=tag[N]) —
	// a mismatch this assertion would flag. Under the single-lock fix
	// (publishCompleted writes both atomically), no such window exists, so
	// the test passes deterministically.
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})
	defer svc.Close()

	var tagMu sync.RWMutex
	tag := make(map[uint64]time.Time)
	record := func(runID uint64, lastCheckAt time.Time) {
		tagMu.Lock()
		tag[runID] = lastCheckAt
		tagMu.Unlock()
	}
	lookup := func(runID uint64) (time.Time, bool) {
		tagMu.RLock()
		defer tagMu.RUnlock()
		t, ok := tag[runID]
		return t, ok
	}

	// Pre-run twice so the tag map has entries before the readers start —
	// otherwise early reads find no tag and skip the assertion.
	for i := 0; i < 2; i++ {
		runID, err := svc.TriggerCheck()
		if err != nil {
			t.Fatalf("warmup TriggerCheck: %v", err)
		}
		waitForCompleted(t, svc, runID)
		record(runID, *svc.Status().LastCheckAt)
	}

	stop := make(chan struct{})
	var torn atomic.Int64
	var mismatchSample atomic.Pointer[string]
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := svc.Status()
				if st.LastCheckAt == nil || st.CompletedRunID == 0 {
					continue
				}
				want, ok := lookup(st.CompletedRunID)
				if !ok {
					// Writer hasn't recorded tag[N] yet; will be checked on next reads.
					continue
				}
				if !st.LastCheckAt.Equal(want) {
					torn.Add(1)
					if mismatchSample.Load() == nil {
						s := fmt.Sprintf("CompletedRunID=%d LastCheckAt=%v want=%v",
							st.CompletedRunID, st.LastCheckAt, want)
						mismatchSample.CompareAndSwap(nil, &s)
					}
				}
			}
		})
	}

	// Drive many runs concurrently with the readers. Each run's tag is
	// recorded synchronously after waitForCompleted so the readers always
	// see a consistent (runID → published LastCheckAt) ground truth.
	for i := 0; i < 100; i++ {
		runID, err := svc.TriggerCheck()
		if err != nil {
			t.Fatalf("TriggerCheck #%d: %v", i, err)
		}
		waitForCompleted(t, svc, runID)
		record(runID, *svc.Status().LastCheckAt)
	}

	close(stop)
	readers.Wait()

	if got := torn.Load(); got > 0 {
		sample := "(no sample captured)"
		if p := mismatchSample.Load(); p != nil {
			sample = *p
		}
		t.Errorf("observed %d torn snapshots; first mismatch: %s", got, sample)
	}
}

func TestStatus_CompletedRunIDMatchesLastCheck(t *testing.T) {
	// Regression test for the torn-snapshot bug: lastCheck and
	// completedRunID must be published together so a reader cannot observe
	// run N+1's Checks paired with completed_run_id=N. Each executeRun()
	// builds a fresh LastCheckAt; we record per-run values and verify the
	// final snapshot's LastCheckAt matches the run named by CompletedRunID.
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})
	defer svc.Close()

	lastCheckAtPerRun := make(map[uint64]time.Time)
	for i := uint64(1); i <= 5; i++ {
		runID, err := svc.TriggerCheck()
		if err != nil {
			t.Fatalf("TriggerCheck #%d: %v", i, err)
		}
		waitForCompleted(t, svc, runID)
		st := svc.Status()
		if st.LastCheckAt == nil {
			t.Fatalf("run %d: LastCheckAt nil", runID)
		}
		lastCheckAtPerRun[runID] = *st.LastCheckAt
		if st.CompletedRunID != runID {
			t.Errorf("run %d: CompletedRunID=%d, want %d", runID, st.CompletedRunID, runID)
		}
		// Tiny gap so consecutive runs have distinguishable LastCheckAt.
		time.Sleep(time.Millisecond)
	}

	// Final correlation: the snapshot's CompletedRunID must name the run
	// whose LastCheckAt we still observe.
	st := svc.Status()
	want := lastCheckAtPerRun[st.CompletedRunID]
	if !st.LastCheckAt.Equal(want) {
		t.Errorf("LastCheckAt=%v doesn't match recorded value for run %d (%v)",
			st.LastCheckAt, st.CompletedRunID, want)
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
