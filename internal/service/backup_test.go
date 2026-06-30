package service

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

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

func newTestBackupService(t *testing.T, store backupObjectStore) *BackupService {
	t.Helper()

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	return &BackupService{
		config: &config.Config{
			Backup: config.BackupConfig{
				Enabled:       true,
				Path:          dir,
				RetentionDays: 1,
				MaxBackups:    10,
			},
		},
		backupRoot: root,
		runner:     async.New(),
		s3:         store,
	}
}

func createBackupFile(t *testing.T, svc *BackupService, filename string, modTime time.Time) {
	t.Helper()

	path := filepath.Join(svc.config.Backup.GetPath(), filename)
	if err := os.WriteFile(path, []byte("backup"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func TestDeleteTracksS3DeleteAsBackgroundWork(t *testing.T) {
	store, started, release := newBlockingStore()
	svc := newTestBackupService(t, store)

	createBackupFile(t, svc, "aeron-backup-2026-06-29-120000.dump", time.Now())
	if err := svc.Delete("aeron-backup-2026-06-29-120000.dump"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	<-started
	closeDone := make(chan struct{})
	go func() {
		svc.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned before handler-initiated S3 delete completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after S3 delete completed")
	}
}

func TestNewBackupServiceLeavesObjectStoreNilWhenS3Disabled(t *testing.T) {
	dir := t.TempDir()
	pgDumpPath := filepath.Join(dir, "pg_dump")
	pgRestorePath := filepath.Join(dir, "pg_restore")
	for _, path := range []string{pgDumpPath, pgRestorePath} {
		if err := os.WriteFile(path, []byte("test tool"), 0o600); err != nil {
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

func TestCleanupOldBackupsTracksS3DeleteAsChild(t *testing.T) {
	store, started, release := newBlockingStore()
	svc := newTestBackupService(t, store)

	createBackupFile(t, svc, "aeron-backup-2026-06-28-120000.dump", time.Now().Add(-48*time.Hour))
	if !svc.runner.TryStart() {
		t.Fatal("TryStart returned false")
	}

	runDone := make(chan struct{})
	svc.runner.Go(func() {
		svc.cleanupOldBackups()
		close(runDone)
	})

	<-started
	<-runDone

	closeDone := make(chan struct{})
	go func() {
		svc.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned before retention S3 child delete completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after retention S3 child delete completed")
	}
}

func TestCleanupOldBackupsSchedulesS3DeleteAfterShutdownStarts(t *testing.T) {
	const filename = "aeron-backup-2026-06-28-120000.dump"

	deleted := make(chan string, 1)
	store := &fakeBackupObjectStore{
		deleteFunc: func(ctx context.Context, filename string) error {
			select {
			case deleted <- filename:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		},
	}
	svc := newTestBackupService(t, store)

	createBackupFile(t, svc, filename, time.Now().Add(-48*time.Hour))
	if !svc.runner.TryStart() {
		t.Fatal("TryStart returned false")
	}

	closeDone := make(chan struct{})
	go func() {
		svc.Close()
		close(closeDone)
	}()

	for svc.runner.TryGoBackground(func() {}) {
		runtime.Gosched()
	}

	runDone := make(chan struct{})
	svc.runner.Go(func() {
		svc.cleanupOldBackups()
		close(runDone)
	})

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("cleanupOldBackups did not return")
	}

	select {
	case got := <-deleted:
		if got != filename {
			t.Fatalf("deleted filename = %q, want %q", got, filename)
		}
	case <-time.After(time.Second):
		t.Fatal("retention S3 delete was not scheduled after shutdown started")
	}

	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after retention S3 delete completed")
	}
}

func TestCleanupMaxBackupsTracksS3DeleteAsChild(t *testing.T) {
	store, started, release := newBlockingStore()
	svc := newTestBackupService(t, store)
	svc.config.Backup.MaxBackups = 1

	const (
		newest = "aeron-backup-2026-06-29-120000.dump"
		oldest = "aeron-backup-2026-06-29-110000.dump"
	)
	createBackupFile(t, svc, newest, time.Now())
	createBackupFile(t, svc, oldest, time.Now().Add(-time.Hour))

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
	for svc.runner.TryGoBackground(func() {}) {
		runtime.Gosched()
	}

	runDone := make(chan struct{})
	svc.runner.Go(func() {
		svc.cleanupOldBackups()
		close(runDone)
	})

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("cleanupOldBackups did not return")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("max_backups S3 delete was not scheduled after shutdown started")
	}

	// max_backups removes the oldest excess backup and keeps the newest.
	if _, err := os.Stat(filepath.Join(svc.config.Backup.GetPath(), oldest)); !os.IsNotExist(err) {
		t.Fatalf("oldest backup should be deleted, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.config.Backup.GetPath(), newest)); err != nil {
		t.Fatalf("newest backup should remain: %v", err)
	}

	close(release)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after max_backups S3 delete completed")
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
	svc := newTestBackupService(t, store)

	const filename = "aeron-backup-2026-06-29-120000.dump"
	createBackupFile(t, svc, filename, time.Now())

	// Close the runner only: this reproduces the shutdown window inside
	// BackupService.Close where the runner is already closed but the backup
	// root is still open. (Full Close also releases the root, which is covered
	// by TestBackupServiceCloseClosesBackupRoot.)
	svc.runner.Close()

	// Local removal must still succeed while the root is open.
	if err := svc.Delete(filename); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.config.Backup.GetPath(), filename)); !os.IsNotExist(err) {
		t.Fatalf("local backup should be deleted, stat err = %v", err)
	}

	// With the runner closed, TryGoBackground drops the work: S3 delete must not run.
	select {
	case <-called:
		t.Fatal("S3 delete ran after Close; expected it to be dropped")
	case <-time.After(50 * time.Millisecond):
	}
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
		wantErr   string
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
			wantErr:   "use 0 for default, or 1-9",
		},
		{
			name:      "too high level",
			requested: 10,
			wantErr:   "use 0 for default, or 1-9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.compressionLevel(tt.requested)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("compressionLevel returned nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
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

const testBackupFilename = "aeron-backup-2026-01-02-030405.dump"

func newBackupTestService(t *testing.T, backupPath string) *BackupService {
	t.Helper()

	root, err := os.OpenRoot(backupPath)
	if err != nil {
		t.Fatalf("open backup root: %v", err)
	}

	return &BackupService{
		config: &config.Config{
			Backup: config.BackupConfig{
				Enabled: true,
				Path:    backupPath,
			},
		},
		backupRoot: root,
		runner:     async.New(),
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
	backupPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupPath, testBackupFilename), []byte("backup-data"), 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)

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

func TestBackupServiceOpenFileRejectsInvalidFilename(t *testing.T) {
	svc := newBackupTestService(t, t.TempDir())
	t.Cleanup(svc.Close)

	file, _, err := svc.OpenFile("../" + testBackupFilename)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("OpenFile accepted path traversal filename")
	}
	var validationErr *types.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("OpenFile error = %T %[1]v, want *types.ValidationError", err)
	}
}

func TestBackupServiceOpenFileRejectsNonBackupNames(t *testing.T) {
	backupPath := t.TempDir()
	for _, filename := range []string{
		"notaprefix.dump",
		"aeron-backup-2026-01-02-030405.txt",
	} {
		if err := os.WriteFile(filepath.Join(backupPath, filename), []byte("backup-data"), 0o600); err != nil {
			t.Fatalf("write backup file %q: %v", filename, err)
		}
	}

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)

	for _, filename := range []string{
		"notaprefix.dump",
		"aeron-backup-2026-01-02-030405.txt",
	} {
		t.Run(filename, func(t *testing.T) {
			file, _, err := svc.OpenFile(filename)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Fatal("OpenFile accepted non-backup filename")
			}
			var validationErr *types.ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("OpenFile error = %T %[1]v, want *types.ValidationError", err)
			}
		})
	}
}

