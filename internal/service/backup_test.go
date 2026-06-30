package service

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

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

	svc := newBackupTestService(t, backupPath)
	t.Cleanup(svc.Close)
	svc.pgRestorePath = pgRestorePath

	result, err := svc.Validate(testBackupFilename)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !result.Valid {
		t.Fatalf("validation result = invalid: %s", result.Error)
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
