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

// newTestService creates a FileMonitorService for testing. Variadic for
// brevity at call sites (all tests pass an inline literal). The notification
// service has no email configured so it never sends. Constructor errors fail
// the test — they would always indicate an invalid ActiveWindow in the test
// fixture, never a runtime condition worth proceeding past.
func newTestService(t *testing.T, checks ...config.FileMonitorCheckConfig) *FileMonitorService {
	t.Helper()
	cfg := &config.Config{}
	cfg.FileMonitor = config.FileMonitorConfig{
		Enabled: true,
		Checks:  checks,
	}
	notifySvc := notify.New(cfg)
	svc, err := newFileMonitorService(cfg, notifySvc)
	if err != nil {
		t.Fatalf("newFileMonitorService: %v", err)
	}
	return svc
}

func TestGraceRun_NoAlertsOnFirstRun(t *testing.T) {
	// Create a file that is stale (mod time in the past).
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10},
	)

	// First run = grace run.
	svc.run()

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10},
	)

	// First run = grace run (no alerts).
	svc.run()

	// Second run should detect the stale file as a new alert.
	svc.run()

	status := svc.Status()
	if !status.Checks[0].InAlert {
		t.Error("expected InAlert=true on second run for stale file")
	}
}

func TestGraceRun_NoPhantomRecovery(t *testing.T) {
	// File is stale during grace run, then becomes fresh before second run.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10},
	)

	// Grace run: file is stale.
	svc.run()

	// Touch the file so it's fresh.
	touchFile(t, path)

	// Second run: file is now fresh. Since grace run didn't set alertState,
	// wasInAlert=false, so no recovery should be triggered.
	svc.run()

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10},
	)
	notifier := &captureFileMonitorNotifier{}
	svc.notify = notifier

	// Grace run.
	svc.run()

	// Second run: file is stale → enters alert.
	svc.run()
	if !svc.Status().Checks[0].InAlert {
		t.Fatal("expected InAlert=true after second run")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected exactly 1 alert call, got %d", len(notifier.alerts))
	}

	// Third run: file still stale → stays in alert (no duplicate alert).
	svc.run()
	if !svc.Status().Checks[0].InAlert {
		t.Error("expected InAlert=true to persist for still-stale file")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected still exactly 1 alert call after duplicate run, got %d", len(notifier.alerts))
	}

	// Touch the file → should recover.
	touchFile(t, path)

	svc.run()
	if svc.Status().Checks[0].InAlert {
		t.Error("expected InAlert=false after file recovery")
	}
	if svc.Status().Checks[0].IsStale {
		t.Error("expected IsStale=false after file recovery")
	}
	if len(notifier.recoveries) != 1 {
		t.Fatalf("expected exactly 1 recovery call, got %d", len(notifier.recoveries))
	}
}

func TestAlertSuppressedAcrossWindowGap(t *testing.T) {
	// 1. Inside window: file goes stale → alert fires exactly once.
	pinNow(t, timeAt(10, 0))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path, 60*time.Minute)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{
			Name: "Daytime news", Path: path, MaxAgeMinutes: 10,
			ActiveWindow: "08:00-20:00",
		},
	)
	notifier := &captureFileMonitorNotifier{}
	svc.notify = notifier

	svc.run() // grace
	svc.run() // real → alert
	if !svc.Status().Checks[0].InAlert {
		t.Fatal("expected InAlert=true after entering alert inside window")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected 1 alert dispatch, got %d", len(notifier.alerts))
	}

	// 2. Window closes → run outside window, no state mutation, no duplicate.
	pinNow(t, timeAt(22, 0))
	makeStale(t, path, 60*time.Minute)
	svc.run()

	r := svc.Status().Checks[0]
	if !r.IsStale {
		t.Error("expected IsStale=true outside window (transparency)")
	}
	if r.InAlert {
		t.Error("expected InAlert=false outside window")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected still 1 alert dispatch after outside-window run, got %d", len(notifier.alerts))
	}

	// 3. Window reopens while file is still stale → no duplicate alert.
	pinNow(t, timeAt(10, 0))
	makeStale(t, path, 60*time.Minute)
	svc.run()

	r = svc.Status().Checks[0]
	if !r.InAlert {
		t.Error("expected InAlert=true once window reopens with still-stale file")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected no duplicate alert after window reopens, got %d", len(notifier.alerts))
	}

	// 4. File becomes fresh inside window → recovery fires exactly once.
	makeFresh(t, path)
	svc.run()

	r = svc.Status().Checks[0]
	if r.IsStale {
		t.Error("expected IsStale=false after recovery")
	}
	if r.InAlert {
		t.Error("expected InAlert=false after recovery")
	}
	if len(notifier.recoveries) != 1 {
		t.Fatalf("expected 1 recovery dispatch, got %d", len(notifier.recoveries))
	}
}

