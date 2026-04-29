package notify

import (
	"testing"

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

func TestFormatEmailError_LocalizesOperationalErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "notification drop",
			in:   "notification dropped: service is closed",
			want: "notificatie niet verzonden: service is gesloten",
		},
		{
			name: "backup timeout",
			in:   "create backup failed: backup timeout after 30m0s (configure backup.timeout_minutes)",
			want: "backup maken mislukt: backup-timeout na 30m0s (configureer backup.timeout_minutes)",
		},
		{
			name: "database health",
			in:   "get database size failed: permission denied",
			want: "databasegrootte ophalen mislukt: geen rechten",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEmailError(tt.in)
			if got != tt.want {
				t.Errorf("formatEmailError(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatHealthRecommendation_LocalizesEmailText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "dead tuple percentage",
			in:   "Table 'playlistitem' has 15.2% dead tuples - VACUUM recommended",
			want: "Tabel 'playlistitem' heeft 15.2% dode tuples - VACUUM aanbevolen",
		},
		{
			name: "connection usage",
			in:   "Connection usage is high: 82/100 (82%) - check for connection leaks",
			want: "Connectiegebruik is hoog: 82/100 (82%) - controleer op connectielekken",
		},
		{
			name: "unknown recommendation",
			in:   "custom operator message",
			want: "custom operator message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatHealthRecommendation(tt.in)
			if got != tt.want {
				t.Errorf("formatHealthRecommendation(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
