package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileMonitorValidation(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		checks  []FileMonitorCheckConfig
		// wantErrContains asserts the field label from formatErrors so the
		// test fails if a neighboring rule fires instead of the one under
		// test. Empty means the config must validate cleanly.
		wantErrContains string
	}{
		{
			// nil and empty checks hit the identical len==0 branch.
			name:            "enabled with no checks",
			enabled:         true,
			checks:          nil,
			wantErrContains: "filemonitor.checks must have at least one entry when enabled",
		},
		{
			name:    "enabled with valid check",
			enabled: true,
			checks:  []FileMonitorCheckConfig{{Path: "/data/news.mp3", MaxAgeMinutes: 30}},
		},
		{
			name:    "disabled with empty checks",
			enabled: false,
			checks:  []FileMonitorCheckConfig{},
		},
		{
			name:            "zero max age",
			enabled:         true,
			checks:          []FileMonitorCheckConfig{{Path: "/data/news.mp3", MaxAgeMinutes: 0}},
			wantErrContains: "filemonitor.checks[0].maxageminutes is required",
		},
		{
			name:            "missing path",
			enabled:         true,
			checks:          []FileMonitorCheckConfig{{MaxAgeMinutes: 30}},
			wantErrContains: "filemonitor.checks[0].path is required",
		},
		{
			name:    "duplicate paths",
			enabled: true,
			checks: []FileMonitorCheckConfig{
				{Path: "/data/news.mp3", MaxAgeMinutes: 10},
				{Path: "/data/news.mp3", MaxAgeMinutes: 30},
			},
			wantErrContains: "filemonitor.path is duplicated (/data/news.mp3)",
		},
		{
			name:            "relative path",
			enabled:         true,
			checks:          []FileMonitorCheckConfig{{Path: "data/news.mp3", MaxAgeMinutes: 30}},
			wantErrContains: "filemonitor.checks[0].path must be an absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.FileMonitor.Enabled = tt.enabled
			cfg.FileMonitor.Checks = tt.checks

			err := validate(cfg)
			if tt.wantErrContains == "" {
				if err != nil {
					t.Fatalf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() error = nil, want message containing %q", tt.wantErrContains)
			}
			if !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Errorf("validate() error %q missing %q", err.Error(), tt.wantErrContains)
			}
		})
	}
}

func TestGetterDefaults(t *testing.T) {
	api := &APIConfig{}
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"GetRequestTimeout", api.GetRequestTimeout(), 30 * time.Second},
		{"GetUploadReadTimeout", api.GetUploadReadTimeout(), 180 * time.Second},
		{"GetReadTimeout", api.GetReadTimeout(), 30 * time.Second},
		{"GetWriteTimeout", api.GetWriteTimeout(), 60 * time.Second},
		{"GetIdleTimeout", api.GetIdleTimeout(), 120 * time.Second},
		{"GetMaxUploadBodyBytes", api.GetMaxUploadBodyBytes(), int64(70 * 1024 * 1024)},
		{"GetRateLimitRequests", api.GetRateLimitRequests(), 120},
		{"GetRateLimitWindow", api.GetRateLimitWindow(), 60 * time.Second},
		{"GetMaxPixels", (&ImageConfig{}).GetMaxPixels(), int64(25_000_000)},
		{"Interval zero", (&FileMonitorConfig{}).Interval(), 60 * time.Second},
		{"Interval negative", (&FileMonitorConfig{IntervalSeconds: -5}).Interval(), 60 * time.Second},
		{"StatTimeout zero", (&FileMonitorCheckConfig{}).StatTimeout(), 5 * time.Second},
		{"StatTimeout negative", (&FileMonitorCheckConfig{StatTimeoutSec: -1}).StatTimeout(), 5 * time.Second},
		{"StatTimeout configured", (&FileMonitorCheckConfig{StatTimeoutSec: 12}).StatTimeout(), 12 * time.Second},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestGettersRespectConfiguredValues(t *testing.T) {
	api := &APIConfig{
		RequestTimeoutSeconds:    11,
		UploadReadTimeoutSeconds: 12,
		ReadTimeoutSeconds:       13,
		WriteTimeoutSeconds:      14,
		IdleTimeoutSeconds:       15,
		MaxUploadBodyBytes:       16,
		RateLimitRequests:        10,
		RateLimitWindowSeconds:   5,
	}
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"GetRequestTimeout", api.GetRequestTimeout(), 11 * time.Second},
		{"GetUploadReadTimeout", api.GetUploadReadTimeout(), 12 * time.Second},
		{"GetReadTimeout", api.GetReadTimeout(), 13 * time.Second},
		{"GetWriteTimeout", api.GetWriteTimeout(), 14 * time.Second},
		{"GetIdleTimeout", api.GetIdleTimeout(), 15 * time.Second},
		{"GetMaxUploadBodyBytes", api.GetMaxUploadBodyBytes(), int64(16)},
		{"GetRateLimitRequests", api.GetRateLimitRequests(), 10},
		{"GetRateLimitWindow", api.GetRateLimitWindow(), 5 * time.Second},
		{"GetMaxPixels", (&ImageConfig{MaxPixels: 123}).GetMaxPixels(), int64(123)},
		{"Interval", (&FileMonitorConfig{IntervalSeconds: 90}).Interval(), 90 * time.Second},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
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
		{name: "enabled with key", enabled: true, keys: []string{"test-api-key-12345"}},
		{name: "enabled with minimum-length key", enabled: true, keys: []string{"0123456789abcdef"}},
		{name: "enabled with nil keys", enabled: true, wantErr: true},
		{name: "enabled with empty keys", enabled: true, keys: []string{}, wantErr: true},
		{name: "enabled with empty key", enabled: true, keys: []string{""}, wantErr: true},
		{name: "enabled with short key", enabled: true, keys: []string{"short-key"}, wantErr: true},
		{name: "disabled with short key", enabled: false, keys: []string{"short-key"}, wantErr: true},
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

	// The example intentionally leaves the API key and database password blank;
	// filling both must be all an operator needs for a valid configuration.
	cfg.API.Keys = []string{"test-random-api-key"}
	cfg.Database.Password = "test-database-password"
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