func TestMissingFile_FileExistsFalse(t *testing.T) {
	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "Ghost", Path: "/nonexistent/path/file.mp3", MaxAgeMinutes: 10},
	)

	// Grace run.
	svc.run()
	// Real run.
	svc.run()

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
	if result.ErrorKind != FileCheckErrorKindNotFound {
		t.Errorf("expected ErrorKind=%q, got %q", FileCheckErrorKindNotFound, result.ErrorKind)
	}
}

func TestPermissionDenied_FileExistsNull(t *testing.T) {
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "Restricted", Path: path, MaxAgeMinutes: 10},
	)

	// Grace run.
	svc.run()
	// Real run.
	svc.run()

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
	if result.ErrorKind != FileCheckErrorKindPermission {
		t.Errorf("expected ErrorKind=%q, got %q", FileCheckErrorKindPermission, result.ErrorKind)
	}
}

func TestGenericStatError_UsesStatErrorKind(t *testing.T) {
	prev := osStat
	t.Cleanup(func() { osStat = prev })

	osStat = func(path string) (os.FileInfo, error) {
		return nil, errors.New("input/output error")
	}

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "Broken", Path: "/broken/news.mp3", MaxAgeMinutes: 10},
	)

	svc.run() // grace
	svc.run() // real

	result := svc.Status().Checks[0]
	if result.FileExists != nil {
		t.Errorf("expected file_exists=null for generic stat error, got %v", *result.FileExists)
	}
	if result.ErrorKind != FileCheckErrorKindStatError {
		t.Errorf("expected ErrorKind=%q, got %q", FileCheckErrorKindStatError, result.ErrorKind)
	}
}

func TestFreshFile_NotStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)

	svc.run() // grace
	svc.run() // real

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "Fresh", Path: fresh, MaxAgeMinutes: 30},
		config.FileMonitorCheckConfig{Name: "Stale", Path: stale, MaxAgeMinutes: 10},
	)

	svc.run() // grace
	svc.run() // real

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	svc.run()

	result := svc.Status().Checks[0]
	if !result.IsStale {
		t.Error("expected IsStale=true on stat timeout")
	}
	if result.ErrorKind != FileCheckErrorKindStatTimeout {
		t.Errorf("expected ErrorKind=%q, got %q", FileCheckErrorKindStatTimeout, result.ErrorKind)
	}
	if result.Error == "" {
		t.Error("expected Error message to mention timeout")
	}
}

type captureFileMonitorNotifier struct {
	alerts     [][]notify.FileAlertResult
	recoveries [][]notify.FileAlertResult
}

func (n *captureFileMonitorNotifier) SendFileAlerts(alerts []notify.FileAlertResult) {
	n.alerts = append(n.alerts, append([]notify.FileAlertResult(nil), alerts...))
}

func (n *captureFileMonitorNotifier) SendFileRecoveries(recoveries []notify.FileAlertResult) {
	n.recoveries = append(n.recoveries, append([]notify.FileAlertResult(nil), recoveries...))
}

