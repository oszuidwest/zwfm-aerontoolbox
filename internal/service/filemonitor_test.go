package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// run drives the service synchronously for testing. It is the test-only
// alternative to TriggerCheck, which is asynchronous and involves the runner.
func (s *FileMonitorService) run() {
	status := s.executeRun()
	s.publishedMu.Lock()
	s.lastCheck = status
	s.publishedMu.Unlock()
}

// newTestService creates a FileMonitorService for testing. Variadic for
// brevity at call sites (all tests pass an inline literal). The notification
// service has no email configured so it never sends. Constructor errors fail
// the test - they would always indicate an invalid ActiveWindow in the test
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

// stubStat replaces osStat for the duration of the test.
func stubStat(t *testing.T, fn func(string) (os.FileInfo, error)) {
	t.Helper()
	prev := osStat
	osStat = fn
	t.Cleanup(func() { osStat = prev })
}

// shrinkStatTimeout overrides the per-check stat budget for the duration of
// the test so timeout tests complete in milliseconds instead of the
// second-granularity config minimum. Returns the injected timeout so
// assertions can express their bounds relative to it.
func shrinkStatTimeout(t *testing.T, d time.Duration) time.Duration {
	t.Helper()
	prev := statTimeoutFor
	statTimeoutFor = func(config.FileMonitorCheckConfig) time.Duration { return d }
	t.Cleanup(func() { statTimeoutFor = prev })
	return d
}

func TestGraceRun_NoAlertsOnFirstRun(t *testing.T) {
	// Create a file that is stale (mod time in the past).
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now().Add(-60*time.Minute))

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

func TestGraceRun_NoPhantomRecovery(t *testing.T) {
	// File is stale during grace run, then becomes fresh before second run.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now().Add(-60*time.Minute))

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10},
	)

	// Grace run: file is stale.
	svc.run()

	// Touch the file so it's fresh.
	writeFileAt(t, path, time.Now())

	// Second run: file is now fresh. Since grace run didn't set alertState,
	// wasInAlert=false, so no recovery should be triggered.
	svc.run()

	status := svc.Status()
	if status.Checks[0].IsStale {
		t.Error("file should be fresh after touch")
	}
	if status.Checks[0].InAlert {
		t.Error("expected InAlert=false - no phantom recovery should occur")
	}
}

func TestAlertAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now().Add(-60*time.Minute))

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
	writeFileAt(t, path, time.Now())

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
	pinNow(t, timeAt(10))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path)

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
	pinNow(t, timeAt(22))
	makeStale(t, path)
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
	pinNow(t, timeAt(10))
	makeStale(t, path)
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

// TestCheckResult_StateProfiles covers the valid FileCheckResult state
// profiles documented on the type: fresh file, missing file, permission
// denied, generic stat error, and stat timeout. Each case runs a grace run
// plus a real run against an always-active window, so InAlert must track
// IsStale exactly.
func TestCheckResult_StateProfiles(t *testing.T) {
	ptr := func(b bool) *bool { return &b }

	tests := []struct {
		name       string
		arrange    func(t *testing.T) string // returns the path to monitor
		wantExists *bool                     // nil means file_exists must be null
		wantStale  bool
		wantError  bool // whether Error must be non-empty
		wantKind   FileCheckErrorKind
	}{
		{
			name: "fresh file",
			arrange: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "news.mp3")
				writeFileAt(t, path, time.Now())
				return path
			},
			wantExists: ptr(true),
			wantKind:   FileCheckErrorKindNone,
		},
		{
			name: "missing file",
			arrange: func(t *testing.T) string {
				return "/nonexistent/path/file.mp3"
			},
			wantExists: ptr(false),
			wantStale:  true,
			// Error stays empty for ENOENT: FileExists=false already encodes
			// the absence, and the path identifies the missing file.
			wantKind: FileCheckErrorKindNotFound,
		},
		{
			name: "permission denied",
			arrange: func(t *testing.T) string {
				// Injected via the osStat seam rather than chmod 0o000 on the
				// parent directory, so the test also passes when running as
				// root (root ignores file modes).
				stubStat(t, func(path string) (os.FileInfo, error) {
					return nil, &fs.PathError{Op: "stat", Path: path, Err: syscall.EACCES}
				})
				return "/restricted/news.mp3"
			},
			wantStale: true,
			wantError: true,
			wantKind:  FileCheckErrorKindPermission,
		},
		{
			name: "generic stat error",
			arrange: func(t *testing.T) string {
				stubStat(t, func(string) (os.FileInfo, error) {
					return nil, errors.New("input/output error")
				})
				return "/broken/news.mp3"
			},
			wantStale: true,
			wantError: true,
			wantKind:  FileCheckErrorKindStatError,
		},
		{
			name: "stat timeout",
			arrange: func(t *testing.T) string {
				shrinkStatTimeout(t, 20*time.Millisecond)
				path := "/hang/news.mp3"
				hangingStat(t, path)
				return path
			},
			wantStale: true,
			wantError: true,
			wantKind:  FileCheckErrorKindStatTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.arrange(t)
			svc := newTestService(t,
				config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10},
			)

			svc.run() // grace
			svc.run() // real

			result := svc.Status().Checks[0]
			switch {
			case tt.wantExists == nil:
				if result.FileExists != nil {
					t.Errorf("file_exists = %v, want null", *result.FileExists)
				}
			case result.FileExists == nil:
				t.Errorf("file_exists = null, want %v", *tt.wantExists)
			default:
				if *result.FileExists != *tt.wantExists {
					t.Errorf("file_exists = %v, want %v", *result.FileExists, *tt.wantExists)
				}
			}
			if result.IsStale != tt.wantStale {
				t.Errorf("IsStale = %v, want %v", result.IsStale, tt.wantStale)
			}
			if result.InAlert != tt.wantStale {
				t.Errorf("InAlert = %v, want %v (always-active window tracks staleness)", result.InAlert, tt.wantStale)
			}
			if tt.wantError && result.Error == "" {
				t.Error("expected Error to be set")
			}
			if !tt.wantError && result.Error != "" {
				t.Errorf("expected empty Error, got %q", result.Error)
			}
			if result.ErrorKind != tt.wantKind {
				t.Errorf("ErrorKind = %q, want %q", result.ErrorKind, tt.wantKind)
			}
		})
	}
}

