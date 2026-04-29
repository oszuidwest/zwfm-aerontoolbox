package service

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// osStat is the file-system probe invoked by startOrJoinFlight. Tests
// override it to inject hangs or controlled errors without touching the real
// file system.
var osStat = os.Stat

// nowFunc returns the current time. Wrapped here so windowing tests can pin
// a deterministic clock without depending on time-of-day at run time.
// ActiveWindow comparisons use the location of the returned time, matching
// how operators configure local-time windows (TZ env, see scheduler.go).
var nowFunc = time.Now

// statCheckParallelism caps how many files are stat'd concurrently per Run().
// A radio config typically lists a handful of files, so 8 is plenty.
const statCheckParallelism = 8

// FileCheckErrorKind categorises why a file check failed.
type FileCheckErrorKind string

const (
	FileCheckErrorKindNone        FileCheckErrorKind = ""
	FileCheckErrorKindNotFound    FileCheckErrorKind = "not_found"
	FileCheckErrorKindPermission  FileCheckErrorKind = "permission_denied"
	FileCheckErrorKindStatError   FileCheckErrorKind = "stat_error"
	FileCheckErrorKindStatTimeout FileCheckErrorKind = "stat_timeout"
)

type fileMonitorNotifier interface {
	SendFileAlerts([]notify.FileAlertResult)
	SendFileRecoveries([]notify.FileAlertResult)
}

// statResult holds the outcome of a single os.Stat invocation.
type statResult struct {
	info os.FileInfo
	err  error
}

// statInFlight tracks a single in-progress stat for a path. The single-flight
// design ensures a frozen mount leaks at most one goroutine per unique path
// (until the OS returns), instead of one per Run() iteration. Joiners share the
// original timeout budget measured from when statFn actually started (not from
// when executeRun was called), so a permanently hanging flight only burns one
// full StatTimeout — subsequent runs return immediately.
//
// startedNano is an atomic int64 (Unix nanoseconds). It is initialised to the
// executeRun call time as a safe fallback for joiners that race the goroutine
// start, and overwritten with time.Now() just before statFn is called. Using
// an atomic avoids a mutex while still providing a happens-before guarantee
// between the goroutine write and the joiner read.
type statInFlight struct {
	startedNano atomic.Int64  // Unix ns; set at goroutine start, read by joiners
	done        chan struct{} // closed once result is populated
	result      statResult
}

// FileMonitorService monitors files on disk for staleness based on modification time.
type FileMonitorService struct {
	config *config.Config
	notify fileMonitorNotifier

	// Parsed ActiveWindow per check path. Populated once in
	// newFileMonitorService and never mutated afterwards, so reads from Run()
	// require no synchronisation. An entry missing or zero-valued means
	// "always active". Pre-parsing here keeps the hot path lock-free and lets
	// the constructor reject invalid windows up-front.
	windows map[string]config.TimeWindow

	// Per-file alert state: path → currently in alert.
	alertState   map[string]bool
	graceRunDone bool
	stateMu      sync.Mutex

	// In-flight stat tracking for single-flight per path.
	inflightMu sync.Mutex
	inflight   map[string]*statInFlight

	// Published state: lastCheck + run IDs live under one lock so a Status()
	// snapshot is internally consistent. Writing lastCheck without also bumping
	// completedRunID (or vice versa) would let a client observe checks from
	// run N+1 paired with completed_run_id=N — which would falsify the
	// documented exact-correlation contract.
	//
	// Status().Running comes from runner.IsRunning() (atomic) rather than a
	// mirrored field. The runner is the actual single-flight gate: clearing a
	// mirrored s.running inside fn() would flip Running=false before the
	// runner's deferred Store(false) opens the gate, so a client could see
	// "idle" but still get a 409 from the next TriggerCheck.
	runner         *async.Runner
	publishedMu    sync.RWMutex
	lastCheck      *FileMonitorStatus
	startedAt      *time.Time
	runID          uint64
	completedRunID uint64
}