func TestSingleFlight_RepeatedRunsCallStatAtMostOnce(t *testing.T) {
	path := "/hang/news.mp3"
	counters := hangingStat(t, path)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	for i := 0; i < 10; i++ {
		svc.run()
	}

	if got := counters[path].Load(); got != 1 {
		t.Errorf("osStat called %d times for %q, want exactly 1 (single-flight broken)", got, path)
	}
}

func TestSingleFlight_DifferentPathsAreIndependent(t *testing.T) {
	pathA := "/hang/a.mp3"
	pathB := "/hang/b.mp3"
	counters := hangingStat(t, pathA, pathB)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "A", Path: pathA, MaxAgeMinutes: 10, StatTimeoutSec: 1},
		config.FileMonitorCheckConfig{Name: "B", Path: pathB, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	svc.run()

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	// First Run() establishes the flight and waits the full StatTimeout.
	svc.run()

	// Second Run() must observe remaining<=0 on the still-hanging flight and
	// return immediately rather than waiting another full StatTimeout.
	start := time.Now()
	svc.run()
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "Slow", Path: slow, MaxAgeMinutes: 10, StatTimeoutSec: 1},
		config.FileMonitorCheckConfig{Name: "FastA", Path: fastA, MaxAgeMinutes: 10, StatTimeoutSec: 1},
		config.FileMonitorCheckConfig{Name: "FastB", Path: fastB, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	start := time.Now()
	svc.run()
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 5},
	)

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 30},
	)

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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 30},
	)

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

	fmSvc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
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

	fmSvc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 30},
	)

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

func TestTriggerCheck_NoConflictAfterStatusReportsIdle(t *testing.T) {
	// Status().Running must agree with the runner's single-flight gate: once
	// a client observes Running == false, the next TriggerCheck() must
	// succeed without 409. Earlier the published "running" was cleared in
	// publishCompleted (inside fn), but the runner's own Store(false) only
	// fires in a defer after fn returns — so a brief window let the API
	// report idle while TryStart() still rejected.
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
	defer svc.Close()

	// 200 back-to-back triggers; if Status().Running could ever lead the
	// runner gate, one of these waitForCompleted → TriggerCheck pairs would
	// hit a 409. Reproducer at -count=50 originally failed ~5/50.
	for i := 0; i < 200; i++ {
		runID, err := svc.TriggerCheck()
		if err != nil {
			t.Fatalf("TriggerCheck #%d (after %d successful back-to-backs): %v", i, i, err)
		}
		waitForCompleted(t, svc, runID)
		if svc.Status().Running {
			t.Fatalf("iter %d: waitForCompleted returned but Status().Running == true", i)
		}
	}
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
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

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
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

// pinNow overrides nowFunc for the duration of the test. The fixed time is
// used both for the file-age comparison and the ActiveWindow check, so a
// pinned clock + a file mod-time set via makeStale/makeFresh (which read the
// pinned clock) gives reproducible windowing assertions independent of when
// the test actually runs.
func pinNow(t *testing.T, fixed time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return fixed }
	t.Cleanup(func() { nowFunc = prev })
}

// timeAt returns a fixed local-time clock for hour:minute on a known day.
// Local time matches how the production code interprets ActiveWindow
// (operators configure HH:MM in TZ-local time; see scheduler.go).
//
//nolint:unparam // minute kept in signature for future minute-precision tests
func timeAt(hour, minute int) time.Time {
	return time.Date(2026, 4, 1, hour, minute, 0, 0, time.Local)
}