func TestBackupServiceOpenFileRejectsDirectory(t *testing.T) {
	backupPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(backupPath, testBackupFilename), 0o700); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)

	file, _, err := svc.OpenFile(testBackupFilename)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("OpenFile accepted directory named like a backup")
	}
	var validationErr *types.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("OpenFile error = %T %[1]v, want *types.ValidationError", err)
	}
}

func TestBackupServiceOpenFileRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on many Windows systems")
	}

	backupPath := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "outside.dump")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(backupPath, testBackupFilename)); err != nil {
		t.Fatalf("create symlink escape: %v", err)
	}

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)

	file, _, err := svc.OpenFile(testBackupFilename)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("OpenFile followed symlink outside backup root")
	}
}

func TestBackupServiceValidateStreamsRootedFileToPgRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell helper")
	}

	backupPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupPath, testBackupFilename), []byte("backup-data"), 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)
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

	backupPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupPath, testBackupFilename), []byte("backup-data"), 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)
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
	svc := newBackupTestService(t, t.TempDir())
	t.Cleanup(svc.Close)

	result, err := svc.Validate(testBackupFilename)
	if err == nil {
		t.Fatal("Validate returned nil error for missing backup")
	}
	if result != nil {
		t.Fatalf("Validate result = %#v, want nil on missing backup", result)
	}
	var notFound *types.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Validate error = %T %[1]v, want *types.NotFoundError", err)
	}
}

func TestBackupServiceCloseClosesBackupRoot(t *testing.T) {
	backupPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupPath, testBackupFilename), []byte("backup-data"), 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	svc := newBackupTestService(t, backupPath)
	root := svc.backupRoot

	svc.Close()

	if _, err := root.Stat(testBackupFilename); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("root.Stat after Close error = %v, want %v", err, os.ErrClosed)
	}
}
