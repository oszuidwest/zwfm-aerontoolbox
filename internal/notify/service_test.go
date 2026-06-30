package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

// TestSendAsync_DroppedOnClose verifies that when the runner is closed,
// sendAsync records the drop via trackError so the health endpoint reflects it.
func TestSendAsync_DroppedOnClose(t *testing.T) {
	cfg := &config.Config{}
	svc := New(cfg)
	svc.runner.Close()

	svc.sendAsync("test subject", "test body")

	lastErr, lastErrAt := svc.LastError()
	if lastErr == "" {
		t.Error("LastError is empty, want non-empty (dropped notification must be tracked)")
	}
	if lastErrAt == nil {
		t.Error("LastErrorAt is nil, want non-nil")
	}
	const wantErr = "notification dropped: service is closed"
	if lastErr != wantErr {
		t.Errorf("LastError = %q, want %q", lastErr, wantErr)
	}
}

func TestFormatBackupFailureIncludesOperationalContext(t *testing.T) {
	started := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	ended := started.Add(2*time.Minute + 3*time.Second)

	subject, body := (&NotificationService{}).formatBackupFailure(&BackupResult{
		StartedAt: &started,
		EndedAt:   &ended,
		Filename:  "aeron-backup.dump",
		Error:     "pg_dump failed",
	})

	if subject != "[ERROR] Backup failed - Aeron Toolbox" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Backup failed",
		"Duration:       2m3s",
		"Filename:       aeron-backup.dump",
		"Error:          pg_dump failed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestFormatFileAlertsIncludesErrorStatus(t *testing.T) {
	checkedAt := time.Date(2026, 6, 29, 11, 0, 0, 0, time.UTC)

	subject, body := formatFileAlerts([]FileAlertResult{{
		Name:          "Nieuws",
		Path:          "/data/news.mp3",
		MaxAgeMinutes: 30,
		Error:         "stat timeout after 5s",
		CheckedAt:     checkedAt,
	}})

	if subject != "[ERROR] File monitor: Nieuws stale - Aeron Toolbox" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"File monitor failed",
		"Timestamp: 2026-06-29 11:00:00",
		"Path:             /data/news.mp3",
		"Status:           Error: stat timeout after 5s",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestFormatMediaCheckFailureUsesMetadataLabel(t *testing.T) {
	checkedAt := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	subject, body := formatMediaCheckFailure(&MediaCheckResult{
		CheckedAt: checkedAt,
		Scope:     "today",
		Total:     42,
		Problems: []MediaCheckProblem{{
			Artist:      "Artist",
			TrackTitle:  "Title",
			StartTime:   "12:34:56",
			Block:       "Middag",
			Status:      "missing",
			DBReference: `O:\Audio\missing.wav`,
		}},
	})

	if subject != "[ERROR] Media file check: Artist - Title - Aeron Toolbox" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Media file check found problems",
		"Scope:     today",
		"Checked:   42 items",
		"[MISSING] Artist - Title",
		"Reference: O:\\Audio\\missing.wav",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}
