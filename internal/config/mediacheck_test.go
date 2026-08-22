package config

import (
	"strings"
	"testing"
	"time"
)

func TestMediaFileCheckValidation(t *testing.T) {
	tests := []struct {
		name string
		mfc  MediaFileCheckConfig
		// wantErrContains asserts the field label from formatErrors so the
		// test fails if a neighboring rule fires instead of the one under
		// test. Empty means the config must validate cleanly.
		wantErrContains string
	}{
		{
			name: "disabled skips validation",
			mfc: MediaFileCheckConfig{
				Enabled:     false,
				SearchDirs:  []string{"relative/path"}, // would be invalid if enabled
				DriveMounts: map[string]string{"bad": "also-relative"},
			},
		},
		{
			name:            "enabled requires a source",
			mfc:             MediaFileCheckConfig{Enabled: true},
			wantErrContains: "mediafilecheck.search_dirs requires at least one search dir or drive mount when enabled",
		},
		{
			name: "enabled with absolute root",
			mfc: MediaFileCheckConfig{
				Enabled:    true,
				SearchDirs: []string{"/mnt/aeron-audio"},
			},
		},
		{
			name: "relative root rejected",
			mfc: MediaFileCheckConfig{
				Enabled:    true,
				SearchDirs: []string{"relative/audio"},
			},
			wantErrContains: "mediafilecheck.search_dirs[0] must be an absolute path",
		},
		{
			name: "drive mapping valid",
			mfc: MediaFileCheckConfig{
				Enabled:     true,
				DriveMounts: map[string]string{"O:": "/mnt/aeron-o", "Y:": "/mnt/aeron-y"},
			},
		},
		{
			name: "drive mapping bad key",
			mfc: MediaFileCheckConfig{
				Enabled:     true,
				DriveMounts: map[string]string{"audio": "/mnt/aeron-o"},
			},
			wantErrContains: `mediafilecheck.drive_mounts has an invalid drive key "audio"`,
		},
		{
			name: "drive mapping relative target",
			mfc: MediaFileCheckConfig{
				Enabled:     true,
				DriveMounts: map[string]string{"O:": "relative/dir"},
			},
			wantErrContains: `mediafilecheck.drive_mounts target for drive "O:" must be an absolute path`,
		},
		{
			name: "negative lookahead rejected",
			mfc: MediaFileCheckConfig{
				Enabled:       true,
				SearchDirs:    []string{"/mnt/aeron-audio"},
				LookaheadDays: -1,
			},
			wantErrContains: "mediafilecheck.lookaheaddays must be 0 or greater",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.MediaFileCheck = tt.mfc

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

func TestMediaFileCheck_Defaults(t *testing.T) {
	cfg := MediaFileCheckConfig{}

	if got := cfg.StatTimeout(); got != 5*time.Second {
		t.Errorf("StatTimeout default = %v, want 5s", got)
	}
	if got := cfg.GetMaxRangeDays(); got != 31 {
		t.Errorf("GetMaxRangeDays default = %d, want 31", got)
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
