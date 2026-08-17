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

func TestFormatters(t *testing.T) {
	checkedAt := time.Date(2026, 6, 29, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		format      func() (subject, body string)
		wantSubject string
		wantBody    []string
	}{
		{
			name: "backup failure includes operational context",
			format: func() (string, string) {
				started := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
				ended := started.Add(2*time.Minute + 3*time.Second)
				return (&NotificationService{}).formatBackupFailure(&BackupResult{
					StartedAt: &started,
					EndedAt:   &ended,
					Filename:  "aeron-backup.dump",
					Error:     "pg_dump failed",
				})
			},
			wantSubject: "[ERROR] Backup failed - Aeron Toolbox",
			wantBody: []string{
				"Backup failed",
				"Duration:       2m3s",
				"Filename:       aeron-backup.dump",
				"Error:          pg_dump failed",
			},
		},
		{
			name: "single file alert includes error status",
			format: func() (string, string) {
				return formatFileAlerts([]FileAlertResult{{
					Name:          "Nieuws",
					Path:          "/data/news.mp3",
					MaxAgeMinutes: 30,
					Error:         "stat timeout after 5s",
					CheckedAt:     checkedAt,
				}})
			},
			wantSubject: "[ERROR] File monitor: Nieuws stale - Aeron Toolbox",
			wantBody: []string{
				"File monitor failed",
				"Timestamp: 2026-06-29 11:00:00",
				"Path:             /data/news.mp3",
				"Status:           Error: stat timeout after 5s",
			},
		},
		{
			name: "multiple file alerts use count subject",
			format: func() (string, string) {
				return formatFileAlerts([]FileAlertResult{
					{Name: "Nieuws", Path: "/data/news.mp3", MaxAgeMinutes: 30, CheckedAt: checkedAt},
					{Name: "Weer", Path: "/data/weather.mp3", MaxAgeMinutes: 60, CheckedAt: checkedAt},
				})
			},
			wantSubject: "[ERROR] File monitor: 2 files stale - Aeron Toolbox",
			wantBody: []string{
				"Count:     2",
				"Path:             /data/news.mp3",
				"Path:             /data/weather.mp3",
			},
		},
		{
			name: "file recovery reports current again",
			format: func() (string, string) {
				return formatFileRecoveries([]FileAlertResult{{
					Name:      "Nieuws",
					Path:      "/data/news.mp3",
					CheckedAt: checkedAt,
				}})
			},
			wantSubject: "[OK] File monitor: Nieuws recovered - Aeron Toolbox",
			wantBody: []string{
				"File monitor recovered",
				"Path:             /data/news.mp3",
				"Status:           Current again",
			},
		},
		{
			name: "media check failure uses metadata label",
			format: func() (string, string) {
				return formatMediaCheckFailure(&MediaCheckResult{
					CheckedAt: time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
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
			},
			wantSubject: "[ERROR] Media file check: Artist - Title - Aeron Toolbox",
			wantBody: []string{
				"Media file check found problems",
				"Scope:     today",
				"Checked:   42 items",
				"[MISSING] Artist - Title",
				"Reference: O:\\Audio\\missing.wav",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body := tt.format()
			if subject != tt.wantSubject {
				t.Fatalf("subject = %q, want %q", subject, tt.wantSubject)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(body, want) {
					t.Fatalf("body missing %q:\n%s", want, body)
				}
			}
		})
	}
}
