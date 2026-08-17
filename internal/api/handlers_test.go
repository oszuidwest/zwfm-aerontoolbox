package api

// The full handleHealth flow (dbPing seam, response shape, detail redaction)
// is covered by the health tests in server_test.go; this file tests the
// extracted status precedence logic on its own.

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
