package image

import (
	"bytes"
	stdimage "image"
	"image/color"
	"image/gif"
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
	data := testGIF(t, 10, 10)

	_, err := Process(data, Config{TargetWidth: 10, TargetHeight: 10, Quality: 85})
	if err == nil {
		t.Fatal("Process accepted unsupported GIF")
	}
	if !strings.Contains(err.Error(), "file format gif is not supported") {
		t.Fatalf("error = %q, want unsupported GIF message", err)
	}
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

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := solidRGBA(width, height, color.RGBA{R: 200, G: 80, B: 40, A: 255})
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := solidRGBA(width, height, color.RGBA{R: 40, G: 80, B: 200, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func testGIF(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := stdimage.NewPaletted(stdimage.Rect(0, 0, width, height), []color.Color{
		color.Black,
		color.White,
	})
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func solidRGBA(width, height int, c color.Color) *stdimage.RGBA {
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, c)
		}
	}
	return img
}
