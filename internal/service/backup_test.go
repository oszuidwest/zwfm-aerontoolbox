package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

const testBackupFilename = "aeron-backup-2026-01-02-030405.dump"

type fakeBackupObjectStore struct {
	deleteFunc func(context.Context, string) error
}

func (f *fakeBackupObjectStore) upload(context.Context, string, io.Reader) error {
	return nil
}

func (f *fakeBackupObjectStore) delete(ctx context.Context, filename string) error {
	if f.deleteFunc == nil {
		return nil
	}
	return f.deleteFunc(ctx, filename)
}

// newBlockingStore returns a store whose delete closes started on entry and then
// blocks until release is closed (or the context is cancelled). It exercises the
// window where an S3 delete is in flight while Close is draining.
func newBlockingStore() (store *fakeBackupObjectStore, started, release chan struct{}) {
	started = make(chan struct{})
	release = make(chan struct{})
	store = &fakeBackupObjectStore{
		deleteFunc: func(ctx context.Context, _ string) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	return store, started, release
}

type backupTestSettings struct {
	store       backupObjectStore
	maxBackups  int
	rootCleanup bool
}

type backupTestOption func(*backupTestSettings)

// withStore installs an S3 object store on the service under test.
func withStore(store backupObjectStore) backupTestOption {
	return func(s *backupTestSettings) { s.store = store }
}

// withMaxBackups overrides the max_backups retention limit (default 10).
func withMaxBackups(n int) backupTestOption {
	return func(s *backupTestSettings) { s.maxBackups = n }
}

// withoutRootCleanup skips the automatic Close cleanup, for tests that close
// the service themselves and inspect the released backup root afterwards.
func withoutRootCleanup() backupTestOption {
	return func(s *backupTestSettings) { s.rootCleanup = false }
}

// newTestBackupService builds a BackupService over a fresh temp directory and,
// unless opted out, registers Close as a test cleanup.
func newTestBackupService(t *testing.T, opts ...backupTestOption) *BackupService {
	t.Helper()

	settings := backupTestSettings{maxBackups: 10, rootCleanup: true}
	for _, opt := range opts {
		opt(&settings)
	}

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}

	svc := &BackupService{
		config: &config.Config{
			Backup: config.BackupConfig{
				Enabled:       true,
				Path:          dir,
				RetentionDays: 1,
				MaxBackups:    settings.maxBackups,
			},
		},
		backupRoot: root,
		runner:     async.New(),
		s3:         settings.store,
	}
	if settings.rootCleanup {
		t.Cleanup(svc.Close)
	}
	return svc
}

