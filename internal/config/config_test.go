package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if got, want := cfg.GetUploadReadTimeout(), 180*time.Second; got != want {
		t.Errorf("GetUploadReadTimeout() = %s, want %s", got, want)
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
		RequestTimeoutSeconds:    11,
		UploadReadTimeoutSeconds: 12,
		ReadTimeoutSeconds:       13,
		WriteTimeoutSeconds:      14,
		IdleTimeoutSeconds:       15,
		MaxUploadBodyBytes:       16,
	}

	if got, want := cfg.GetRequestTimeout(), 11*time.Second; got != want {
		t.Errorf("GetRequestTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetUploadReadTimeout(), 12*time.Second; got != want {
		t.Errorf("GetUploadReadTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetReadTimeout(), 13*time.Second; got != want {
		t.Errorf("GetReadTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetWriteTimeout(), 14*time.Second; got != want {
		t.Errorf("GetWriteTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetIdleTimeout(), 15*time.Second; got != want {
		t.Errorf("GetIdleTimeout() = %s, want %s", got, want)
	}
	if got, want := cfg.GetMaxUploadBodyBytes(), int64(16); got != want {
		t.Errorf("GetMaxUploadBodyBytes() = %d, want %d", got, want)
	}
}

func TestAPIAuthenticationValidation(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		keys    []string
		wantErr bool
	}{
		{name: "disabled without keys", enabled: false},
		{name: "enabled with key", enabled: true, keys: []string{"test-api-key"}},
		{name: "enabled with nil keys", enabled: true, wantErr: true},
		{name: "enabled with empty keys", enabled: true, keys: []string{}, wantErr: true},
		{name: "enabled with empty key", enabled: true, keys: []string{""}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.API.Enabled = tt.enabled
			cfg.API.Keys = tt.keys

			err := validate(cfg)
			if tt.wantErr && err == nil {
				t.Fatal("validate() error = nil, want an authentication configuration error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validate() error = %v, want nil", err)
			}
		})
	}
}

func TestExampleConfigRequiresAPIKey(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode example config: %v", err)
	}
	if !cfg.API.Enabled {
		t.Fatal("example config must enable API authentication")
	}
	if len(cfg.API.Keys) != 0 {
		t.Fatalf("example API keys = %q, want empty list that requires operator configuration", cfg.API.Keys)
	}

	// Isolate API validation from the intentionally empty database password in
	// the example configuration.
	cfg.Database.Password = "test-database-password"
	if err := validate(&cfg); err == nil || !strings.Contains(err.Error(), "api.keys must have at least one entry when enabled") {
		t.Fatalf("validate(example) error = %v, want missing API key error", err)
	}

	cfg.API.Keys = []string{"test-random-api-key"}
	if err := validate(&cfg); err != nil {
		t.Fatalf("validate(configured example) error = %v, want nil", err)
	}
}

func TestAPIConfigValidationRejectsNegativeValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "negative request timeout",
			mutate: func(cfg *Config) {
				cfg.API.RequestTimeoutSeconds = -1
			},
		},
		{
			name: "negative upload read timeout",
			mutate: func(cfg *Config) {
				cfg.API.UploadReadTimeoutSeconds = -1
			},
		},
		{
			name: "negative read timeout",
			mutate: func(cfg *Config) {
				cfg.API.ReadTimeoutSeconds = -1
			},
		},
		{
			name: "negative write timeout",
			mutate: func(cfg *Config) {
				cfg.API.WriteTimeoutSeconds = -1
			},
		},
		{
			name: "negative idle timeout",
			mutate: func(cfg *Config) {
				cfg.API.IdleTimeoutSeconds = -1
			},
		},
		{
			name: "negative max upload body bytes",
			mutate: func(cfg *Config) {
				cfg.API.MaxUploadBodyBytes = -1
			},
		},
		{
			name: "negative rate limit requests",
			mutate: func(cfg *Config) {
				cfg.API.RateLimitRequests = -1
			},
		},
		{
			name: "negative rate limit window",
			mutate: func(cfg *Config) {
				cfg.API.RateLimitWindowSeconds = -1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalConfig()
			tt.mutate(cfg)

			if err := validate(cfg); err == nil {
				t.Fatal("expected validation error for negative API config value, got nil")
			}
		})
	}
}

func TestImageConfigMaxPixels(t *testing.T) {
	cfg := &ImageConfig{}
	if got, want := cfg.GetMaxPixels(), int64(DefaultMaxPixels); got != want {
		t.Errorf("GetMaxPixels() = %d, want %d", got, want)
	}

	cfg.MaxPixels = 123
	if got := cfg.GetMaxPixels(); got != 123 {
		t.Errorf("GetMaxPixels() = %d, want configured value 123", got)
	}
}

func TestAPIRateLimitDefaults(t *testing.T) {
	cfg := &APIConfig{}
	if got := cfg.GetRateLimitRequests(); got != DefaultRateLimitRequests {
		t.Fatalf("GetRateLimitRequests() = %d, want %d", got, DefaultRateLimitRequests)
	}
	if got := cfg.GetRateLimitWindow(); got != time.Duration(DefaultRateLimitWindowSeconds)*time.Second {
		t.Fatalf("GetRateLimitWindow() = %s, want %ds", got, DefaultRateLimitWindowSeconds)
	}

	cfg.RateLimitRequests = 10
	cfg.RateLimitWindowSeconds = 5
	if got := cfg.GetRateLimitRequests(); got != 10 {
		t.Fatalf("configured GetRateLimitRequests() = %d, want 10", got)
	}
	if got := cfg.GetRateLimitWindow(); got != 5*time.Second {
		t.Fatalf("configured GetRateLimitWindow() = %s, want 5s", got)
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
