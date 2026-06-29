package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// BackupService creates, validates, lists, and deletes PostgreSQL backups.
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

// BackupStatus is the latest backup run snapshot.
type BackupStatus struct {
	Running   bool          `json:"running"`
	StartedAt *time.Time    `json:"started_at,omitempty"`
	EndedAt   *time.Time    `json:"ended_at,omitempty"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Filename  string        `json:"filename,omitempty"`
	S3Sync    *S3SyncStatus `json:"s3_sync,omitempty"`
}

// S3SyncStatus is the latest remote sync result for a backup.
type S3SyncStatus struct {
	Synced bool   `json:"synced"`
	Error  string `json:"error,omitempty"`
}

// newBackupService resolves backup tooling and optional S3 state.
func newBackupService(
	repo *database.Repository, cfg *config.Config, notifySvc *notify.NotificationService,
) (*BackupService, error) {
	svc := &BackupService{
		repo:   repo,
		config: cfg,
		runner: async.New(),
		notify: notifySvc,
	}

	if cfg.Backup.Enabled {
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

		s3svc, err := newS3Service(&cfg.Backup.S3)
		if err != nil {
			return nil, err
		}
		svc.s3 = s3svc
	}

	return svc, nil
}

// Close waits for any running backup or tracked follow-up work.
func (s *BackupService) Close() {
	s.runner.Close()
	if s.backupRoot != nil {
		if err := s.backupRoot.Close(); err != nil {
			slog.Warn("Failed to close backup root", "error", err)
		}
		s.backupRoot = nil
	}
}

// BackupRequest selects optional backup parameters.
type BackupRequest struct {
	Compression int `json:"compression"`
}

// BackupInfo describes one local backup file.
type BackupInfo struct {
	Filename      string    `json:"filename"`
	Size          int64     `json:"size_bytes"`
	SizeFormatted string    `json:"size"`
	CreatedAt     time.Time `json:"created_at"`
}

// BackupListResponse is the backup directory summary.
type BackupListResponse struct {
	Backups    []BackupInfo `json:"backups"`
	TotalSize  int64        `json:"total_size_bytes"`
	TotalCount int          `json:"total_count"`
}

const (
	backupPrefix = "aeron-backup-"
	backupSuffix = ".dump"
)

var safeBackupFilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

// resolveToolPath returns the configured tool path or resolves toolName from PATH.
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

// checkEnabled rejects backup operations when backup support is not initialized.
func (s *BackupService) checkEnabled() error {
	if !s.config.Backup.Enabled || s.backupRoot == nil {
		return types.NewConfigError("backup.enabled", "backup functionality is not enabled")
	}
	return nil
}

// validateBackupFilename accepts only managed .dump backup filenames.
func validateBackupFilename(filename string) error {
	if !safeBackupFilenamePattern.MatchString(filename) {
		return types.NewValidationError("filename", "invalid filename")
	}
	if !strings.HasPrefix(filename, backupPrefix) || !strings.HasSuffix(filename, backupSuffix) {
		return types.NewValidationError("filename", "not a valid backup file")
	}
	return nil
}

func (s *BackupService) validateBackupFileName(filename string) error {
	if err := s.checkEnabled(); err != nil {
		return err
	}
	if err := validateBackupFilename(filename); err != nil {
		return err
	}
	return nil
}

// ensureBackupFile checks the feature is enabled, validates the filename, and
// confirms the file is present. A missing file maps to NotFoundError; any other
// stat error (e.g. permission denied) maps to OperationError.
func (s *BackupService) ensureBackupFile(filename string) error {
	if err := s.validateBackupFileName(filename); err != nil {
		return err
	}
	if _, err := s.backupRoot.Stat(filename); err != nil {
		if os.IsNotExist(err) {
			return types.NewNotFoundError("backup", filename)
		}
		return types.NewOperationError("stat backup", err)
	}
	return nil
}

// buildPgDumpArgs constructs the pg_dump arguments for the configured database.
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

// compressionLevel applies the default and validates the 0-9 pg_dump range.
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

// OpenFile opens a managed backup file through the backup root.
func (s *BackupService) OpenFile(filename string) (*os.File, os.FileInfo, error) {
	if err := s.validateBackupFileName(filename); err != nil {
		return nil, nil, err
	}

	file, err := s.backupRoot.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, types.NewNotFoundError("backup", filename)
		}
		return nil, nil, types.NewOperationError("open backup", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, types.NewOperationError("stat backup", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, types.NewValidationError("filename", "not a regular backup file")
	}

	return file, info, nil
}

// validateBackupFile checks backup integrity with pg_restore --list.
func (s *BackupService) validateBackupFile(ctx context.Context, file *os.File) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return types.NewOperationError("backup validation", fmt.Errorf("seek file: %w", err))
	}

	//nolint:gosec // G204: pgRestorePath is resolved from config/PATH at startup.
	cmd := exec.CommandContext(ctx, s.pgRestorePath, "--list")
	cmd.Stdin = file
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

func (s *BackupService) validateManagedBackupFile(ctx context.Context, filename string) (err error) {
	file, _, err := s.OpenFile(filename)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = types.NewOperationError("backup validation", fmt.Errorf("close file: %w", closeErr))
		}
	}()

	return s.validateBackupFile(ctx, file)
}

// generateBackupFilename returns a managed timestamped .dump filename.
func generateBackupFilename() string {
	timestamp := time.Now().Format("2006-01-02-150405")
	return fmt.Sprintf("%s%s%s", backupPrefix, timestamp, backupSuffix)
}

// executePgDump runs pg_dump and removes the partial file on failure.
func (s *BackupService) executePgDump(
	ctx context.Context, pgDumpPath, filename, fullPath string, args []string,
) (os.FileInfo, time.Duration, error) {
	//nolint:gosec // G204: pgDumpPath is resolved from config/PATH, not user HTTP input
	cmd := exec.CommandContext(ctx, pgDumpPath, args...)
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

// Start initiates a database backup in the background. Returns an error if
// validation fails or a backup is already running.
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

	// Initialize status before spawning the goroutine to avoid a race.
	s.setStatusStarted()

	s.runner.Go(func() {
		ctx, cancel := s.runner.Context(s.config.Backup.GetTimeout())
		defer cancel()

		_ = s.execute(ctx, req) // Error tracked in status
	})

	return nil
}

// Run executes a database backup synchronously.
func (s *BackupService) Run(ctx context.Context, req BackupRequest) error {
	if !s.runner.TryStart() {
		return types.NewConflictError("backup", "backup already in progress")
	}
	defer s.runner.Done()

	s.setStatusStarted()

	return s.execute(ctx, req)
}

// execute creates a backup and starts optional S3 synchronization.
// The caller must publish the started status first.
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

	slog.Info("Validating backup", "filename", filename)

	validateCtx, validateCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer validateCancel()

	if err := s.validateManagedBackupFile(validateCtx, filename); err != nil {
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

	// Upload backup to S3 asynchronously. GoChild is safe here because
	// execute() always runs within an active TryStart()+Go() or TryStart()+Done()
	// body, so Close() is already blocking on the primary WaitGroup slot.
	if s.s3 != nil {
		s.runner.GoChild(func() {
			uploadCtx, cancel := context.WithTimeout(context.Background(), s.config.Backup.GetTimeout())
			defer cancel()

			if err := s.uploadBackupToS3(uploadCtx, filename); err != nil {
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

// Status returns a snapshot of the latest backup state.
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

func (s *BackupService) uploadBackupToS3(ctx context.Context, filename string) (err error) {
	file, _, err := s.OpenFile(filename)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = types.NewOperationError("S3 upload", fmt.Errorf("close file: %w", closeErr))
		}
	}()

	return s.s3.upload(ctx, filename, file)
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

// notifyBackup sends the current backup status to the notifier.
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

// List returns metadata for managed backup files in the backup directory.
func (s *BackupService) List() (*BackupListResponse, error) {
	if err := s.checkEnabled(); err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(s.backupRoot.FS(), ".")
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
		if !strings.HasPrefix(name, backupPrefix) || !strings.HasSuffix(name, backupSuffix) {
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

// Delete removes a managed backup locally and schedules remote deletion.
func (s *BackupService) Delete(filename string) error {
	if err := s.ensureBackupFile(filename); err != nil {
		return err
	}

	if err := s.backupRoot.Remove(filename); err != nil {
		return types.NewOperationError("delete backup", err)
	}

	slog.Info("Backup deleted", "filename", filename)

	// Delete from S3 asynchronously. TryGoBackground is used because Delete()
	// is called from an HTTP handler, not from within an active primary run.
	if s.s3 != nil {
		if !s.runner.TryGoBackground(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := s.s3.delete(ctx, filename); err != nil {
				slog.Warn("Failed to delete S3 backup", "filename", filename, "error", err)
			}
		}) {
			slog.Warn("S3 deletion skipped: backup service is closed", "filename", filename)
		}
	}

	return nil
}

// ValidationResult is the on-demand backup validation result.
type ValidationResult struct {
	Filename string `json:"filename"`
	Valid    bool   `json:"valid"`
	Error    string `json:"error,omitempty"`
}

// Validate checks a managed backup with pg_restore --list.
func (s *BackupService) Validate(filename string) (*ValidationResult, error) {
	result := &ValidationResult{
		Filename: filename,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.validateManagedBackupFile(ctx, filename); err != nil {
		result.Valid = false
		result.Error = err.Error()
	} else {
		result.Valid = true
	}

	return result, nil
}

// cleanupOldBackups removes backups exceeding age or count retention.
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