// createBackupFile writes a backup file with the given content and modification
// time into the service's backup directory.
func createBackupFile(t *testing.T, svc *BackupService, filename, content string, modTime time.Time) {
	t.Helper()

	path := filepath.Join(svc.config.Backup.GetPath(), filename)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func TestBackupServiceStartRejectsConcurrentRun(t *testing.T) {
	svc := newTestBackupService(t)
	if !svc.runner.TryStart() {
		t.Fatal("TryStart returned false")
	}
	defer svc.runner.Done()

	err := svc.Start(BackupRequest{})
	if _, ok := errors.AsType[*types.ConflictError](err); !ok {
		t.Fatalf("Start error = %T %[1]v, want *types.ConflictError", err)
	}
}

func TestDeleteTracksS3DeleteAsBackgroundWork(t *testing.T) {
	store, started, release := newBlockingStore()
	svc := newTestBackupService(t, withStore(store))

	createBackupFile(t, svc, testBackupFilename, "backup", time.Now())
	if err := svc.Delete(testBackupFilename); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	mustReceive(t, started, "S3 delete start")

	closeDone := make(chan struct{})
	go func() {
		svc.Close()
		close(closeDone)
	}()

	mustNotReceive(t, closeDone, 50*time.Millisecond, "Close return before handler-initiated S3 delete completed")

	close(release)
	mustReceive(t, closeDone, "Close return after S3 delete completed")
}

func TestNewBackupServiceLeavesObjectStoreNilWhenS3Disabled(t *testing.T) {
	dir := t.TempDir()
	pgDumpPath := filepath.Join(dir, "pg_dump")
	pgRestorePath := filepath.Join(dir, "pg_restore")
	for _, path := range []string{pgDumpPath, pgRestorePath} {
		if err := os.WriteFile(path, []byte("test tool"), 0o700); err != nil { //nolint:gosec // G306: test tools must be executable.
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}

	cfg := &config.Config{
		Backup: config.BackupConfig{
			Enabled:       true,
			Path:          filepath.Join(dir, "backups"),
			RetentionDays: 30,
			MaxBackups:    5,
			PgDumpPath:    pgDumpPath,
			PgRestorePath: pgRestorePath,
		},
	}

	svc, err := newBackupService(nil, cfg, notify.New(cfg))
	if err != nil {
		t.Fatalf("newBackupService: %v", err)
	}
	defer svc.Close()

	if svc.s3 != nil {
		t.Fatal("s3 object store is non-nil when S3 is disabled")
	}
}

func TestResolveToolPathRejectsInvalidCustomPaths(t *testing.T) {
	nonExecutablePath := filepath.Join(t.TempDir(), "pg_dump")
	if err := os.WriteFile(nonExecutablePath, []byte("test tool"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", nonExecutablePath, err)
	}

	tests := []struct {
		name string
		path string
	}{
		{
			name: "directory",
			path: t.TempDir(),
		},
		{
			name: "non-executable file",
			path: nonExecutablePath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveToolPath(tt.path, "pg_dump")
			if err == nil {
				t.Fatal("resolveToolPath returned nil error")
			}

			if _, ok := errors.AsType[*types.ConfigError](err); !ok {
				t.Fatalf("resolveToolPath error = %T, want *types.ConfigError", err)
			}
		})
	}
}

// TestCleanupS3DeleteViaGoChildSurvivesShutdown covers the single production
// call site deleteDuringRun -> runner.GoChild(deleteS3Backup): a retention
// delete scheduled from within the primary run, after shutdown has already
// started, must still run and be waited on by Close.
func TestCleanupS3DeleteViaGoChildSurvivesShutdown(t *testing.T) {
	store, started, release := newBlockingStore()
	svc := newTestBackupService(t, withStore(store))

	createBackupFile(t, svc, "aeron-backup-2026-06-28-120000.dump", "backup", time.Now().Add(-48*time.Hour))
	if !svc.runner.TryStart() {
		t.Fatal("TryStart returned false")
	}

	closeDone := make(chan struct{})
	go func() {
		svc.Close()
		close(closeDone)
	}()

	// Wait until shutdown has started so we exercise the post-Close path:
	// TryGoBackground would now drop the delete, GoChild must still run it.
	waitFor(t, time.Second, "runner did not begin closing", svc.runner.Closing)

	runDone := make(chan struct{})
	svc.runner.Go(func() {
		svc.cleanupOldBackups()
		close(runDone)
	})

	mustReceive(t, runDone, "cleanupOldBackups return")
	mustReceive(t, started, "retention S3 delete scheduled after shutdown started")
	mustNotReceive(t, closeDone, 50*time.Millisecond, "Close return before retention S3 child delete completed")

	close(release)
	mustReceive(t, closeDone, "Close return after retention S3 child delete completed")
}

func TestCleanupMaxBackupsDeletesOldestKeepsNewest(t *testing.T) {
	svc := newTestBackupService(t, withMaxBackups(1))

	const (
		newest = "aeron-backup-2026-06-29-120000.dump"
		oldest = "aeron-backup-2026-06-29-110000.dump"
	)
	createBackupFile(t, svc, newest, "backup", time.Now())
	createBackupFile(t, svc, oldest, "backup", time.Now().Add(-time.Hour))

	svc.cleanupOldBackups()

	// max_backups removes the oldest excess backup and keeps the newest.
	if _, err := os.Stat(filepath.Join(svc.config.Backup.GetPath(), oldest)); !os.IsNotExist(err) {
		t.Fatalf("oldest backup should be deleted, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.config.Backup.GetPath(), newest)); err != nil {
		t.Fatalf("newest backup should remain: %v", err)
	}
}

func TestDeleteSkipsS3DeleteAfterClose(t *testing.T) {
	called := make(chan struct{}, 1)
	store := &fakeBackupObjectStore{
		deleteFunc: func(context.Context, string) error {
			called <- struct{}{}
			return nil
		},
	}
	svc := newTestBackupService(t, withStore(store))

	createBackupFile(t, svc, testBackupFilename, "backup", time.Now())

	// Close the runner only: this reproduces the shutdown window inside
	// BackupService.Close where the runner is already closed but the backup
	// root is still open. (Full Close also releases the root, which is covered
	// by TestBackupServiceCloseClosesBackupRoot.)
	svc.runner.Close()

	// Local removal must still succeed while the root is open.
	if err := svc.Delete(testBackupFilename); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.config.Backup.GetPath(), testBackupFilename)); !os.IsNotExist(err) {
		t.Fatalf("local backup should be deleted, stat err = %v", err)
	}

	// With the runner closed, TryGoBackground drops the work: S3 delete must not run.
	mustNotReceive(t, called, 50*time.Millisecond, "S3 delete after Close (expected it to be dropped)")
}

func TestCompressionLevel(t *testing.T) {
	svc := &BackupService{
		config: &config.Config{
			Backup: config.BackupConfig{DefaultCompression: 6},
		},
	}

	tests := []struct {
		name      string
		requested int
		want      int
		wantErr   bool
	}{
		{
			name:      "explicit zero uses default",
			requested: 0,
			want:      6,
		},
		{
			name:      "explicit level",
			requested: 5,
			want:      5,
		},
		{
			name:      "max valid level",
			requested: 9,
			want:      9,
		},
		{
			name:      "negative level",
			requested: -1,
			wantErr:   true,
		},
		{
			name:      "too high level",
			requested: 10,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.compressionLevel(tt.requested)
			if tt.wantErr {
				if err == nil {
					t.Fatal("compressionLevel returned nil error, want error")
				}
				validationErr, ok := errors.AsType[*types.ValidationError](err)
				if !ok {
					t.Fatalf("compressionLevel error = %T %[1]v, want *types.ValidationError", err)
				}
				if validationErr.Field != "compression" {
					t.Fatalf("ValidationError field = %q, want compression", validationErr.Field)
				}
				return
			}
			if err != nil {
				t.Fatalf("compressionLevel returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("compressionLevel = %d, want %d", got, tt.want)
			}
		})
	}
}

func writePgRestoreStub(t *testing.T) string {
	t.Helper()

	pgRestorePath := filepath.Join(t.TempDir(), "pg_restore")
	script := `#!/bin/sh
if [ "$#" -ne 1 ] || [ "$1" != "--list" ]; then
  echo "unexpected args: $*" >&2
  exit 3
fi
input=$(cat)
if [ "$input" != "backup-data" ]; then
  echo "unexpected stdin: $input" >&2
  exit 4
fi
`
	if err := os.WriteFile(pgRestorePath, []byte(script), 0o600); err != nil {
		t.Fatalf("write pg_restore helper: %v", err)
	}
	if err := os.Chmod(pgRestorePath, 0o700); err != nil { //nolint:gosec // test helper script must be executable.
		t.Fatalf("chmod pg_restore helper: %v", err)
	}
	return pgRestorePath
}

func TestBackupServiceOpenFileReadsManagedBackup(t *testing.T) {
	svc := newTestBackupService(t)
	createBackupFile(t, svc, testBackupFilename, "backup-data", time.Now())

	file, info, err := svc.OpenFile(testBackupFilename)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("close opened backup: %v", err)
		}
	}()

	if info.Size() != int64(len("backup-data")) {
		t.Fatalf("file size = %d, want %d", info.Size(), len("backup-data"))
	}

	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read opened backup: %v", err)
	}
	if string(got) != "backup-data" {
		t.Fatalf("opened backup data = %q, want backup-data", got)
	}
}

