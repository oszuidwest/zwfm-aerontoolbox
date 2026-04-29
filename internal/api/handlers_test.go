package api

// Note: handleHealth itself is not unit-tested here because it requires a live
// database for the Repository().Ping() call; it is smoke-tested by CI.
// The status precedence logic is extracted into overallHealthStatus and tested below.

import "testing"

func TestOverallHealthStatus(t *testing.T) {
	tests := []struct {
		name              string
		dbConnected       bool
		notifyExpiresSoon bool
		fmAlerting        int
		want              string
	}{
		{"all healthy", true, false, 0, "healthy"},
		{"notify expiry soon", true, true, 0, "degraded"},
		{"fm alerting", true, false, 1, "degraded"},
		{"both degraded signals", true, true, 1, "degraded"},
		{"db down", false, false, 0, "unhealthy"},
		{"db down overrides notify expiry", false, true, 0, "unhealthy"},
		{"db down overrides fm alerting", false, false, 1, "unhealthy"},
		{"db down overrides both", false, true, 1, "unhealthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overallHealthStatus(tt.dbConnected, tt.notifyExpiresSoon, tt.fmAlerting)
			if got != tt.want {
				t.Errorf("overallHealthStatus(%v, %v, %d) = %q, want %q",
					tt.dbConnected, tt.notifyExpiresSoon, tt.fmAlerting, got, tt.want)
			}
		})
	}
}
