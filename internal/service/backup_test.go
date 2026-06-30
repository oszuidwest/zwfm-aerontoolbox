package service

import (
	"strings"
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

func TestCompressionLevel(t *testing.T) {
	svc := &BackupService{
		config: &config.Config{
			Backup: config.BackupConfig{DefaultCompression: 6},
		},
	}

	tests := []struct {
		name      string
		requested int
		want      int
		wantErr   string
	}{
		{
			name:      "explicit zero uses default",
			requested: 0,
			want:      6,
		},
		{
			name:      "explicit level",
			requested: 5,
			want:      5,
		},
		{
			name:      "max valid level",
			requested: 9,
			want:      9,
		},
		{
			name:      "negative level",
			requested: -1,
			wantErr:   "use 0 for default, or 1-9",
		},
		{
			name:      "too high level",
			requested: 10,
			wantErr:   "use 0 for default, or 1-9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.compressionLevel(tt.requested)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("compressionLevel returned nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("compressionLevel returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("compressionLevel = %d, want %d", got, tt.want)
			}
		})
	}
}
