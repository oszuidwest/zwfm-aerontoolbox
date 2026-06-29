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
		mediaProblems     int
		want              string
	}{
		{"all healthy", true, false, 0, 0, "healthy"},
		{"notify expiry soon", true, true, 0, 0, "degraded"},
		{"fm alerting", true, false, 1, 0, "degraded"},
		{"media problems", true, false, 0, 1, "degraded"},
		{"both degraded signals", true, true, 1, 0, "degraded"},
		{"db down", false, false, 0, 0, "unhealthy"},
		{"db down overrides notify expiry", false, true, 0, 0, "unhealthy"},
		{"db down overrides fm alerting", false, false, 1, 0, "unhealthy"},
		{"db down overrides media problems", false, false, 0, 1, "unhealthy"},
		{"db down overrides all", false, true, 1, 1, "unhealthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overallHealthStatus(tt.dbConnected, tt.notifyExpiresSoon, tt.fmAlerting, tt.mediaProblems)
			if got != tt.want {
				t.Errorf("overallHealthStatus(%v, %v, %d, %d) = %q, want %q",
					tt.dbConnected, tt.notifyExpiresSoon, tt.fmAlerting, tt.mediaProblems, got, tt.want)
			}
		})
	}
}
