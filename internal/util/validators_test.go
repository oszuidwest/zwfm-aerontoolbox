package util

import "testing"

func TestValidateImageFormatAcceptsPNGAndRejectsGIF(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr bool
	}{
		{
			name:   "png",
			format: "png",
		},
		{
			name:    "gif",
			format:  "gif",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageFormat(tt.format)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateImageFormat(%q) error = %v, wantErr %v", tt.format, err, tt.wantErr)
			}
		})
	}
}
