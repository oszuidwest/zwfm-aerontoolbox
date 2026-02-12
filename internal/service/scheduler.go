package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	cron "github.com/netresearch/go-cron"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// Scheduler manages cron-based scheduled jobs for the application.
// It consolidates all scheduled tasks into a single cron instance.
type Scheduler struct {
	cron    *cron.Cron
	service *AeronService
	jobs    []string // names of registered jobs for logging
}

// NewScheduler creates a scheduler and registers all enabled scheduled jobs.
// The scheduler uses the system's local timezone (set via TZ environment variable).
func NewScheduler(svc *AeronService) (*Scheduler, error) {
	cfg := svc.Config()

	slog.Info("Scheduler using system timezone", "timezone", time.Local.String())

	c := cron.New(
		cron.WithLocation(time.Local),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)

	s := &Scheduler{cron: c, service: svc}

	// Register backup job if enabled
	if cfg.Backup.Enabled && cfg.Backup.Scheduler.Enabled {
		if err := s.addJob(cfg.Backup.Scheduler, "backup", s.runBackup); err != nil {
			return nil, err
		}
	}

	// Register maintenance job if enabled
	if cfg.Maintenance.Scheduler.Enabled {
		if err := s.addJob(cfg.Maintenance.Scheduler, "maintenance", s.runMaintenance); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// addJob registers a scheduled job using the scheduler's configured timezone.
func (s *Scheduler) addJob(cfg config.SchedulerConfig, name string, job func()) error {
	if _, err := s.cron.AddFunc(cfg.Schedule, job); err != nil {
		return err
	}

	s.jobs = append(s.jobs, name)
	slog.Info("Scheduled job registered", "job", name, "schedule", cfg.Schedule)
	return nil
}

// Start activates all scheduled jobs.
func (s *Scheduler) Start() {
	if len(s.jobs) == 0 {
		return
	}
	s.cron.Start()
	slog.Info("Scheduler started", "jobs", s.jobs)
}

// Stop halts the scheduler and waits for running jobs to finish.
func (s *Scheduler) Stop() context.Context {
	if len(s.jobs) == 0 {
		return context.Background()
	}
	slog.Info("Scheduler stopping...", "jobs", s.jobs)
	return s.cron.Stop()
}

// HasJobs returns true if any jobs are registered.
func (s *Scheduler) HasJobs() bool {
	return len(s.jobs) > 0
}

// runBackup performs a scheduled backup.
func (s *Scheduler) runBackup() {
	cfg := s.service.Config().Backup
	ctx, cancel := context.WithTimeout(context.Background(), cfg.GetTimeout())
	defer cancel()

	slog.Info("Scheduled backup started")
	if err := s.service.Backup.Run(ctx, BackupRequest{
		Compression: cfg.GetDefaultCompression(),
	}); err != nil {
		slog.Error("Scheduled backup failed", "error", err)
	}
}

// runMaintenance performs scheduled VACUUM ANALYZE on tables that need it.
func (s *Scheduler) runMaintenance() {
	slog.Info("Scheduled maintenance started")

	if err := s.service.Maintenance.StartVacuum(VacuumOptions{Analyze: true}); err != nil {
		var conflictErr *types.ConflictError
		if errors.As(err, &conflictErr) {
			slog.Info("Scheduled maintenance skipped (already running)")
		} else {
			slog.Error("Scheduled maintenance failed to start", "error", err)
		}
		return
	}

	slog.Info("Scheduled maintenance running in background")
}
