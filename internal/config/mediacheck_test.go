package config

import "testing"

func TestMediaFileCheck_DisabledSkipsValidation(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{
		Enabled:     false,
		SearchDirs:  []string{"relative/path"}, // would be invalid if enabled
		DriveMounts: map[string]string{"bad": "also-relative"},
	}

	if err := validate(cfg); err != nil {
		t.Fatalf("disabled media_file_check should skip validation, got: %v", err)
	}
}

func TestMediaFileCheck_EnabledRequiresSource(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{Enabled: true}

	if err := validate(cfg); err == nil {
		t.Fatal("expected error when enabled with no search dirs or drive mounts, got nil")
	}
}

func TestMediaFileCheck_EnabledWithAbsoluteRoot(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{
		Enabled:    true,
		SearchDirs: []string{"/mnt/aeron-audio"},
	}

	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestMediaFileCheck_RelativeRootRejected(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{
		Enabled:    true,
		SearchDirs: []string{"relative/audio"},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected error for relative root, got nil")
	}
}

func TestMediaFileCheck_DriveMappingValid(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{
		Enabled:     true,
		DriveMounts: map[string]string{"O:": "/mnt/aeron-o", "Y:": "/mnt/aeron-y"},
	}

	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestMediaFileCheck_DriveMappingBadKey(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{
		Enabled:     true,
		DriveMounts: map[string]string{"audio": "/mnt/aeron-o"},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected error for invalid drive key, got nil")
	}
}

func TestMediaFileCheck_DriveMappingRelativeTarget(t *testing.T) {
	cfg := minimalConfig()
	cfg.MediaFileCheck = MediaFileCheckConfig{
		Enabled:     true,
		DriveMounts: map[string]string{"O:": "relative/dir"},
	}

	if err := validate(cfg); err == nil {
		t.Fatal("expected error for relative drive mapping target, got nil")
	}
}

func TestMediaFileCheck_Defaults(t *testing.T) {
	cfg := MediaFileCheckConfig{}

	if got := cfg.StatTimeout(); got.Seconds() != DefaultMediaFileCheckStatTimeoutSeconds {
		t.Errorf("StatTimeout default = %v, want %ds", got, DefaultMediaFileCheckStatTimeoutSeconds)
	}
	if got := cfg.GetMaxRangeDays(); got != DefaultMediaFileCheckMaxRangeDays {
		t.Errorf("GetMaxRangeDays default = %d, want %d", got, DefaultMediaFileCheckMaxRangeDays)
	}
	if !cfg.IsCaseInsensitive() {
		t.Error("IsCaseInsensitive default = false, want true")
	}

	off := false
	cfg.CaseInsensitive = &off
	if cfg.IsCaseInsensitive() {
		t.Error("IsCaseInsensitive = true after explicit false")
	}
}
