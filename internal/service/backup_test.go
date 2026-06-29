package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

type fakeBackupObjectStore struct {
	deleteFunc func(context.Context, string) error
}

func (f *fakeBackupObjectStore) upload(context.Context, string, string) error {
	return nil
}

func (f *fakeBackupObjectStore) delete(ctx context.Context, filename string) error {
	if f.deleteFunc == nil {
		return nil
	}
	return f.deleteFunc(ctx, filename)
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
	started := make(chan struct{})
	release := make(chan struct{})
	store := &fakeBackupObjectStore{
		deleteFunc: func(ctx context.Context, filename string) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
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

func TestCleanupOldBackupsTracksS3DeleteAsChild(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	store := &fakeBackupObjectStore{
		deleteFunc: func(ctx context.Context, filename string) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
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