// FileMonitorStatus contains the results of the most recent file monitor check
// plus the live run-state for the service.
//
// Polling recipe after POST /check: read run_id from the response (myRunID),
// then poll /status until completed_run_id >= myRunID && running == false.
// Strict equality (==) confirms the visible Checks were produced by your run;
// > means a later (e.g. cron) run overtook yours, which is fine for "is the
// system healthy" but loses exact correlation.
type FileMonitorStatus struct {
	Running         bool              `json:"running"`
	RunID           uint64            `json:"run_id"`
	CompletedRunID  uint64            `json:"completed_run_id"`
	StartedAt       *time.Time        `json:"started_at,omitempty"`
	LastCheckAt     *time.Time        `json:"last_check_at,omitempty"`
	IntervalSeconds int               `json:"interval_seconds"`
	Checks          []FileCheckResult `json:"checks"`
}

// FileCheckResult contains the result of checking a single file.
//
// Five valid state profiles exist:
//   - File exists:        FileExists=true,  FileAgeMinutes/LastModified set, Error empty
//   - File missing:       FileExists=false, FileAgeMinutes/LastModified nil,  Error empty, ErrorKind="not_found"
//   - Permission denied:  FileExists=nil,   FileAgeMinutes/LastModified nil,  Error set, ErrorKind="permission_denied"
//   - Stat timeout:       FileExists=nil,   FileAgeMinutes/LastModified nil,  Error set, ErrorKind="stat_timeout"
//   - Stat error:         FileExists=nil,   FileAgeMinutes/LastModified nil,  Error set, ErrorKind="stat_error"
//
// ErrorKind classifies the failure mode for downstream consumers:
// "" (success), "not_found", "permission_denied", "stat_timeout", "stat_error".
type FileCheckResult struct {
	Name           string             `json:"name,omitempty"`
	Path           string             `json:"path"`
	MaxAgeMinutes  int                `json:"max_age_minutes"`
	FileExists     *bool              `json:"file_exists"`
	FileAgeMinutes *float64           `json:"file_age_minutes,omitempty"`
	LastModified   *time.Time         `json:"last_modified,omitempty"`
	IsStale        bool               `json:"is_stale"`
	InAlert        bool               `json:"in_alert"`
	Error          string             `json:"error,omitempty"`
	ErrorKind      FileCheckErrorKind `json:"error_kind,omitempty"`
}

func newFileMonitorService(cfg *config.Config, notifySvc *notify.NotificationService) (*FileMonitorService, error) {
	windows := make(map[string]config.TimeWindow, len(cfg.FileMonitor.Checks))
	for _, c := range cfg.FileMonitor.Checks {
		w, err := config.ParseTimeWindow(c.ActiveWindow)
		if err != nil {
			return nil, fmt.Errorf("file monitor: check %q: %w", c.DisplayName(), err)
		}
		windows[c.Path] = w
	}
	return &FileMonitorService{
		config:     cfg,
		notify:     notifySvc,
		windows:    windows,
		alertState: make(map[string]bool),
		inflight:   make(map[string]*statInFlight),
		runner:     async.New(),
	}, nil
}

// executeRun performs the check cycle and returns the result without
// publishing it. Separated from Run() so the trigger path can publish the
// result together with run-state under a single lock.
func (s *FileMonitorService) executeRun() *FileMonitorStatus {
	now := nowFunc()
	checks := s.config.FileMonitor.Checks

	// Stat each file with a per-flight timeout in parallel. Single-flight
	// per path bounds goroutine leakage on hung mounts (see statInFlight).
	results := make([]FileCheckResult, len(checks))
	var g errgroup.Group
	g.SetLimit(statCheckParallelism)
	for i, check := range checks {
		g.Go(func() error {
			results[i] = s.checkFileWithTimeout(check, now)
			return nil
		})
	}
	// Always nil: goroutines embed errors in FileCheckResult and never return them.
	_ = g.Wait()

	newAlerts, newRecoveries := s.processAlertStates(checks, results, now)

	// Send batched notifications outside the lock.
	s.dispatchNotifications(newAlerts, newRecoveries)

	staleCount := 0
	for _, r := range results {
		if r.IsStale {
			staleCount++
		}
	}

	slog.Info("File monitor check completed", "total", len(results), "stale", staleCount)

	return &FileMonitorStatus{
		LastCheckAt: &now,
		Checks:      results,
	}
}

