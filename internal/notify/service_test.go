package notify

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	graphmail "github.com/oszuidwest/zw-graphmail"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

type fakeMailer struct {
	sent []*graphmail.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m *graphmail.Message) error {
	f.sent = append(f.sent, m)
	return f.err
}

func TestNew_ConfiguredBuildsClientAndExpiryChecker(t *testing.T) {
	cfg := &config.Config{}
	cfg.Notifications.Email = config.GraphConfig{
		TenantID:     "12345678-1234-1234-1234-123456789abc",
		ClientID:     "12345678-1234-1234-1234-123456789abc",
		ClientSecret: "secret",
		FromAddress:  "noreply@example.com",
		Recipients:   "a@example.com",
	}
	svc := New(cfg)
	if !svc.IsConfigured() || svc.client == nil || svc.expiryChecker == nil {
		t.Fatalf("configured=%v client=%v expiryChecker=%v, want all set", svc.IsConfigured(), svc.client, svc.expiryChecker)
	}
}

func TestSend_PlainTextToAllRecipients(t *testing.T) {
	fake := &fakeMailer{err: errors.New("graph down")}
	svc := &NotificationService{client: fake, recipients: []string{"a@example.com", "b@example.com"}}

	svc.send("subject", "body")

	if len(fake.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(fake.sent))
	}
	m := fake.sent[0]
	if !slices.Equal(m.To, svc.recipients) || m.Subject != "subject" || m.Body != "body" || m.HTML {
		t.Errorf("message = %+v, want plain text to %v", m, svc.recipients)
	}
	if lastErr, _ := svc.LastError(); lastErr != "graph down" {
		t.Errorf("LastError = %q, want %q", lastErr, "graph down")
	}
}

func TestSecretExpiryChecker_Refresh(t *testing.T) {
	expiresAt := time.Now().Add(10*24*time.Hour + time.Hour).Truncate(time.Second)
	tests := []struct {
		name string
		at   time.Time
		err  error
		want SecretExpiryInfo
	}{
		{
			name: "expiry within warning window",
			at:   expiresAt,
			want: SecretExpiryInfo{ExpiresAt: expiresAt.Format(time.RFC3339), ExpiresSoon: true, DaysLeft: 10},
		},
		{
			name: "lookup error",
			err:  errors.New("secret not found"),
			want: SecretExpiryInfo{Error: "secret not found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewSecretExpiryChecker(func(context.Context) (time.Time, error) { return tt.at, tt.err })
			c.refresh()
			if c.cached != tt.want {
				t.Errorf("cached = %+v, want %+v", c.cached, tt.want)
			}
		})
	}
}

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

func TestFormatHealthAlertPutsRecommendationsFirst(t *testing.T) {
	_, body := formatHealthAlert(&HealthAlertResult{
		Recommendations:    []string{"VACUUM may be blocked"},
		LongRunningQueries: []types.LongRunningQuery{{PID: 42}},
	})
	recs, queries := strings.Index(body, "Recommendations:"), strings.Index(body, "Long-running queries:")
	if recs < 0 || queries < recs {
		t.Fatalf("body = %q, want recommendations before long-running queries", body)
	}
}
