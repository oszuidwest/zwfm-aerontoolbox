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

// Scheduler runs all enabled cron jobs on one shared cron instance.
type Scheduler struct {
	cron    *cron.Cron
	service *AeronService
	jobs    []string // names of registered jobs for logging
}

// NewScheduler registers enabled jobs using the local system timezone (TZ).
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

	if cfg.Backup.Enabled && cfg.Backup.Scheduler.Enabled {
		if err := s.addJob(cfg.Backup.Scheduler.Schedule, "backup", s.runBackup); err != nil {
			return nil, err
		}
	}

	if cfg.Maintenance.Scheduler.Enabled {
		if err := s.addJob(cfg.Maintenance.Scheduler.Schedule, "health-check", s.runHealthCheck); err != nil {
			return nil, err
		}
	}

	if cfg.FileMonitor.Enabled {
		schedule := fmt.Sprintf("@every %s", cfg.FileMonitor.Interval())
		if err := s.addJob(schedule, "file-monitor", s.runFileMonitor); err != nil {
			return nil, err
		}
	}

	if cfg.MediaFileCheck.Enabled && cfg.MediaFileCheck.Scheduler.Enabled {
		if err := s.addJob(cfg.MediaFileCheck.Scheduler.Schedule, "media-file-check", s.runMediaFileCheck); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// addJob registers a named job whose context follows the scheduler lifecycle.
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

// Start activates the cron runner when at least one job is registered.
func (s *Scheduler) Start() {
	if len(s.jobs) == 0 {
		return
	}
	s.cron.Start()
	slog.Info("Scheduler started", "jobs", s.jobs)
}

// Stop halts the cron runner and returns a context that closes after jobs drain.
func (s *Scheduler) Stop() context.Context {
	if len(s.jobs) == 0 {
		return context.Background()
	}
	slog.Info("Scheduler stopping...", "jobs", s.jobs)
	return s.cron.Stop()
}

// HasJobs reports whether the scheduler has registered work.
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
	s.runTriggered("file monitor check", s.service.FileMonitor.TriggerCheck)
}

// runMediaFileCheck verifies that today's playlist audio files exist on disk.
// It funnels through TriggerScheduled so a manual API trigger and a cron tick
// cannot overlap, and so the scheduled run (which emails alerts) keeps a stable
// today-scope for the failure/recovery transition.
func (s *Scheduler) runMediaFileCheck(_ context.Context) {
	s.runTriggered("media file check", s.service.MediaFileCheck.TriggerScheduled)
}

// runTriggered starts a single-flight check from a cron tick, logging a skip
// when a run is already in progress instead of treating it as a failure.
func (s *Scheduler) runTriggered(name string, trigger func() (uint64, error)) {
	if _, err := trigger(); err != nil {
		if _, ok := errors.AsType[*types.ConflictError](err); ok {
			slog.Info("Scheduled " + name + " skipped (already running)")
			return
		}
		slog.Error("Scheduled "+name+" failed to start", "error", err)
		return
	}
	slog.Info("Scheduled " + name + " started")
}

// healthCheckTimeout is the maximum time allowed for a scheduled health check.
const healthCheckTimeout = 2 * time.Minute

// runHealthCheck runs the scheduled database health check and alert pass.
func (s *Scheduler) runHealthCheck(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	slog.Info("Scheduled health check started")
	s.service.Maintenance.CheckHealthAndAlert(ctx)
}
