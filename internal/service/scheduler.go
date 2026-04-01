package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	cron "github.com/netresearch/go-cron"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
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
		if err := s.addJob(cfg.Backup.Scheduler, "backup", s.runBackup); err != nil {
			return nil, err
		}
	}

	// Register health check job if enabled
	if cfg.Maintenance.Scheduler.Enabled {
		if err := s.addJob(cfg.Maintenance.Scheduler, "health-check", s.runHealthCheck); err != nil {
			return nil, err
		}
	}

	// Register file monitor job if enabled (interval auto-derived from checks)
	if cfg.FileMonitor.Enabled {
		interval := cfg.FileMonitor.CheckIntervalMinutes()
		schedule := fmt.Sprintf("@every %dm", interval)
		if err := s.addJob(config.SchedulerConfig{Enabled: true, Schedule: schedule}, "file-monitor", s.runFileMonitor); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// addJob registers a context-aware scheduled job with a name for observability.
// The context passed to the job function is derived from the cron's base context,
// enabling graceful shutdown propagation to running jobs.
func (s *Scheduler) addJob(cfg config.SchedulerConfig, name string, job func(context.Context)) error {
	if _, err := s.cron.AddJob(cfg.Schedule, cron.FuncJobWithContext(job), cron.WithName(name)); err != nil {
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

// runFileMonitor checks all monitored files for staleness.
func (s *Scheduler) runFileMonitor(_ context.Context) {
	slog.Info("Scheduled file monitor check started")
	s.service.FileMonitor.Run()
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
