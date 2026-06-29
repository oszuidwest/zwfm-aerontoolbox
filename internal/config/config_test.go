package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestInterval_DefaultIsSixtySeconds(t *testing.T) {
	cfg := &FileMonitorConfig{}
	if got, want := cfg.Interval(), 60*time.Second; got != want {
		t.Errorf("Interval() = %s, want %s", got, want)
	}
}

func TestInterval_RespectsConfig(t *testing.T) {
	cfg := &FileMonitorConfig{IntervalSeconds: 90}
	if got, want := cfg.Interval(), 90*time.Second; got != want {
		t.Errorf("Interval() = %s, want %s", got, want)
	}
}

func TestInterval_ZeroFallsBackToDefault(t *testing.T) {
	for _, n := range []int{0, -5} {
		cfg := &FileMonitorConfig{IntervalSeconds: n}
		if got, want := cfg.Interval(), 60*time.Second; got != want {
			t.Errorf("Interval() with IntervalSeconds=%d = %s, want %s", n, got, want)
		}
	}
}

func TestAPIConfigDefaults(t *testing.T) {
	cfg := &APIConfig{}

	if got, want := cfg.GetRequestTimeout(), 30*time.Second; got != want {
		t.Errorf("GetRequestTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetReadTimeout(), 30*time.Second; got != want {
		t.Errorf("GetReadTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetWriteTimeout(), 60*time.Second; got != want {
		t.Errorf("GetWriteTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetIdleTimeout(), 120*time.Second; got != want {
		t.Errorf("GetIdleTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetMaxUploadBodyBytes(), int64(70*1024*1024); got != want {
		t.Errorf("GetMaxUploadBodyBytes() = %d, want %d", got, want)
	}
}

func TestAPIConfigRespectsConfiguredValues(t *testing.T) {
	cfg := &APIConfig{
		RequestTimeoutSeconds: 11,
		ReadTimeoutSeconds:    12,
		WriteTimeoutSeconds:   13,
		IdleTimeoutSeconds:    14,
		MaxUploadBodyBytes:    15,
	}

	if got, want := cfg.GetRequestTimeout(), 11*time.Second; got != want {
		t.Errorf("GetRequestTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetReadTimeout(), 12*time.Second; got != want {
		t.Errorf("GetReadTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetWriteTimeout(), 13*time.Second; got != want {
		t.Errorf("GetWriteTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetIdleTimeout(), 14*time.Second; got != want {
		t.Errorf("GetIdleTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetMaxUploadBodyBytes(), int64(15); got != want {
		t.Errorf("GetMaxUploadBodyBytes() = %d, want %d", got, want)
	}
}

func TestFileMonitorValidation_DuplicatePaths(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "/data/news.mp3", MaxAgeMinutes: 10},
		{Path: "/data/news.mp3", MaxAgeMinutes: 30},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for duplicate paths, got nil")
	}
}

func TestFileMonitorValidation_RelativePath(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "data/news.mp3", MaxAgeMinutes: 30},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for relative path, got nil")
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
