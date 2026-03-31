package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileMonitorValidation_EnabledWithEmptyChecks(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{}

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for enabled file_monitor with empty checks, got nil")
	}
}

func TestFileMonitorValidation_EnabledWithNilChecks(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = nil

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for enabled file_monitor with nil checks, got nil")
	}
}

func TestFileMonitorValidation_EnabledWithValidChecks(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "/data/news.mp3", MaxAgeMinutes: 30},
	}

	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestFileMonitorValidation_DisabledWithEmptyChecks(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = false
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{}

	if err := validate(cfg); err != nil {
		t.Fatalf("disabled file_monitor should accept empty checks, got: %v", err)
	}
}

func TestFileMonitorValidation_ZeroMaxAge(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "/data/news.mp3", MaxAgeMinutes: 0},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for max_age_minutes=0, got nil")
	}
}

func TestFileMonitorValidation_MissingPath(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{MaxAgeMinutes: 30},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for missing path, got nil")
	}
}

func TestFileMonitorCheckIntervalMinutes(t *testing.T) {
	tests := []struct {
		name     string
		checks   []FileMonitorCheckConfig
		expected int
	}{
		{
			name:     "empty checks",
			checks:   nil,
			expected: 0,
		},
		{
			name: "single check",
			checks: []FileMonitorCheckConfig{
				{Path: "/a", MaxAgeMinutes: 30},
			},
			expected: 30,
		},
		{
			name: "multiple checks returns minimum",
			checks: []FileMonitorCheckConfig{
				{Path: "/a", MaxAgeMinutes: 60},
				{Path: "/b", MaxAgeMinutes: 10},
				{Path: "/c", MaxAgeMinutes: 30},
			},
			expected: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &FileMonitorConfig{Checks: tt.checks}
			if got := cfg.CheckIntervalMinutes(); got != tt.expected {
				t.Errorf("CheckIntervalMinutes() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestFileMonitorValidation_LoadFromJSON(t *testing.T) {
	cfgJSON := `{
		"database": {"host":"h","port":"5432","name":"db","user":"u","password":"p","schema":"s","sslmode":"disable"},
		"image": {"target_width":1,"target_height":1,"quality":85},
		"file_monitor": {
			"enabled": true,
			"checks": []
		}
	}`

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected Load to fail for enabled file_monitor with empty checks")
	}
}

// minimalConfig returns a Config that passes validation with all optional features disabled.
func minimalConfig() *Config {
	cfg := &Config{}

	// Satisfy required database fields.
	cfg.Database = DatabaseConfig{
		Host: "localhost", Port: "5432", Name: "db", User: "u",
		Password: "p", Schema: "testschema", SSLMode: "disable",
	}

	// Satisfy required image fields.
	cfg.Image = ImageConfig{TargetWidth: 1, TargetHeight: 1, Quality: 85}

	return cfg
}

// TestFileMonitorJSON_NullableFileExists verifies that *bool serializes correctly.
func TestFileMonitorJSON_NullableFileExists(t *testing.T) {
	type result struct {
		FileExists *bool `json:"file_exists"`
	}

	t.Run("true", func(t *testing.T) {
		v := true
		b, _ := json.Marshal(result{FileExists: &v})
		if string(b) != `{"file_exists":true}` {
			t.Errorf("got %s", b)
		}
	})

	t.Run("false", func(t *testing.T) {
		v := false
		b, _ := json.Marshal(result{FileExists: &v})
		if string(b) != `{"file_exists":false}` {
			t.Errorf("got %s", b)
		}
	})

	t.Run("null", func(t *testing.T) {
		b, _ := json.Marshal(result{FileExists: nil})
		if string(b) != `{"file_exists":null}` {
			t.Errorf("got %s", b)
		}
	})
}