func TestBackupServiceOpenFileRejections(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(t *testing.T, svc *BackupService)
		filename string
		// anyError accepts any non-nil error instead of requiring a
		// ValidationError (os.Root reports symlink escapes with its own error).
		anyError      bool
		skipOnWindows string
	}{
		{
			name:     "path traversal filename",
			filename: "../" + testBackupFilename,
		},
		{
			name: "non-backup prefix",
			arrange: func(t *testing.T, svc *BackupService) {
				createBackupFile(t, svc, "notaprefix.dump", "backup-data", time.Now())
			},
			filename: "notaprefix.dump",
		},
		{
			name: "non-backup extension",
			arrange: func(t *testing.T, svc *BackupService) {
				createBackupFile(t, svc, "aeron-backup-2026-01-02-030405.txt", "backup-data", time.Now())
			},
			filename: "aeron-backup-2026-01-02-030405.txt",
		},
		{
			name: "directory named like a backup",
			arrange: func(t *testing.T, svc *BackupService) {
				if err := os.Mkdir(filepath.Join(svc.config.Backup.GetPath(), testBackupFilename), 0o700); err != nil {
					t.Fatalf("create backup directory: %v", err)
				}
			},
			filename: testBackupFilename,
		},
		{
			name: "symlink escape",
			arrange: func(t *testing.T, svc *BackupService) {
				outsidePath := filepath.Join(t.TempDir(), "outside.dump")
				if err := os.WriteFile(outsidePath, []byte("secret"), 0o600); err != nil {
					t.Fatalf("write outside file: %v", err)
				}
				if err := os.Symlink(outsidePath, filepath.Join(svc.config.Backup.GetPath(), testBackupFilename)); err != nil {
					t.Fatalf("create symlink escape: %v", err)
				}
			},
			filename:      testBackupFilename,
			anyError:      true,
			skipOnWindows: "symlink creation requires elevated privileges on many Windows systems",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipOnWindows != "" && runtime.GOOS == "windows" {
				t.Skip(tt.skipOnWindows)
			}

			svc := newTestBackupService(t)
			if tt.arrange != nil {
				tt.arrange(t, svc)
			}

			file, _, err := svc.OpenFile(tt.filename)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Fatal("OpenFile accepted an invalid backup target")
			}
			if tt.anyError {
				return
			}
			if _, ok := errors.AsType[*types.ValidationError](err); !ok {
				t.Fatalf("OpenFile error = %T %[1]v, want *types.ValidationError", err)
			}
		})
	}
}

