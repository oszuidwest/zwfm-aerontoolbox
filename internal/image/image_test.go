package image

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	stdimage "image"
	"image/color"
	stdpng "image/png"
	"strings"
	"testing"
)

func TestProcessRejectsOversizedDimensionsBeforeDecode(t *testing.T) {
	data := pngWithDimensions(t, 10_000, 10_000)

	_, err := Process(data, Config{
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
	if strings.Contains(err.Error(), "failed to decode PNG") {
		t.Fatalf("error = %q, oversized image should be rejected before full PNG decode", err.Error())
	}
}

func TestProcessAllowsImageAtPixelLimit(t *testing.T) {
	var buf bytes.Buffer
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.White)
	if err := stdpng.Encode(&buf, img); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result, err := Process(buf.Bytes(), Config{
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

func TestExceedsMaxPixelsAvoidsOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if !exceedsMaxPixels(maxInt, maxInt, 1) {
		t.Fatal("exceedsMaxPixels = false, want true for huge dimensions")
	}
}

func pngWithDimensions(t *testing.T, width, height uint32) []byte {
	t.Helper()

	var data bytes.Buffer
	writeBytes(t, &data, []byte{137, 80, 78, 71, 13, 10, 26, 10})

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // truecolor

	writePNGChunk(t, &data, "IHDR", ihdr)
	writePNGChunk(t, &data, "IEND", nil)

	return data.Bytes()
}

func writePNGChunk(t *testing.T, dst *bytes.Buffer, chunkType string, data []byte) {
	t.Helper()

	if err := binary.Write(dst, binary.BigEndian, uint32(len(data))); err != nil {
		t.Fatalf("write chunk length: %v", err)
	}
	writeString(t, dst, chunkType)
	writeBytes(t, dst, data)

	crcData := append([]byte(chunkType), data...)
	if err := binary.Write(dst, binary.BigEndian, crc32.ChecksumIEEE(crcData)); err != nil {
		t.Fatalf("write chunk crc: %v", err)
	}
}

func writeString(t *testing.T, dst *bytes.Buffer, value string) {
	t.Helper()
	if _, err := dst.WriteString(value); err != nil {
		t.Fatalf("write string: %v", err)
	}
}

func writeBytes(t *testing.T, dst *bytes.Buffer, value []byte) {
	t.Helper()
	if _, err := dst.Write(value); err != nil {
		t.Fatalf("write bytes: %v", err)
	}
}