// makeStale creates path with a mod time `age` before the currently pinned
// clock so the resulting age is deterministic regardless of wall-clock time.
// Must be called after pinNow.
//
//nolint:unparam // age kept explicit at call sites for readability
func makeStale(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	mod := nowFunc().Add(-age)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// makeFresh sets path's mod time to the currently pinned clock.
// Must be called after pinNow.
func makeFresh(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	mod := nowFunc()
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestActiveWindow_NoAlertOutsideWindow(t *testing.T) {
	// Window 22:00-06:00 (overnight) — at 14:00 we are firmly outside it.
	pinNow(t, timeAt(14, 0))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path, 60*time.Minute) // stale relative to a 10-minute max

	svc := newTestService(t,
		config.FileMonitorCheckConfig{
			Name: "Nightly news", Path: path, MaxAgeMinutes: 10,
			ActiveWindow: "22:00-06:00",
		},
	)

	svc.run() // grace
	svc.run() // real

	r := svc.Status().Checks[0]
	if !r.IsStale {
		t.Error("file should still report IsStale=true outside window (transparency)")
	}
	if r.InAlert {
		t.Error("InAlert must be false outside the active window")
	}
	if got := svc.AlertingCount(); got != 0 {
		t.Errorf("AlertingCount() = %d, want 0 outside window", got)
	}
	if got := svc.StaleCount(); got != 1 {
		t.Errorf("StaleCount() = %d, want 1 (raw stale is preserved)", got)
	}
}

func TestActiveWindow_AlertWhenWindowOpens(t *testing.T) {
	// Outside window: grace + real, no alert state should be touched.
	pinNow(t, timeAt(2, 0))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path, 60*time.Minute)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{
			Name: "Daytime news", Path: path, MaxAgeMinutes: 10,
			ActiveWindow: "08:00-20:00",
		},
	)

	svc.run()
	svc.run()
	if svc.Status().Checks[0].InAlert {
		t.Fatal("InAlert leaked outside window")
	}

	// Window opens; the file is still stale from before, so the next run
	// should now flip InAlert and emit an alert.
	pinNow(t, timeAt(9, 0))
	// Re-stamp the mod time so it is still 60 minutes old relative to the
	// new pinned clock (otherwise the file would now appear "in the future").
	makeStale(t, path, 60*time.Minute)
	svc.run()

	r := svc.Status().Checks[0]
	if !r.InAlert {
		t.Error("expected InAlert=true once the window opens with a stale file")
	}
	if got := svc.AlertingCount(); got != 1 {
		t.Errorf("AlertingCount() = %d, want 1 inside window with stale file", got)
	}
}

func TestActiveWindow_StatTimeoutOutsideWindowDoesNotAlert(t *testing.T) {
	pinNow(t, timeAt(2, 0))

	path := "/hang/nightly.mp3"
	hangingStat(t, path)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{
			Name: "Nightly news", Path: path, MaxAgeMinutes: 10,
			ActiveWindow: "08:00-20:00", StatTimeoutSec: 1,
		},
	)

	svc.run() // grace
	svc.run() // real

	r := svc.Status().Checks[0]
	if !r.IsStale {
		t.Error("stat timeout should still report IsStale=true outside window")
	}
	if r.InAlert {
		t.Error("InAlert must stay false outside the active window on stat timeout")
	}
	if r.ErrorKind != FileCheckErrorKindStatTimeout {
		t.Errorf("expected ErrorKind=%q, got %q", FileCheckErrorKindStatTimeout, r.ErrorKind)
	}
	if got := svc.AlertingCount(); got != 0 {
		t.Errorf("AlertingCount() = %d, want 0 outside window", got)
	}
	if got := svc.StaleCount(); got != 1 {
		t.Errorf("StaleCount() = %d, want 1 for timed-out stat outside window", got)
	}
}

