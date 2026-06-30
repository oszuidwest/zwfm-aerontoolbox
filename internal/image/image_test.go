package image

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	stdimage "image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestProcessRejectsInvalidImageData(t *testing.T) {
	_, err := Process([]byte("not an image"), Config{TargetWidth: 10, TargetHeight: 10, Quality: 85})
	if err == nil {
		t.Fatal("Process accepted invalid image data")
	}
	if !strings.Contains(err.Error(), "failed to get image information") {
		t.Fatalf("error = %q, want image information failure", err)
	}
}

func TestProcessRejectsUnsupportedGIF(t *testing.T) {
	data := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x00;")

	_, err := Process(data, Config{TargetWidth: 10, TargetHeight: 10, Quality: 85})
	if err == nil {
		t.Fatal("Process accepted unsupported GIF")
	}
	// GIF is unsupported here because the decoder is intentionally not registered.
}

func TestProcessRejectsSmallerImageWhenConfigured(t *testing.T) {
	data := testJPEG(t, 10, 10)

	_, err := Process(data, Config{TargetWidth: 20, TargetHeight: 20, Quality: 85, RejectSmaller: true})
	if err == nil {
		t.Fatal("Process accepted smaller image with RejectSmaller")
	}
	if !strings.Contains(err.Error(), "image is too small") {
		t.Fatalf("error = %q, want too small message", err)
	}
}

func TestProcessRejectsOversizedDimensionsBeforeDecode(t *testing.T) {
	tests := []struct {
		name          string
		data          []byte
		decodeFailure string
	}{
		{
			name:          "png",
			data:          pngWithDimensions(t, 10_000, 10_000),
			decodeFailure: "failed to decode PNG",
		},
		{
			name:          "jpeg",
			data:          jpegWithDimensions(t, 10_000, 10_000),
			decodeFailure: "failed to decode JPEG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Process(tt.data, Config{
				TargetWidth:  640,
				TargetHeight: 640,
				Quality:      85,
				MaxPixels:    1_000_000,
			})
			if err == nil {
				t.Fatal("Process returned nil error, want oversized dimensions error")
			}
			if !strings.Contains(err.Error(), "image is too large") {
				t.Fatalf("error = %q, want oversized dimensions error", err.Error())
			}
			if !strings.Contains(err.Error(), "100000000 pixels exceeds maximum 1000000") {
				t.Fatalf("error = %q, want actual pixel count and maximum", err.Error())
			}
			if strings.Contains(err.Error(), tt.decodeFailure) {
				t.Fatalf("error = %q, oversized image should be rejected before full decode", err.Error())
			}
		})
	}
}

func TestProcessAllowsImageAtPixelLimit(t *testing.T) {
	data := testPNG(t, 1, 1)

	result, err := Process(data, Config{
		TargetWidth:  1,
		TargetHeight: 1,
		Quality:      85,
		MaxPixels:    1,
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if result.Original.Width != 1 || result.Original.Height != 1 {
		t.Fatalf("original dimensions = %dx%d, want 1x1", result.Original.Width, result.Original.Height)
	}
}

func TestProcessSkipsAlreadyTargetSize(t *testing.T) {
	data := testPNG(t, 12, 8)

	result, err := Process(data, Config{TargetWidth: 12, TargetHeight: 8, Quality: 85})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Encoder != "original (no optimization needed)" {
		t.Fatalf("encoder = %q, want skip encoder", result.Encoder)
	}
	if result.Original.Width != 12 || result.Original.Height != 8 {
		t.Fatalf("original dimensions = %dx%d, want 12x8", result.Original.Width, result.Original.Height)
	}
	if !bytes.Equal(result.Data, data) {
		t.Fatal("skipped result changed image bytes")
	}
}

func TestExceedsMaxPixels(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name      string
		width     int
		height    int
		maxPixels int64
		want      bool
	}{
		{
			name:      "disabled",
			width:     10_000,
			height:    10_000,
			maxPixels: 0,
			want:      false,
		},
		{
			name:      "exactly at limit",
			width:     1,
			height:    1,
			maxPixels: 1,
			want:      false,
		},
		{
			name:      "just over limit",
			width:     1,
			height:    2,
			maxPixels: 1,
			want:      true,
		},
		{
			name:      "avoids overflow",
			width:     maxInt,
			height:    maxInt,
			maxPixels: 1,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exceedsMaxPixels(tt.width, tt.height, tt.maxPixels); got != tt.want {
				t.Fatalf("exceedsMaxPixels(%d, %d, %d) = %t, want %t",
					tt.width, tt.height, tt.maxPixels, got, tt.want)
			}
		})
	}
}

// testJPEG returns an encoded solid-color JPEG for image processing tests.
func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := solidRGBA(width, height, color.RGBA{R: 200, G: 80, B: 40, A: 255})
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// testPNG returns an encoded solid-color PNG for image processing tests.
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := solidRGBA(width, height, color.RGBA{R: 40, G: 80, B: 200, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// solidRGBA builds a solid-color RGBA image for encoder fixtures.
func solidRGBA(width, height int, c color.Color) *stdimage.RGBA {
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, c)
		}
	}
	return img
}

func pngWithDimensions(t *testing.T, width, height uint32) []byte {
	t.Helper()

	data := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // truecolor

	data = appendPNGChunk(t, data, "IHDR", ihdr)
	return appendPNGChunk(t, data, "IEND", nil)
}

func appendPNGChunk(t *testing.T, dst []byte, chunkType string, data []byte) []byte {
	t.Helper()

	if uint64(len(data)) > uint64(^uint32(0)) {
		t.Fatalf("chunk data too large: %d", len(data))
	}
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(data))) //nolint:gosec // checked above against PNG's uint32 chunk length limit.
	dst = append(dst, chunkType...)
	dst = append(dst, data...)
	crcData := append([]byte(chunkType), data...)
	return binary.BigEndian.AppendUint32(dst, crc32.ChecksumIEEE(crcData))
}

func jpegWithDimensions(t *testing.T, width, height uint16) []byte {
	t.Helper()

	data := []byte{0xff, 0xd8}
	sof0 := []byte{8}
	sof0 = binary.BigEndian.AppendUint16(sof0, height)
	sof0 = binary.BigEndian.AppendUint16(sof0, width)
	sof0 = append(sof0,
		3,
		1, 0x11, 0,
		2, 0x11, 0,
		3, 0x11, 0,
	)
	data = appendJPEGMarker(data, 0xc0, sof0)
	sos := []byte{
		3,
		1, 0,
		2, 0,
		3, 0,
		0, 63, 0,
	}
	data = appendJPEGMarker(data, 0xda, sos)
	return append(data, 0xff, 0xd9)
}

func appendJPEGMarker(dst []byte, marker byte, payload []byte) []byte {
	dst = append(dst, 0xff, marker)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(payload)+2)) //nolint:gosec // test fixtures pass small marker payloads.
	return append(dst, payload...)
}