// processAlertStates evaluates check results against alert state under the
// state lock. It handles the grace run (first run after restart, observe-only),
// active-window suppression, and alert/recovery transitions. Returns new alerts
// and recoveries for notification dispatch (which must happen outside the lock).
func (s *FileMonitorService) processAlertStates(
	checks []config.FileMonitorCheckConfig,
	results []FileCheckResult,
	now time.Time,
) (newAlerts, newRecoveries []notify.FileAlertResult) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if !s.graceRunDone {
		// Grace run: observe only — don't update alert state or send notifications.
		// This avoids false alerts immediately after a restart.
		s.graceRunDone = true
		slog.Info("File monitor grace run completed, alerts will be sent from next check")
		return nil, nil
	}

	for i, check := range checks {
		// Outside an active window the result still reflects raw staleness
		// (transparent in /status) but does not flip alert state, send mail,
		// or trigger a recovery. Suppressing recovery is essential — without
		// it a midnight refresh would mail "[OK] hersteld" for an alert the
		// operator never received.
		if !s.windows[check.Path].Active(now) {
			results[i].InAlert = false
			continue
		}

		wasInAlert := s.alertState[check.Path]

		if results[i].IsStale {
			s.alertState[check.Path] = true
			results[i].InAlert = true

			if !wasInAlert {
				newAlerts = append(newAlerts, toAlertResult(check, &results[i], now))
			}
		} else {
			s.alertState[check.Path] = false

			if wasInAlert {
				newRecoveries = append(newRecoveries, toAlertResult(check, &results[i], now))
			}
		}
	}

	return newAlerts, newRecoveries
}

// dispatchNotifications sends batched alert and recovery notifications.
func (s *FileMonitorService) dispatchNotifications(
	newAlerts, newRecoveries []notify.FileAlertResult,
) {
	if len(newAlerts) > 0 {
		slog.Info("File monitor alert dispatch started",
			"count", len(newAlerts),
			"paths", alertPaths(newAlerts))
		s.notify.SendFileAlerts(newAlerts)
	}
	if len(newRecoveries) > 0 {
		slog.Info("File monitor recovery dispatch started",
			"count", len(newRecoveries),
			"paths", alertPaths(newRecoveries))
		s.notify.SendFileRecoveries(newRecoveries)
	}
}

// Status returns a fresh snapshot of run-state and the most recent Checks.
//
// Single-lock read: lastCheck and run IDs are written together under
// publishedMu by publishCompleted, so a snapshot observed here is internally
// consistent. Concretely, completed_run_id always names the run that
// produced the visible Checks — clients can rely on the documented
// `completed_run_id == myRunID` exact-correlation contract.
//
// Running is read from the runner's atomic gate, not a mirrored field, so
// `running == false` always implies the next TriggerCheck() will succeed.
//
// lastCheck.Checks is safe to share because executeRun() builds a fresh
// slice each call and never mutates a previously published one.
func (s *FileMonitorService) Status() *FileMonitorStatus {
	s.publishedMu.RLock()
	defer s.publishedMu.RUnlock()

	snap := &FileMonitorStatus{
		Running:         s.runner.IsRunning(),
		RunID:           s.runID,
		CompletedRunID:  s.completedRunID,
		IntervalSeconds: int(s.config.FileMonitor.Interval().Seconds()),
		Checks:          []FileCheckResult{},
	}
	if s.startedAt != nil {
		t := *s.startedAt
		snap.StartedAt = &t
	}
	if s.lastCheck != nil {
		snap.LastCheckAt = s.lastCheck.LastCheckAt
		snap.Checks = s.lastCheck.Checks
	}
	return snap
}

// StaleCount returns the raw number of files that were stale in the most
// recent check, regardless of any configured ActiveWindow. Use AlertingCount
// for the window-aware count that drives /health degraded.
func (s *FileMonitorService) StaleCount() int {
	s.publishedMu.RLock()
	defer s.publishedMu.RUnlock()
	return s.countChecksLocked(func(c FileCheckResult) bool { return c.IsStale })
}

// AlertingCount returns the number of checks currently in alert (InAlert==true)
// in the most recent run. Stale-but-outside-window checks are excluded by
// construction — Run() never flips InAlert for them — so this is the right
// signal for /health degraded. Reads under publishedMu so it pairs
// consistently with the Checks slice published by the same run.
func (s *FileMonitorService) AlertingCount() int {
	s.publishedMu.RLock()
	defer s.publishedMu.RUnlock()
	return s.countChecksLocked(func(c FileCheckResult) bool { return c.InAlert })
}

// countChecksLocked counts results satisfying pred. Caller must hold publishedMu for reading.
func (s *FileMonitorService) countChecksLocked(pred func(FileCheckResult) bool) int {
	if s.lastCheck == nil {
		return 0
	}
	n := 0
	for _, c := range s.lastCheck.Checks {
		if pred(c) {
			n++
		}
	}
	return n
}