func TestStaleCount(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.mp3")
	stale := filepath.Join(dir, "stale.mp3")
	writeFileAt(t, fresh, time.Now())
	writeFileAt(t, stale, time.Now().Add(-120*time.Minute))

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

// hangingStat installs an osStat stub that hangs for the listed paths and
// returns os.ErrNotExist for any other path. It returns per-path call counters
// (so tests can assert single-flight behavior deterministically without
// touching runtime.NumGoroutine()) and an idempotent release func that
// unblocks the hung stats mid-test; callers ignore what they don't need.
//
// async.Runner.Close() does not track these stat goroutines, so without a
// release each "hang forever" stub would leak a goroutine for the rest of the
// test process - the release is therefore also wired to t.Cleanup.
func hangingStat(t *testing.T, paths ...string) (counters map[string]*atomic.Int64, release func()) {
	t.Helper()
	counters = make(map[string]*atomic.Int64, len(paths))
	hangSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		counters[p] = &atomic.Int64{}
		hangSet[p] = struct{}{}
	}
	var once sync.Once
	releaseCh := make(chan struct{})
	release = func() { once.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)

	stubStat(t, func(path string) (os.FileInfo, error) {
		if _, hangs := hangSet[path]; !hangs {
			return nil, os.ErrNotExist
		}
		counters[path].Add(1)
		<-releaseCh
		return nil, errors.New("released by test")
	})
	return counters, release
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
	shrinkStatTimeout(t, 20*time.Millisecond)
	path := "/hang/news.mp3"
	counters, _ := hangingStat(t, path)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	for range 10 {
		svc.run()
	}

	if got := counters[path].Load(); got != 1 {
		t.Errorf("osStat called %d times for %q, want exactly 1 (single-flight broken)", got, path)
	}
}

func TestSingleFlight_DifferentPathsAreIndependent(t *testing.T) {
	shrinkStatTimeout(t, 20*time.Millisecond)
	pathA := "/hang/a.mp3"
	pathB := "/hang/b.mp3"
	counters, _ := hangingStat(t, pathA, pathB)

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
	timeout := shrinkStatTimeout(t, 50*time.Millisecond)
	path := "/hang/news.mp3"
	hangingStat(t, path)

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 10, StatTimeoutSec: 1},
	)

	// First Run() establishes the flight and waits the full stat budget.
	svc.run()

	// Second Run() must observe remaining<=0 on the still-hanging flight and
	// return immediately rather than waiting another full budget.
	start := time.Now()
	svc.run()
	elapsed := time.Since(start)

	if elapsed >= timeout/2 {
		t.Errorf("second Run() took %v, expected near-instant return (well under the %v budget) on already-budgeted flight",
			elapsed, timeout)
	}
}

