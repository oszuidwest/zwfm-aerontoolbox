package api

// handleHealth itself is not unit-tested here because it requires a live
// database for Repository().Ping(); it is smoke-tested by CI.
// The status precedence logic is extracted into overallHealthStatus and tested below.

import "testing"

func TestOverallHealthStatus(t *testing.T) {
	tests := []struct {
		name        string
		dbConnected bool
		degraded    bool
		want        string
	}{
		{"all healthy", true, false, "healthy"},
		{"degraded signal", true, true, "degraded"},
		{"db down", false, false, "unhealthy"},
		{"db down overrides degraded", false, true, "unhealthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overallHealthStatus(tt.dbConnected, tt.degraded)
			if got != tt.want {
				t.Errorf("overallHealthStatus(%v, %v) = %q, want %q",
					tt.dbConnected, tt.degraded, got, tt.want)
			}
		})
	}
}