// Close waits for any running TriggerCheck() invocation to finish.
// os.Stat goroutines started by startOrJoinFlight are not joined here: they
// cannot be cancelled (os.Stat has no context parameter), and blocking on them
// would cause process shutdown to hang permanently on a frozen mount. The
// goroutines hold a closure reference to s, so the GC will not collect the
// service while they are still running.
func (s *FileMonitorService) Close() {
	s.runner.Close()
}

// TriggerCheck starts a file monitor run in the background. It is the single
// entry point for both manual API triggers and scheduled cron ticks so the two
// can never run concurrently. Returns the monotone run_id of the run just
// started, or a ConflictError if a run is already in progress.
func (s *FileMonitorService) TriggerCheck() (uint64, error) {
	if !s.runner.TryStart() {
		return 0, types.NewConflictError("file_monitor", "file monitor check already running")
	}
	runID := s.startNewRun()
	s.runner.Go(func() {
		status := s.executeRun()
		s.publishCompleted(status, runID)
	})
	return runID, nil
}

// startNewRun increments the run counter and records the start time.
// Returns the new run ID. The "running" state itself is owned by the runner.
func (s *FileMonitorService) startNewRun() uint64 {
	s.publishedMu.Lock()
	defer s.publishedMu.Unlock()
	s.runID++
	now := nowFunc()
	s.startedAt = &now
	return s.runID
}

// publishCompleted atomically publishes the run's result and bumps
// completedRunID under a single lock. Pairing lastCheck with completedRunID
// in one critical section is what makes the polling contract
// `completed_run_id == myRunID` an exact-correlation guarantee — a Status()
// reader can never see lastCheck from run N+1 alongside completedRunID=N.
func (s *FileMonitorService) publishCompleted(status *FileMonitorStatus, runID uint64) {
	s.publishedMu.Lock()
	defer s.publishedMu.Unlock()
	s.lastCheck = status
	s.completedRunID = runID
}

// checkFileWithTimeout performs a single-flight os.Stat with a per-flight
// timeout budget. At most one goroutine per path is in flight at a time.
//
// Budget origin:
//   - New flight (isNew=true): the goroutine has been spawned but may not have
//     started executing yet, so startedNano is not reliable. The caller gets
//     the full configured timeout from this point forward.
//   - Joiner (isNew=false): shares the budget from startedNano. Once the
//     goroutine has started, startedNano holds the actual OS-call start time;
//     before that it holds the executeRun fallback. Either way the total wait
//     is bounded by one StatTimeout. If the budget is exhausted the joiner
//     returns immediately so a hung mount cannot stall the errgroup slot.
func (s *FileMonitorService) checkFileWithTimeout(check config.FileMonitorCheckConfig, now time.Time) FileCheckResult {
	flight, isNew := s.startOrJoinFlight(check.Path, now)
	timeout := check.StatTimeout()

	var remaining time.Duration
	if isNew {
		// Goroutine just spawned; startedNano reflects the executeRun clock,
		// not the actual OS-call start. Use the full configured budget.
		remaining = timeout
	} else {
		// Joining an in-flight stat: use the shared budget from the goroutine's
		// actual start time so the total wait across all joiners is bounded by
		// one StatTimeout, not one per caller.
		remaining = timeout - time.Since(time.Unix(0, flight.startedNano.Load()))
		if remaining <= 0 {
			return s.timeoutResult(check, flight, timeout)
		}
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case <-flight.done:
		return s.buildResult(check, flight.result.info, flight.result.err, now)
	case <-timer.C:
		return s.timeoutResult(check, flight, timeout)
	}
}