func TestActiveWindow_RecoveryRespectsWindow(t *testing.T) {
	// Recovery mails must not fire outside the window. Otherwise an alert
	// suppressed at 03:00 would still trigger a "[OK] hersteld" mail at 03:30,
	// which would be the only thing the operator ever sees about that file.

	// 1) Inside window: become alerting.
	pinNow(t, timeAt(10, 0))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path, 60*time.Minute)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{
			Name: "Daytime news", Path: path, MaxAgeMinutes: 10,
			ActiveWindow: "08:00-20:00",
		},
	)
	notifier := &captureFileMonitorNotifier{}
	svc.notify = notifier

	svc.run() // grace
	svc.run() // real → alert

	if !svc.Status().Checks[0].InAlert {
		t.Fatal("setup precondition: file should be alerting inside window")
	}
	if len(notifier.alerts) != 1 {
		t.Fatalf("expected 1 alert dispatch after entering alert, got %d", len(notifier.alerts))
	}

	// 2) File becomes fresh, but it is now outside the window.
	pinNow(t, timeAt(22, 0))
	makeFresh(t, path)
	svc.run()

	r := svc.Status().Checks[0]
	if r.IsStale {
		t.Error("freshly-touched file should not be IsStale")
	}
	if r.InAlert {
		t.Error("InAlert must stay suppressed outside window even on recovery tick")
	}
	if len(notifier.recoveries) != 0 {
		t.Fatalf("expected no recovery dispatch outside window, got %d", len(notifier.recoveries))
	}

	// 3) Re-enter the window while the file is still fresh. This must emit the
	// delayed recovery exactly once.
	pinNow(t, timeAt(11, 0))
	svc.run()

	r = svc.Status().Checks[0]
	if r.IsStale {
		t.Error("fresh file should remain non-stale when the window reopens")
	}
	if r.InAlert {
		t.Error("InAlert should clear once recovery is processed inside the window")
	}
	if len(notifier.recoveries) != 1 {
		t.Fatalf("expected 1 recovery dispatch when the window reopens, got %d", len(notifier.recoveries))
	}
	if got := notifier.recoveries[0][0].Path; got != path {
		t.Errorf("recovery dispatch path = %q, want %q", got, path)
	}
}

func TestAlertingCount_ExcludesOutsideWindow(t *testing.T) {
	// /health must stay healthy when the only stale file is outside its
	// window. ChecksStale stays raw (1), ChecksAlerting becomes 0.
	pinNow(t, timeAt(3, 0)) // outside 08:00-20:00

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path, 60*time.Minute)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{
			Name: "Daytime news", Path: path, MaxAgeMinutes: 10,
			ActiveWindow: "08:00-20:00",
		},
	)

	svc.run() // grace
	svc.run() // real

	if got := svc.StaleCount(); got != 1 {
		t.Errorf("StaleCount() = %d, want 1 (raw stale is preserved)", got)
	}
	if got := svc.AlertingCount(); got != 0 {
		t.Errorf("AlertingCount() = %d, want 0 (window suppresses)", got)
	}
}

func TestNewFileMonitorService_RejectsInvalidActiveWindow(t *testing.T) {
	// Tests that bypass config.Load() (i.e. construct Config{} directly, like
	// newTestService does) must still hit a hard failure on a bad window —
	// otherwise an invalid string would silently degrade to "always active".
	cfg := &config.Config{}
	cfg.FileMonitor = config.FileMonitorConfig{
		Enabled: true,
		Checks: []config.FileMonitorCheckConfig{
			{Name: "Bad", Path: "/data/x.mp3", MaxAgeMinutes: 10, ActiveWindow: "12:00-12:00"},
		},
	}
	notifySvc := notify.New(cfg)
	if _, err := newFileMonitorService(cfg, notifySvc); err == nil {
		t.Fatal("expected newFileMonitorService to reject equal-start/end window, got nil")
	}

	cfg.FileMonitor.Checks[0].ActiveWindow = "garbage"
	if _, err := newFileMonitorService(cfg, notifySvc); err == nil {
		t.Fatal("expected newFileMonitorService to reject unparsable window, got nil")
	}

	cfg.FileMonitor.Checks[0].ActiveWindow = "06:00-22:00"
	if _, err := newFileMonitorService(cfg, notifySvc); err != nil {
		t.Fatalf("valid window should be accepted, got: %v", err)
	}
}