func TestBackupServiceValidateStreamsRootedFileToPgRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell helper")
	}

	svc := newTestBackupService(t)
	createBackupFile(t, svc, testBackupFilename, "backup-data", time.Now())
	svc.pgRestorePath = writePgRestoreStub(t)

	result, err := svc.Validate(testBackupFilename)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !result.Valid {
		t.Fatalf("validation result = invalid: %s", result.Error)
	}
}

func TestBackupServiceValidateRewindsFileBeforePgRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell helper")
	}

	svc := newTestBackupService(t)
	createBackupFile(t, svc, testBackupFilename, "backup-data", time.Now())
	svc.pgRestorePath = writePgRestoreStub(t)

	file, _, err := svc.OpenFile(testBackupFilename)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("close opened backup: %v", err)
		}
	}()

	buf := make([]byte, len("backup-"))
	if _, err := io.ReadFull(file, buf); err != nil {
		t.Fatalf("read prefix from backup: %v", err)
	}
	if string(buf) != "backup-" {
		t.Fatalf("backup prefix = %q, want backup-", buf)
	}

	if err := svc.validateBackupFile(context.Background(), file); err != nil {
		t.Fatalf("validateBackupFile after partial read: %v", err)
	}
}

func TestBackupServiceValidateMissingFileReturnsError(t *testing.T) {
	svc := newTestBackupService(t)

	result, err := svc.Validate(testBackupFilename)
	if err == nil {
		t.Fatal("Validate returned nil error for missing backup")
	}
	if result != nil {
		t.Fatalf("Validate result = %#v, want nil on missing backup", result)
	}
	if _, ok := errors.AsType[*types.NotFoundError](err); !ok {
		t.Fatalf("Validate error = %T %[1]v, want *types.NotFoundError", err)
	}
}

func TestBackupServiceCloseClosesBackupRoot(t *testing.T) {
	svc := newTestBackupService(t, withoutRootCleanup())
	createBackupFile(t, svc, testBackupFilename, "backup-data", time.Now())
	root := svc.backupRoot

	svc.Close()

	if _, err := root.Stat(testBackupFilename); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("root.Stat after Close error = %v, want %v", err, os.ErrClosed)
	}
}
