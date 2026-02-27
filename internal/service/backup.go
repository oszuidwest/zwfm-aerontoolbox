package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// BackupService handles database backup operations.
type BackupService struct {
	repo       *database.Repository
	config     *config.Config
	backupRoot *os.Root
	s3         *s3Service // nil if S3 is disabled
	runner     *async.Runner
	notify     *notify.NotificationService

	pgDumpPath    string
	pgRestorePath string

	statusMu sync.RWMutex
	status   *BackupStatus
}

// BackupStatus represents the status of the last backup operation.
type BackupStatus struct {
	Running   bool          `json:"running"`
	StartedAt *time.Time    `json:"started_at,omitempty"`
	EndedAt   *time.Time    `json:"ended_at,omitempty"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Filename  string        `json:"filename,omitempty"`
	S3Sync    *S3SyncStatus `json:"s3_sync,omitempty"`
}

// S3SyncStatus represents the status of S3 synchronization.
type S3SyncStatus struct {
	Synced bool   `json:"synced"`
	Error  string `json:"error,omitempty"`
}

// newBackupService creates a BackupService with resolved tool paths and optional S3 client.
func newBackupService(repo *database.Repository, cfg *config.Config, notifySvc *notify.NotificationService) (*BackupService, error) {
	svc := &BackupService{
		repo:   repo,
		config: cfg,
		runner: async.New(),
		notify: notifySvc,
	}

	if cfg.Backup.Enabled {
		// Resolve required external tools at startup
		pgDumpPath, err := resolveToolPath(cfg.Backup.PgDumpPath, "pg_dump")
		if err != nil {
			return nil, err
		}
		svc.pgDumpPath = pgDumpPath

		pgRestorePath, err := resolveToolPath(cfg.Backup.PgRestorePath, "pg_restore")
		if err != nil {
			return nil, err
		}
		svc.pgRestorePath = pgRestorePath

		backupPath := cfg.Backup.GetPath()
		if err := os.MkdirAll(backupPath, 0o750); err != nil {
			return nil, types.NewConfigError("backup.path", fmt.Sprintf("backup directory not accessible: %v", err))
		}

		root, err := os.OpenRoot(backupPath)
		if err != nil {
			return nil, types.NewConfigError("backup.path", fmt.Sprintf("backup directory cannot be opened: %v", err))
		}
		svc.backupRoot = root

		// Initialize S3 backend if configured
		s3svc, err := newS3Service(&cfg.Backup.S3)
		if err != nil {
			return nil, err
		}
		svc.s3 = s3svc
	}

	return svc, nil
}

// Close stops the backup service and waits for any running backup to complete.
func (s *BackupService) Close() {
	s.runner.Close()
}

// Types.

// BackupRequest represents the request body for backup operations.
type BackupRequest struct {
	Compression int `json:"compression"`
}

// BackupInfo represents metadata about an existing backup file.
type BackupInfo struct {
	Filename      string    `json:"filename"`
	Size          int64     `json:"size_bytes"`
	SizeFormatted string    `json:"size"`
	CreatedAt     time.Time `json:"created_at"`
}

// BackupListResponse represents the response for listing backups.
type BackupListResponse struct {
	Backups    []BackupInfo `json:"backups"`
	TotalSize  int64        `json:"total_size_bytes"`
	TotalCount int          `json:"total_count"`
}

// Helpers.

var safeBackupFilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

// resolveToolPath returns the absolute path to an external tool, checking custom paths first.
func resolveToolPath(customPath, toolName string) (string, error) {
	if customPath != "" {
		if _, err := os.Stat(customPath); err != nil {
			return "", types.NewConfigError(toolName, fmt.Sprintf("%s not found at path: %s", toolName, customPath))
		}
		return customPath, nil
	}

	path, err := exec.LookPath(toolName)
	if err != nil {
		return "", types.NewConfigError(toolName, fmt.Sprintf("%s not found in PATH", toolName))
	}
	return path, nil
}

// checkEnabled returns an error if backup functionality is disabled.
func (s *BackupService) checkEnabled() error {
	if !s.config.Backup.Enabled || s.backupRoot == nil {
		return types.NewConfigError("backup.enabled", "backup functionality is not enabled")
	}
	return nil
}

// validateBackupFilename ensures the filename has valid characters, expected prefix and .dump extension.
func validateBackupFilename(filename string) error {
	if !safeBackupFilenamePattern.MatchString(filename) {
		return types.NewValidationError("filename", "invalid filename")
	}
	if !strings.HasPrefix(filename, "aeron-backup-") || !strings.HasSuffix(filename, ".dump") {
		return types.NewValidationError("filename", "not a valid backup file")
	}
	return nil
}

// buildPgDumpArgs constructs pg_dump command-line arguments for the given settings.
func (s *BackupService) buildPgDumpArgs(compression int) []string {
	return []string{
		"--format=custom",
		"--compress=" + strconv.Itoa(compression),
		"--host=" + s.config.Database.Host,
		"--port=" + s.config.Database.Port,
		"--username=" + s.config.Database.User,
		"--dbname=" + s.config.Database.Name,
		"--schema=" + s.config.Database.Schema,
		"--no-password",
	}
}

// compressionLevel returns a valid compression level (0-9), applying defaults and validation.
func (s *BackupService) compressionLevel(requested int) (int, error) {
	level := requested
	if level == 0 {
		level = s.config.Backup.GetDefaultCompression()
	}
	if level < 0 || level > 9 {
		return 0, types.NewValidationError("compression", fmt.Sprintf("invalid compression value: %d (use 0-9)", level))
	}
	return level, nil
}

// validateBackupFile checks backup file integrity using pg_restore --list.
func (s *BackupService) validateBackupFile(ctx context.Context, filePath string) error {
	cmd := exec.CommandContext(ctx, s.pgRestorePath, "--list", filePath) //nolint:gosec // pgRestorePath is from validated config
	output, err := cmd.CombinedOutput()
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		return types.NewOperationError("backup validation", fmt.Errorf("file is corrupt or unreadable: %s", errMsg))
	}
	return nil
}

// generateBackupFilename creates a timestamped filename with .dump extension.
func generateBackupFilename() string {
	timestamp := time.Now().Format("2006-01-02-150405")
	return fmt.Sprintf("aeron-backup-%s.dump", timestamp)
}

// executePgDump runs pg_dump and returns file info on success, cleaning up on failure.
func (s *BackupService) executePgDump(ctx context.Context, pgDumpPath, filename, fullPath string, args []string) (os.FileInfo, time.Duration, error) {
	cmd := exec.CommandContext(ctx, pgDumpPath, args...) //nolint:gosec // G204: pgDumpPath is resolved from config/PATH, not user HTTP input
	cmd.Env = append(os.Environ(), "PGPASSWORD="+s.config.Database.Password)

	start := time.Now()
	output, err := cmd.CombinedOutput()
	duration := time.Since(start)

	if err != nil {
		if removeErr := s.backupRoot.Remove(filename); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Warn("Failed to clean up failed backup", "filename", filename, "error", removeErr)
		}

		var errMsg string
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			errMsg = fmt.Sprintf("backup timeout after %s (configure backup.timeout_minutes)", duration.Round(time.Second))
		case ctx.Err() == context.Canceled:
			errMsg = "backup cancelled"
		case len(output) > 0:
			errMsg = strings.TrimSpace(string(output))
		default:
			errMsg = err.Error()
		}

		slog.Error("Backup failed", "error", err, "duration", duration, "output", string(output))
		return nil, 0, types.NewOperationError("create backup", errors.New(errMsg))
	}

	fileInfo, err := s.backupRoot.Stat(filename)
	if err != nil {
		return nil, 0, types.NewOperationError("create backup", fmt.Errorf("backup file not found after creation: %w", err))
	}

	if err := os.Chmod(fullPath, 0o600); err != nil {
		slog.Warn("Could not set file permissions", "file", filename, "error", err)
	}

	return fileInfo, duration, nil
}

// Public methods.

// Start initiates a database backup in the background. Returns an error if validation fails or a backup is already running.
func (s *BackupService) Start(req BackupRequest) error {
	if err := s.checkEnabled(); err != nil {
		return err
	}
	if _, err := s.compressionLevel(req.Compression); err != nil {
		return err
	}

	if !s.runner.TryStart() {
		return types.NewConflictError("backup", "backup already in progress")
	}

	// Initialize status before spawning goroutine to prevent race condition
	s.setStatusStarted()

	s.runner.Go(func() {
		ctx, cancel := s.runner.Context(s.config.Backup.GetTimeout())
		defer cancel()

		_ = s.execute(ctx, req) // Error tracked in status
	})

	return nil
}

// Run executes a database backup synchronously, blocking until completion.
func (s *BackupService) Run(ctx context.Context, req BackupRequest) error {
	if !s.runner.TryStart() {
		return types.NewConflictError("backup", "backup already in progress")
	}
	defer s.runner.Done()

	s.setStatusStarted()

	return s.execute(ctx, req)
}

// execute creates a database backup and synchronizes it to S3 if configured.
// Note: Caller must call setStatusStarted() before invoking this method.
func (s *BackupService) execute(ctx context.Context, req BackupRequest) error {
	if err := s.checkEnabled(); err != nil {
		s.setStatusDone(false, "", err.Error())
		return err
	}

	compression, err := s.compressionLevel(req.Compression)
	if err != nil {
		s.setStatusDone(false, "", err.Error())
		return err
	}

	filename := generateBackupFilename()
	fullPath := filepath.Join(s.config.Backup.GetPath(), filename)
	args := s.buildPgDumpArgs(compression)

	args = append(args, "--file="+fullPath)

	s.setStatusFilename(filename)
	slog.Info("Backup started", "filename", filename)

	fileInfo, duration, err := s.executePgDump(ctx, s.pgDumpPath, filename, fullPath, args)
	if err != nil {
		s.setStatusDone(false, filename, err.Error())
		s.notifyBackup()
		return err
	}

	// Validate backup file integrity
	slog.Info("Validating backup", "filename", filename)

	validateCtx, validateCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer validateCancel()

	if err := s.validateBackupFile(validateCtx, fullPath); err != nil {
		slog.Error("Backup validation failed", "filename", filename, "error", err)
		s.setStatusDone(false, filename, err.Error())
		s.notifyBackup()
		return err
	}

	slog.Info("Backup validated", "filename", filename)

	// Set S3 sync status before completing to prevent race condition in status reporting.
	if s.s3 != nil {
		s.setS3SyncStatus(false, "")
	}

	s.setStatusDone(true, filename, "")
	s.notifyBackup()
	slog.Info("Backup completed",
		"filename", filename,
		"size", util.FormatBytes(fileInfo.Size()),
		"duration", duration.Round(time.Millisecond).String())

	// Upload backup to S3 asynchronously
	if s.s3 != nil {
		s.runner.GoBackground(func() {
			uploadCtx, cancel := context.WithTimeout(context.Background(), s.config.Backup.GetTimeout())
			defer cancel()

			if err := s.s3.upload(uploadCtx, filename, fullPath); err != nil {
				slog.Error("S3 synchronization failed", "filename", filename, "error", err)
				s.setS3SyncStatus(false, err.Error())
				s.notify.NotifyS3SyncResult(filename, &notify.S3SyncResult{Synced: false, Error: err.Error()})
			} else {
				s.setS3SyncStatus(true, "")
				s.notify.NotifyS3SyncResult(filename, &notify.S3SyncResult{Synced: true})
			}
		})
	}

	s.cleanupOldBackups()
	return nil
}

// Status returns the current state and result of the last backup operation.
func (s *BackupService) Status() *BackupStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	if s.status == nil {
		return &BackupStatus{Running: s.runner.IsRunning()}
	}

	status := *s.status
	status.Running = s.runner.IsRunning()
	return &status
}

func (s *BackupService) setStatusStarted() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	now := time.Now()
	s.status = &BackupStatus{StartedAt: &now}
}

func (s *BackupService) setStatusFilename(filename string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.status != nil {
		s.status.Filename = filename
	}
}

func (s *BackupService) setStatusDone(success bool, filename, errMsg string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	now := time.Now()
	if s.status == nil {
		s.status = &BackupStatus{StartedAt: &now}
	}
	s.status.EndedAt = &now
	s.status.Success = success
	s.status.Error = errMsg
	if filename != "" {
		s.status.Filename = filename
	}
}

func (s *BackupService) setS3SyncStatus(synced bool, errMsg string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.status != nil {
		s.status.S3Sync = &S3SyncStatus{Synced: synced, Error: errMsg}
	}
}

// notifyBackup sends a backup notification based on the current status.
func (s *BackupService) notifyBackup() {
	st := s.Status()
	s.notify.NotifyBackupResult(&notify.BackupResult{
		Success:   st.Success,
		StartedAt: st.StartedAt,
		EndedAt:   st.EndedAt,
		Filename:  st.Filename,
		Error:     st.Error,
	})
}

// List returns metadata for all backup files in the backup directory.
func (s *BackupService) List() (*BackupListResponse, error) {
	if err := s.checkEnabled(); err != nil {
		return nil, err
	}

	backupPath := s.config.Backup.GetPath()
	entries, err := os.ReadDir(backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &BackupListResponse{
				Backups:    []BackupInfo{},
				TotalSize:  0,
				TotalCount: 0,
			}, nil
		}
		return nil, types.NewConfigError("backup.path", fmt.Sprintf("backup directory not readable: %v", err))
	}

	var backups []BackupInfo
	var totalSize int64

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, "aeron-backup-") || !strings.HasSuffix(name, ".dump") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Filename:      name,
			Size:          info.Size(),
			SizeFormatted: util.FormatBytes(info.Size()),
			CreatedAt:     info.ModTime(),
		})
		totalSize += info.Size()
	}

	slices.SortFunc(backups, func(a, b BackupInfo) int {
		return b.CreatedAt.Compare(a.CreatedAt) // Descending order
	})

	return &BackupListResponse{
		Backups:    backups,
		TotalSize:  totalSize,
		TotalCount: len(backups),
	}, nil
}

// Delete removes a backup file from local storage and S3 if configured.
func (s *BackupService) Delete(filename string) error {
	if err := s.checkEnabled(); err != nil {
		return err
	}

	if err := validateBackupFilename(filename); err != nil {
		return err
	}

	if _, err := s.backupRoot.Stat(filename); os.IsNotExist(err) {
		return types.NewNotFoundError("backup", filename)
	}

	if err := s.backupRoot.Remove(filename); err != nil {
		return types.NewOperationError("delete backup", err)
	}

	slog.Info("Backup deleted", "filename", filename)

	// Delete from S3 asynchronously
	if s.s3 != nil {
		s.runner.GoBackground(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := s.s3.delete(ctx, filename); err != nil {
				slog.Warn("Failed to delete S3 backup", "filename", filename, "error", err)
			}
		})
	}

	return nil
}

// GetFilePath returns the absolute path to a backup file.
func (s *BackupService) GetFilePath(filename string) (string, error) {
	if err := s.checkEnabled(); err != nil {
		return "", err
	}

	if err := validateBackupFilename(filename); err != nil {
		return "", err
	}

	if _, err := s.backupRoot.Stat(filename); os.IsNotExist(err) {
		return "", types.NewNotFoundError("backup", filename)
	}

	return filepath.Join(s.config.Backup.GetPath(), filename), nil
}

// ValidationResult represents the result of on-demand backup validation.
type ValidationResult struct {
	Filename string `json:"filename"`
	Valid    bool   `json:"valid"`
	Error    string `json:"error,omitempty"`
}

// Validate checks backup file integrity using pg_restore --list.
func (s *BackupService) Validate(filename string) (*ValidationResult, error) {
	fullPath, err := s.GetFilePath(filename)
	if err != nil {
		return nil, err
	}

	result := &ValidationResult{
		Filename: filename,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.validateBackupFile(ctx, fullPath); err != nil {
		result.Valid = false
		result.Error = err.Error()
	} else {
		result.Valid = true
	}

	return result, nil
}

// Background cleanup.

// cleanupOldBackups removes files exceeding retention days or max backup count.
func (s *BackupService) cleanupOldBackups() {
	backups, err := s.List()
	if err != nil {
		slog.Error("Could not retrieve backups for cleanup", "error", err)
		return
	}

	maxAge := time.Duration(s.config.Backup.GetRetentionDays()) * 24 * time.Hour
	maxBackups := s.config.Backup.GetMaxBackups()
	cutoff := time.Now().Add(-maxAge)

	var deleted int

	for _, backup := range backups.Backups {
		if backup.CreatedAt.Before(cutoff) {
			if err := s.Delete(backup.Filename); err != nil {
				slog.Warn("Failed to delete backup (retention)", "filename", backup.Filename, "error", err)
			} else {
				deleted++
				slog.Info("Old backup deleted (retention)", "filename", backup.Filename)
			}
		}
	}

	backups, err = s.List()
	if err != nil {
		slog.Error("Failed to retrieve backup list during cleanup", "error", err)
		return
	}
	if len(backups.Backups) > maxBackups {
		for i := maxBackups; i < len(backups.Backups); i++ {
			if err := s.Delete(backups.Backups[i].Filename); err != nil {
				slog.Warn("Failed to delete backup (max_backups)", "filename", backups.Backups[i].Filename, "error", err)
			} else {
				deleted++
				slog.Info("Old backup deleted (max_backups)", "filename", backups.Backups[i].Filename)
			}
		}
	}

	if deleted > 0 {
		slog.Info("Backup cleanup completed", "deleted", deleted)
	}
}