// startOrJoinFlight returns the in-flight stat for path, or starts a new one.
// A new flight kicks off a goroutine that runs osStat and (on completion)
// removes itself from the inflight map. The map entry is keyed by the
// statInFlight pointer so a still-hanging flight is not deleted when a newer
// flight for the same path has already replaced it.
func (s *FileMonitorService) startOrJoinFlight(path string, now time.Time) (*statInFlight, bool) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()

	if existing, ok := s.inflight[path]; ok {
		return existing, false
	}

	flight := &statInFlight{done: make(chan struct{})}
	flight.startedNano.Store(now.UnixNano()) // fallback for joiners that arrive before goroutine starts
	s.inflight[path] = flight
	// Snapshot the probe before spawning so tests can restore osStat in
	// t.Cleanup without racing a goroutine that has not started executing yet.
	statFn := osStat

	go func() {
		flight.startedNano.Store(time.Now().UnixNano()) // actual OS-call start time
		info, err := statFn(path)
		flight.result = statResult{info: info, err: err}
		close(flight.done) // happens-before vs. <-flight.done in readers

		s.inflightMu.Lock()
		if s.inflight[path] == flight {
			delete(s.inflight, path)
		}
		s.inflightMu.Unlock()
	}()

	return flight, true
}

// timeoutResult builds a synthetic stale-with-timeout result for a hung stat.
func (s *FileMonitorService) timeoutResult(
	check config.FileMonitorCheckConfig, flight *statInFlight, timeout time.Duration,
) FileCheckResult {
	slog.Warn("File monitor: stat timed out (single-flight goroutine still pending OS reply)",
		"name", check.DisplayName(),
		"path", check.Path,
		"timeout", timeout,
		"in_flight_since", time.Unix(0, flight.startedNano.Load()))
	return FileCheckResult{
		Name:          check.Name,
		Path:          check.Path,
		MaxAgeMinutes: check.MaxAgeMinutes,
		IsStale:       true,
		ErrorKind:     FileCheckErrorKindStatTimeout,
		Error:         fmt.Sprintf("stat timeout after %s", timeout),
	}
}

// buildResult turns an os.Stat outcome into a FileCheckResult.
func (s *FileMonitorService) buildResult(
	check config.FileMonitorCheckConfig, info os.FileInfo, err error, now time.Time,
) FileCheckResult {
	result := FileCheckResult{
		Name:          check.Name,
		Path:          check.Path,
		MaxAgeMinutes: check.MaxAgeMinutes,
	}

	if err != nil {
		result.IsStale = true
		label := check.DisplayName()

		switch {
		case errors.Is(err, os.ErrNotExist):
			exists := false
			result.FileExists = &exists
			result.ErrorKind = FileCheckErrorKindNotFound
			// Error intentionally left empty: FileExists=false already encodes the
			// absence, and the path itself identifies the missing file. The other
			// branches set Error because the file may exist but be unreadable.
			slog.Warn("File monitor: file not found", "name", label, "path", check.Path)
		case errors.Is(err, os.ErrPermission):
			result.Error = err.Error()
			result.ErrorKind = FileCheckErrorKindPermission
			slog.Warn("File monitor: permission denied", "name", label, "path", check.Path, "error", err)
		default:
			// FileExists stays nil — we cannot determine existence.
			result.Error = err.Error()
			result.ErrorKind = FileCheckErrorKindStatError
			slog.Warn("File monitor: file stat error", "name", label, "path", check.Path, "error", err)
		}
		return result
	}

	exists := true
	result.FileExists = &exists
	modTime := info.ModTime()
	result.LastModified = &modTime

	age := now.Sub(modTime)
	ageMinutes := age.Minutes()
	result.FileAgeMinutes = &ageMinutes

	maxAge := time.Duration(check.MaxAgeMinutes) * time.Minute
	result.IsStale = age > maxAge

	if result.IsStale {
		label := check.DisplayName()
		slog.Warn("File monitor: file is stale",
			"name", label,
			"path", check.Path,
			"age", fmt.Sprintf("%.0fm", math.Round(ageMinutes)),
			"max_age", fmt.Sprintf("%dm", check.MaxAgeMinutes))
	}

	return result
}

// toAlertResult converts a check result to a notification alert result.
func toAlertResult(
	check config.FileMonitorCheckConfig, result *FileCheckResult, checkedAt time.Time,
) notify.FileAlertResult {
	alert := notify.FileAlertResult{
		Name:          check.Name,
		Path:          check.Path,
		MaxAgeMinutes: check.MaxAgeMinutes,
		Exists:        result.FileExists != nil && *result.FileExists,
		Error:         result.Error,
		CheckedAt:     checkedAt,
	}
	if result.FileAgeMinutes != nil {
		alert.ActualAge = time.Duration(*result.FileAgeMinutes * float64(time.Minute))
	}
	return alert
}

func alertPaths(results []notify.FileAlertResult) []string {
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, result.Path)
	}
	return paths
}
