package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	cron "github.com/netresearch/go-cron"

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
func NewScheduler(ctx context.Context, svc *AeronService) (*Scheduler, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg := svc.Config()

	slog.Info("Scheduler using system timezone", "timezone", time.Local.String())

	c := cron.New(
		cron.WithContext(ctx),
		cron.WithLocation(time.Local),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)

	s := &Scheduler{cron: c, service: svc}

	// Register backup job if enabled
	if cfg.Backup.Enabled && cfg.Backup.Scheduler.Enabled {
		if err := s.addJob(cfg.Backup.Scheduler.Schedule, "backup", s.runBackup); err != nil {
			return nil, err
		}
	}

	// Register health check job if enabled
	if cfg.Maintenance.Scheduler.Enabled {
		if err := s.addJob(cfg.Maintenance.Scheduler.Schedule, "health-check", s.runHealthCheck); err != nil {
			return nil, err
		}
	}

	// Register file monitor job if enabled (cadence from FileMonitor.Interval()).
	if cfg.FileMonitor.Enabled {
		schedule := fmt.Sprintf("@every %s", cfg.FileMonitor.Interval())
		if err := s.addJob(schedule, "file-monitor", s.runFileMonitor); err != nil {
			return nil, err
		}
	}

	// Register media file check job if enabled.
	if cfg.MediaFileCheck.Enabled && cfg.MediaFileCheck.Scheduler.Enabled {
		if err := s.addJob(cfg.MediaFileCheck.Scheduler.Schedule, "media-file-check", s.runMediaFileCheck); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// addJob registers a context-aware scheduled job with a name for observability.
// The context passed to the job function is derived from the cron's base context,
// enabling graceful shutdown propagation to running jobs.
func (s *Scheduler) addJob(schedule, name string, job func(context.Context)) error {
	if _, err := s.cron.AddJob(schedule, cron.FuncJobWithContext(job), cron.WithName(name)); err != nil {
		return err
	}

	s.jobs = append(s.jobs, name)
	slog.Info("Scheduled job registered", "job", name, "schedule", schedule)
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

// runBackup performs a scheduled backup. The context is derived from the cron's
// base context, so it will be canceled when the scheduler shuts down.
func (s *Scheduler) runBackup(ctx context.Context) {
	cfg := s.service.Config().Backup
	ctx, cancel := context.WithTimeout(ctx, cfg.GetTimeout())
	defer cancel()

	slog.Info("Scheduled backup started")
	if err := s.service.Backup.Run(ctx, BackupRequest{
		Compression: cfg.GetDefaultCompression(),
	}); err != nil {
		slog.Error("Scheduled backup failed", "error", err)
	}
}

// runFileMonitor checks all monitored files for staleness. It funnels the
// scheduled run through TriggerCheck so a manual API trigger and a cron tick
// cannot overlap (which would otherwise duplicate alert/recovery emails).
func (s *Scheduler) runFileMonitor(_ context.Context) {
	if _, err := s.service.FileMonitor.TriggerCheck(); err != nil {
		var conflict *types.ConflictError
		if errors.As(err, &conflict) {
			slog.Info("Scheduled file monitor skipped (already running)")
			return
		}
		slog.Error("Scheduled file monitor failed to start", "error", err)
		return
	}
	slog.Info("Scheduled file monitor check started")
}

// runMediaFileCheck verifies that today's playlist audio files exist on disk.
// It funnels through TriggerScheduled so a manual API trigger and a cron tick
// cannot overlap, and so the scheduled run (which emails alerts) keeps a stable
// today-scope for the failure/recovery transition.
func (s *Scheduler) runMediaFileCheck(_ context.Context) {
	if _, err := s.service.MediaFileCheck.TriggerScheduled(); err != nil {
		var conflict *types.ConflictError
		if errors.As(err, &conflict) {
			slog.Info("Scheduled media file check skipped (already running)")
			return
		}
		slog.Error("Scheduled media file check failed to start", "error", err)
		return
	}
	slog.Info("Scheduled media file check started")
}

// healthCheckTimeout is the maximum time allowed for a scheduled health check.
const healthCheckTimeout = 2 * time.Minute

// runHealthCheck performs a scheduled database health check and sends alerts if issues are detected.
func (s *Scheduler) runHealthCheck(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	slog.Info("Scheduled health check started")
	s.service.Maintenance.CheckHealthAndAlert(ctx)
}
