package service

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// osStat is the file-system probe used by Run(). Tests override it to inject
// hangs or controlled errors without touching the real file system.
var osStat = os.Stat

// statCheckParallelism caps how many files are stat'd concurrently per Run().
// A radio config typically lists a handful of files, so 8 is plenty.
const statCheckParallelism = 8

// statResult holds the outcome of a single os.Stat invocation.
type statResult struct {
	info os.FileInfo
	err  error
}

// statInFlight tracks a single in-progress stat for a path. The single-flight
// design ensures a frozen mount leaks at most one goroutine per unique path
// (until the OS returns), instead of one per Run() iteration. Joiners share the
// original timeout budget measured from started, so a permanently hanging
// flight only burns one full StatTimeout — subsequent runs return immediately.
type statInFlight struct {
	started time.Time
	done    chan struct{} // closed once result is populated
	result  statResult
}

// FileMonitorService monitors files on disk for staleness based on modification time.
type FileMonitorService struct {
	config *config.Config
	notify *notify.NotificationService

	// Per-file alert state: path → currently in alert.
	alertState   map[string]bool
	graceRunDone bool
	stateMu      sync.Mutex

	// Last check results for the status API endpoint.
	lastCheck *FileMonitorStatus
	statusMu  sync.RWMutex

	// In-flight stat tracking for single-flight per path.
	inflightMu sync.Mutex
	inflight   map[string]*statInFlight

	// Run-state: tracks manual + scheduled triggers and exposes a monotone run ID
	// so clients can correlate POST /check responses with /status snapshots.
	// Distinct from inflight (per-path stat dedup) and statusMu (lastCheck).
	runner         *async.Runner
	runStateMu     sync.RWMutex
	running        bool
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
	staleCount      int
}

// FileCheckResult contains the result of checking a single file.
//
// Three valid state profiles exist:
//   - File exists:  FileExists=true,  FileAgeMinutes/LastModified set, Error empty
//   - File missing: FileExists=false, FileAgeMinutes/LastModified nil,  Error empty
//   - Stat error:   FileExists=nil,   FileAgeMinutes/LastModified nil,  Error set
//
// ErrorKind classifies the failure mode for downstream consumers:
// "" (success), "not_found", "stat_timeout", "stat_error".
type FileCheckResult struct {
	Name           string     `json:"name,omitempty"`
	Path           string     `json:"path"`
	MaxAgeMinutes  int        `json:"max_age_minutes"`
	FileExists     *bool      `json:"file_exists"`
	FileAgeMinutes *float64   `json:"file_age_minutes,omitempty"`
	LastModified   *time.Time `json:"last_modified,omitempty"`
	IsStale        bool       `json:"is_stale"`
	InAlert        bool       `json:"in_alert"`
	Error          string     `json:"error,omitempty"`
	ErrorKind      string     `json:"error_kind,omitempty"`
}

func newFileMonitorService(cfg *config.Config, notifySvc *notify.NotificationService) *FileMonitorService {
	return &FileMonitorService{
		config:     cfg,
		notify:     notifySvc,
		alertState: make(map[string]bool),
		inflight:   make(map[string]*statInFlight),
		runner:     async.New(),
	}
}

// Run checks all configured files and sends notifications for newly stale or recovered files.
func (s *FileMonitorService) Run() {
	now := time.Now()
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
	_ = g.Wait()

	var newAlerts []notify.FileAlertResult
	var newRecoveries []notify.FileAlertResult

	// Acquire lock only for alert state updates.
	s.stateMu.Lock()
	isGraceRun := !s.graceRunDone

	for i, check := range checks {
		if isGraceRun {
			// Grace run: observe only — don't update alert state or send notifications.
			// This avoids false alerts immediately after a restart.
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

	if isGraceRun {
		s.graceRunDone = true
		slog.Info("File monitor grace run completed, alerts will be sent from next check")
	}

	s.stateMu.Unlock()

	// Send batched notifications outside the lock.
	if len(newAlerts) > 0 {
		s.notify.SendFileAlerts(newAlerts)
	}
	if len(newRecoveries) > 0 {
		s.notify.SendFileRecoveries(newRecoveries)
	}

	// Update status for the API endpoint.
	staleCount := 0
	for _, r := range results {
		if r.IsStale {
			staleCount++
		}
	}
	status := &FileMonitorStatus{
		LastCheckAt:     &now,
		IntervalSeconds: int(s.config.FileMonitor.Interval().Seconds()),
		Checks:          results,
		staleCount:      staleCount,
	}
	s.statusMu.Lock()
	s.lastCheck = status
	s.statusMu.Unlock()

	slog.Info("File monitor check completed", "total", len(results), "stale", staleCount)
}

// Status returns a fresh snapshot combining lastCheck (Run() output) with the
// current run-state (TriggerCheck/markCompleted).
//
// Composition is critical: returning s.lastCheck directly would mean Running
// is never visible to clients. We read both state sources under their own
// locks and assemble a new struct. lastCheck.Checks is safe to share because
// Run() never mutates the slice after publishing it — a new run replaces the
// pointer entirely.
//
// Ordering guarantee: markCompleted runs via defer after Run() returns, and
// Run() writes s.lastCheck before returning. So once a client observes
// completedRunID == myRunID, the visible Checks are guaranteed to be that
// run's output.
func (s *FileMonitorService) Status() *FileMonitorStatus {
	s.runStateMu.RLock()
	running := s.running
	runID := s.runID
	completedRunID := s.completedRunID
	var startedAt *time.Time
	if s.startedAt != nil {
		t := *s.startedAt
		startedAt = &t
	}
	s.runStateMu.RUnlock()

	s.statusMu.RLock()
	last := s.lastCheck
	s.statusMu.RUnlock()

	snap := &FileMonitorStatus{
		Running:         running,
		RunID:           runID,
		CompletedRunID:  completedRunID,
		StartedAt:       startedAt,
		IntervalSeconds: int(s.config.FileMonitor.Interval().Seconds()),
		Checks:          []FileCheckResult{},
	}
	if last != nil {
		snap.LastCheckAt = last.LastCheckAt
		snap.Checks = last.Checks
		snap.staleCount = last.staleCount
	}
	return snap
}

// StaleCount returns the number of files that were stale in the most recent check.
func (s *FileMonitorService) StaleCount() int {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	if s.lastCheck == nil {
		return 0
	}
	return s.lastCheck.staleCount
}

// Close waits for any running TriggerCheck() invocation to finish.
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
		defer s.markCompleted(runID)
		s.Run()
	})
	return runID, nil
}

