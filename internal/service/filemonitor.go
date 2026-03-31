package service

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
)

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
}

// FileMonitorStatus contains the results of the most recent file monitor check.
type FileMonitorStatus struct {
	LastCheckAt     *time.Time        `json:"last_check_at,omitempty"`
	IntervalMinutes int               `json:"interval_minutes"`
	Checks          []FileCheckResult `json:"checks"`
}

// FileCheckResult contains the result of checking a single file.
//
// Three valid state profiles exist:
//   - File exists:  FileExists=true,  FileAgeMinutes/LastModified set, Error empty
//   - File missing: FileExists=false, FileAgeMinutes/LastModified nil,  Error empty
//   - Stat error:   FileExists=nil,   FileAgeMinutes/LastModified nil,  Error set
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
}

func newFileMonitorService(cfg *config.Config, notifySvc *notify.NotificationService) *FileMonitorService {
	return &FileMonitorService{
		config:     cfg,
		notify:     notifySvc,
		alertState: make(map[string]bool),
	}
}

// Run checks all configured files and sends notifications for newly stale or recovered files.
func (s *FileMonitorService) Run() {
	now := time.Now()
	checks := s.config.FileMonitor.Checks

	// Perform I/O (os.Stat) outside the lock to avoid blocking state reads
	// when files are on network mounts that may be slow or unresponsive.
	results := make([]FileCheckResult, len(checks))
	for i, check := range checks {
		results[i] = s.checkFile(check, now)
	}

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
	status := &FileMonitorStatus{
		LastCheckAt:     &now,
		IntervalMinutes: s.config.FileMonitor.CheckIntervalMinutes(),
		Checks:          results,
	}
	s.statusMu.Lock()
	s.lastCheck = status
	s.statusMu.Unlock()

	staleCount := 0
	for _, r := range results {
		if r.IsStale {
			staleCount++
		}
	}
	slog.Info("File monitor check completed", "total", len(results), "stale", staleCount)
}

// Status returns the most recent file monitor check results.
func (s *FileMonitorService) Status() *FileMonitorStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	if s.lastCheck == nil {
		return &FileMonitorStatus{
			IntervalMinutes: s.config.FileMonitor.CheckIntervalMinutes(),
		}
	}
	return s.lastCheck
}

// StaleCount returns the number of files that were stale in the most recent check.
func (s *FileMonitorService) StaleCount() int {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	if s.lastCheck == nil {
		return 0
	}
	count := 0
	for _, r := range s.lastCheck.Checks {
		if r.IsStale {
			count++
		}
	}
	return count
}

// Close satisfies the service lifecycle pattern.
func (s *FileMonitorService) Close() {}

// checkFile inspects a single file and returns its check result.
func (s *FileMonitorService) checkFile(check config.FileMonitorCheckConfig, now time.Time) FileCheckResult {
	result := FileCheckResult{
		Name:          check.Name,
		Path:          check.Path,
		MaxAgeMinutes: check.MaxAgeMinutes,
	}

	info, err := os.Stat(check.Path)
	if err != nil {
		result.IsStale = true
		label := displayName(check)

		if errors.Is(err, os.ErrNotExist) {
			exists := false
			result.FileExists = &exists
			slog.Warn("File monitor: file not found", "name", label, "path", check.Path)
		} else {
			// FileExists stays nil — we cannot determine existence.
			result.Error = err.Error()
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
		label := displayName(check)
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

// displayName returns the check name if set, otherwise the file path.
func displayName(check config.FileMonitorCheckConfig) string {
	if check.Name != "" {
		return check.Name
	}
	return check.Path
}
