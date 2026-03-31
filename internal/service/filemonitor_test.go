package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
)

// newTestService creates a FileMonitorService for testing.
// The notification service has no email configured so it never sends.
func newTestService(checks []config.FileMonitorCheckConfig) *FileMonitorService {
	cfg := &config.Config{}
	cfg.FileMonitor = config.FileMonitorConfig{
		Enabled: true,
		Checks:  checks,
	}
	notifySvc := notify.New(cfg)
	return newFileMonitorService(cfg, notifySvc)
}

func TestGraceRun_NoAlertsOnFirstRun(t *testing.T) {
	// Create a file that is stale (mod time in the past).
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// First run = grace run.
	svc.Run()

	status := svc.Status()
	if len(status.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(status.Checks))
	}

	// The file IS stale, so IsStale should be true.
	if !status.Checks[0].IsStale {
		t.Error("expected file to be reported as stale during grace run")
	}

	// But InAlert should be false because grace run doesn't set alert state.
	if status.Checks[0].InAlert {
		t.Error("expected InAlert=false during grace run (no alert state should be set)")
	}
}

func TestGraceRun_AlertOnSecondRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// First run = grace run (no alerts).
	svc.Run()

	// Second run should detect the stale file as a new alert.
	svc.Run()

	status := svc.Status()
	if !status.Checks[0].InAlert {
		t.Error("expected InAlert=true on second run for stale file")
	}
}

func TestGraceRun_NoPhantomRecovery(t *testing.T) {
	// File is stale during grace run, then becomes fresh before second run.
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// Grace run: file is stale.
	svc.Run()

	// Touch the file so it's fresh.
	touchFile(t, path)

	// Second run: file is now fresh. Since grace run didn't set alertState,
	// wasInAlert=false, so no recovery should be triggered.
	svc.Run()

	status := svc.Status()
	if status.Checks[0].IsStale {
		t.Error("file should be fresh after touch")
	}
	if status.Checks[0].InAlert {
		t.Error("expected InAlert=false — no phantom recovery should occur")
	}
}

func TestAlertAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	writeAndAge(t, path, 60*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 10},
	})

	// Grace run.
	svc.Run()

	// Second run: file is stale → enters alert.
	svc.Run()
	if !svc.Status().Checks[0].InAlert {
		t.Fatal("expected InAlert=true after second run")
	}

	// Third run: file still stale → stays in alert (no duplicate alert).
	svc.Run()
	if !svc.Status().Checks[0].InAlert {
		t.Error("expected InAlert=true to persist for still-stale file")
	}

	// Touch the file → should recover.
	touchFile(t, path)

	svc.Run()
	if svc.Status().Checks[0].InAlert {
		t.Error("expected InAlert=false after file recovery")
	}
	if svc.Status().Checks[0].IsStale {
		t.Error("expected IsStale=false after file recovery")
	}
}

func TestMissingFile_FileExistsFalse(t *testing.T) {
	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Ghost", Path: "/nonexistent/path/file.mp3", MaxAgeMinutes: 10},
	})

	// Grace run.
	svc.Run()
	// Real run.
	svc.Run()

	result := svc.Status().Checks[0]
	if result.FileExists == nil {
		t.Fatal("expected file_exists to be non-nil for ENOENT")
	}
	if *result.FileExists {
		t.Error("expected file_exists=false for missing file")
	}
	if !result.IsStale {
		t.Error("expected missing file to be stale")
	}
	if result.Error != "" {
		t.Errorf("expected no error string for ENOENT, got %q", result.Error)
	}
}

func TestStatError_FileExistsNull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test not reliable on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "restricted.mp3")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Remove all permissions from the parent directory so stat fails
	// with permission denied rather than ENOENT.
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: directories need execute bit for cleanup

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Restricted", Path: path, MaxAgeMinutes: 10},
	})

	// Grace run.
	svc.Run()
	// Real run.
	svc.Run()

	result := svc.Status().Checks[0]
	if result.FileExists != nil {
		t.Errorf("expected file_exists=null for permission error, got %v", *result.FileExists)
	}
	if result.Error == "" {
		t.Error("expected error field to contain the stat error")
	}
	if !result.IsStale {
		t.Error("expected stat error to be treated as stale")
	}
}

func TestFreshFile_NotStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.mp3")
	touchFile(t, path)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "News", Path: path, MaxAgeMinutes: 60},
	})

	svc.Run() // grace
	svc.Run() // real

	result := svc.Status().Checks[0]
	if result.IsStale {
		t.Error("fresh file should not be stale")
	}
	if result.FileExists == nil || !*result.FileExists {
		t.Error("expected file_exists=true for existing fresh file")
	}
	if result.InAlert {
		t.Error("expected InAlert=false for fresh file")
	}
}

func TestStaleCount(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.mp3")
	stale := filepath.Join(dir, "stale.mp3")
	touchFile(t, fresh)
	writeAndAge(t, stale, 120*time.Minute)

	svc := newTestService([]config.FileMonitorCheckConfig{
		{Name: "Fresh", Path: fresh, MaxAgeMinutes: 30},
		{Name: "Stale", Path: stale, MaxAgeMinutes: 10},
	})

	svc.Run() // grace
	svc.Run() // real

	if got := svc.StaleCount(); got != 1 {
		t.Errorf("StaleCount() = %d, want 1", got)
	}
}

// writeAndAge creates a file and sets its modification time to the past.
func writeAndAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-age)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

// touchFile creates or updates a file's modification time to now.
func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}
