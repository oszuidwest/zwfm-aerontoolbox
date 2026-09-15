package util

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestValidators(t *testing.T) {
	// Importing image/png also registers the decoder ValidateImageData needs.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	pngData := buf.Bytes()

	tests := []struct {
		name    string
		check   func() error
		wantErr bool
	}{
		{
			name:  "entity id accepts uuid v4",
			check: func() error { return ValidateEntityID("9e37ff1f-7823-43ce-93d0-12fc1c2edb8b", "artist") },
		},
		{
			name:    "entity id rejects empty",
			check:   func() error { return ValidateEntityID("", "artist") },
			wantErr: true,
		},
		{
			name:    "entity id rejects non-uuid",
			check:   func() error { return ValidateEntityID("not-a-uuid", "artist") },
			wantErr: true,
		},
		{
			name:  "content type accepts image",
			check: func() error { return ValidateContentType("image/png") },
		},
		{
			name:  "content type accepts absent header",
			check: func() error { return ValidateContentType("") },
		},
		{
			name:    "content type rejects non-image",
			check:   func() error { return ValidateContentType("text/html") },
			wantErr: true,
		},
		{
			name:  "image data accepts decodable png",
			check: func() error { return ValidateImageData(pngData) },
		},
		{
			name:    "image data rejects empty",
			check:   func() error { return ValidateImageData(nil) },
			wantErr: true,
		},
		{
			name:    "image data rejects undecodable bytes",
			check:   func() error { return ValidateImageData([]byte("not an image")) },
			wantErr: true,
		},
		{
			name:  "image format accepts png",
			check: func() error { return ValidateImageFormat("png") },
		},
		{
			name:    "image format rejects gif",
			check:   func() error { return ValidateImageFormat("gif") },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check()
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