func TestParallelChecks_FastFailNotBlockedBySlowStat(t *testing.T) {
	timeout := shrinkStatTimeout(t, 50*time.Millisecond)
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

	// Total should be ~one stat budget (slow's timeout), not ~three budgets
	// (sequential).
	if elapsed >= 2*timeout {
		t.Errorf("Run() took %v, expected ~%v - fast checks appear serialized behind slow stat", elapsed, timeout)
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

// waitForCompleted polls Status() until the given runID is completed and the
// service is idle. Fails the test after a short deadline.
func waitForCompleted(t *testing.T, svc *FileMonitorService, runID uint64) {
	t.Helper()
	waitFor(t, 2*time.Second, fmt.Sprintf("timed out waiting for run %d to complete", runID), func() bool {
		st := svc.Status()
		return !st.Running && st.CompletedRunID >= runID
	})
}

func TestTriggerCheck_RunsAndUpdatesStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now())

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
	_, release := hangingStat(t, path)

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
	if _, ok := errors.AsType[*types.ConflictError](err); !ok {
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
	_, release := hangingStat(t, path)

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
	// First-ever active run: CompletedRunID must still be 0, lagging RunID
	// until publishCompleted runs.
	if st.CompletedRunID != 0 {
		t.Errorf("Status().CompletedRunID = %d, want 0 (first active run)", st.CompletedRunID)
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
	writeFileAt(t, path, time.Now())

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

func TestScheduler_RunFileMonitor_TriggersCheck(t *testing.T) {
	// Exercise the actual code path used by cron: Scheduler.runFileMonitor.
	// A regression that breaks runFileMonitor (e.g. dropping TriggerCheck)
	// should fail this test, not just contract tests on FileMonitorService.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now())

	fmSvc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
	defer fmSvc.Close()

	sch := &Scheduler{service: &AeronService{FileMonitor: fmSvc}}

	sch.runFileMonitor(t.Context())
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
	_, release := hangingStat(t, path)

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
	sch.runFileMonitor(t.Context())

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
	// fires in a defer after fn returns - so a brief window let the API
	// report idle while TryStart() still rejected.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now())

	svc := newTestService(t,
		config.FileMonitorCheckConfig{Name: "News", Path: path, MaxAgeMinutes: 60},
	)
	defer svc.Close()

	// Back-to-back triggers; if Status().Running could ever lead the runner
	// gate, one of these waitForCompleted → TriggerCheck pairs would hit a
	// 409. The original reproducer failed ~5/50 at -count=50.
	for i := range 25 {
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
	// two unlocks would observe (CompletedRunID=N-1, LastCheckAt=tag[N]) -
	// a mismatch this assertion would flag. Under the single-lock fix
	// (publishCompleted writes both atomically), no such window exists, so
	// the test passes deterministically.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeFileAt(t, path, time.Now())

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

	// Pre-run twice so the tag map has entries before the readers start -
	// otherwise early reads find no tag and skip the assertion.
	for range 2 {
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
	for range 8 {
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
					runtime.Gosched()
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
				runtime.Gosched()
			}
		})
	}

	// Drive many runs concurrently with the readers. Each run's tag is
	// recorded synchronously after waitForCompleted so the readers always
	// see a consistent (runID → published LastCheckAt) ground truth.
	for i := range 25 {
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

// timeAt returns a fixed local-time clock for the given hour on a known day.
// Local time matches how the production code interprets ActiveWindow
// (operators configure HH:MM in TZ-local time; see scheduler.go).
func timeAt(hour int) time.Time {
	return time.Date(2026, 4, 1, hour, 0, 0, 0, time.Local)
}

// makeStale creates path with a mod time 60 minutes before the currently
// pinned clock so the resulting age is deterministic regardless of wall-clock
// time (the tests pair it with MaxAgeMinutes well below 60). Must be called
// after pinNow.
func makeStale(t *testing.T, path string) {
	t.Helper()
	writeFileAt(t, path, nowFunc().Add(-60*time.Minute))
}

// makeFresh sets path's mod time to the currently pinned clock.
// Must be called after pinNow.
func makeFresh(t *testing.T, path string) {
	t.Helper()
	writeFileAt(t, path, nowFunc())
}

func TestActiveWindow_NoAlertOutsideWindow(t *testing.T) {
	// Window 22:00-06:00 (overnight) - at 14:00 we are firmly outside it.
	pinNow(t, timeAt(14))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path) // stale relative to a 10-minute max

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
	pinNow(t, timeAt(2))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path)

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
	pinNow(t, timeAt(9))
	// Re-stamp the mod time so it is still 60 minutes old relative to the
	// new pinned clock (otherwise the file would now appear "in the future").
	makeStale(t, path)
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
	shrinkStatTimeout(t, 20*time.Millisecond)
	pinNow(t, timeAt(2))

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
	// suppressed at 03:00 would still trigger a "[OK] recovered" mail at 03:30,
	// which would be the only thing the operator ever sees about that file.

	// 1) Inside window: become alerting.
	pinNow(t, timeAt(10))

	path := filepath.Join(t.TempDir(), "news.mp3")
	makeStale(t, path)

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
	pinNow(t, timeAt(22))
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
	pinNow(t, timeAt(11))
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

	// 4) File goes stale again after recovery - a fresh alert must fire exactly
	// once. This guards the invariant that alertState is cleared by the recovery
	// dispatch, so the next stale detection starts a new alert cycle.
	makeStale(t, path)
	svc.run()

	r = svc.Status().Checks[0]
	if !r.InAlert {
		t.Error("expected InAlert=true after file goes stale again post-recovery")
	}
	if len(notifier.alerts) != 2 {
		t.Fatalf("expected 2 alert dispatches after second stale cycle, got %d", len(notifier.alerts))
	}
}

func TestNewFileMonitorService_RejectsInvalidActiveWindow(t *testing.T) {
	// Tests that bypass config.Load() (i.e. construct Config{} directly, like
	// newTestService does) must still hit a hard failure on a bad window -
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