// startNewRun increments the run counter, marks the service running, and
// records the start time. Returns the new run ID.
func (s *FileMonitorService) startNewRun() uint64 {
	s.runStateMu.Lock()
	defer s.runStateMu.Unlock()
	s.runID++
	s.running = true
	now := time.Now()
	s.startedAt = &now
	return s.runID
}

// markCompleted clears the running flag and links the just-finished runID to
// the published Checks. Called via defer after Run() returns; Run() writes
// s.lastCheck before returning, so completedRunID is always consistent with
// lastCheck.
func (s *FileMonitorService) markCompleted(runID uint64) {
	s.runStateMu.Lock()
	defer s.runStateMu.Unlock()
	s.running = false
	s.completedRunID = runID
}

// checkFileWithTimeout performs a single-flight os.Stat with a per-flight
// timeout budget. At most one goroutine per path is in flight at a time;
// joiners share the original budget measured from flight.started rather than
// restarting the clock. A join on a flight that has already exceeded its
// budget returns immediately so a permanently hung mount cannot starve the
// errgroup slot in subsequent runs.
func (s *FileMonitorService) checkFileWithTimeout(check config.FileMonitorCheckConfig, now time.Time) FileCheckResult {
	flight, isNew := s.startOrJoinFlight(check.Path, now)
	timeout := check.StatTimeout()

	remaining := timeout - time.Since(flight.started)
	if !isNew && remaining <= 0 {
		// The flight has been hanging longer than its budget. Don't queue
		// behind it — return a synthetic timeout immediately.
		return s.timeoutResult(check, flight, timeout)
	}
	if remaining <= 0 {
		// Defensive: treat near-zero remainders on a brand-new flight as a
		// full budget rather than an instant timeout.
		remaining = timeout
	}

	select {
	case <-flight.done:
		return s.buildResult(check, flight.result.info, flight.result.err, now)
	case <-time.After(remaining):
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

	flight := &statInFlight{started: now, done: make(chan struct{})}
	s.inflight[path] = flight

	go func() {
		info, err := osStat(path)
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
func (s *FileMonitorService) timeoutResult(check config.FileMonitorCheckConfig, flight *statInFlight, timeout time.Duration) FileCheckResult {
	slog.Warn("File monitor: stat timed out (single-flight goroutine still pending OS reply)",
		"name", check.DisplayName(),
		"path", check.Path,
		"timeout", timeout,
		"in_flight_since", flight.started)
	return FileCheckResult{
		Name:          check.Name,
		Path:          check.Path,
		MaxAgeMinutes: check.MaxAgeMinutes,
		IsStale:       true,
		ErrorKind:     "stat_timeout",
		Error:         fmt.Sprintf("stat timeout after %s", timeout),
	}
}

// buildResult turns an os.Stat outcome into a FileCheckResult.
func (s *FileMonitorService) buildResult(check config.FileMonitorCheckConfig, info os.FileInfo, err error, now time.Time) FileCheckResult {
	result := FileCheckResult{
		Name:          check.Name,
		Path:          check.Path,
		MaxAgeMinutes: check.MaxAgeMinutes,
	}

	if err != nil {
		result.IsStale = true
		label := check.DisplayName()

		if errors.Is(err, os.ErrNotExist) {
			exists := false
			result.FileExists = &exists
			result.ErrorKind = "not_found"
			slog.Warn("File monitor: file not found", "name", label, "path", check.Path)
		} else {
			// FileExists stays nil — we cannot determine existence.
			result.Error = err.Error()
			result.ErrorKind = "stat_error"
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
func toAlertResult(check config.FileMonitorCheckConfig, result *FileCheckResult, checkedAt time.Time) notify.FileAlertResult {
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
