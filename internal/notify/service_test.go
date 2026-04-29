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
