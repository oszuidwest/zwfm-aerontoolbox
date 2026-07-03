// Package image validates, resizes, and re-encodes uploaded artwork.
package image

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode/DecodeConfig

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
	"golang.org/x/image/draw"
)

// Config controls image dimensions, quality, and minimum-size policy.
type Config struct {
	TargetWidth   int
	TargetHeight  int
	Quality       int
	RejectSmaller bool
	MaxPixels     int64
}

// ProcessingResult reports original and stored image characteristics.
type ProcessingResult struct {
	Data      []byte
	Original  Info
	Optimized Info
	Savings   float64
}

// Info is decoded image metadata.
type Info struct {
	Format string
	Width  int
	Height int
	Size   int
}

// optimizeImage resizes and re-encodes supported image data (JPEG or PNG,
// both re-encoded as JPEG) when the result is smaller; otherwise it returns
// the original bytes. Unsupported formats pass through unchanged.
func optimizeImage(data []byte, format string, cfg Config) ([]byte, error) {
	switch format {
	case "jpeg", "jpg", "png":
	default:
		return data, nil
	}

	sourceImage, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("failed to decode %s: %v", format, err))
	}

	bounds := sourceImage.Bounds()
	if bounds.Dx() > cfg.TargetWidth || bounds.Dy() > cfg.TargetHeight {
		sourceImage = resizeImage(sourceImage, cfg.TargetWidth, cfg.TargetHeight)
	}

	var jpegBuffer bytes.Buffer
	if err := jpeg.Encode(&jpegBuffer, sourceImage, &jpeg.Options{Quality: cfg.Quality}); err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("JPEG encoding failed: %v", err))
	}

	if optimized := jpegBuffer.Bytes(); len(optimized) < len(data) {
		return optimized, nil
	}
	return data, nil
}

// resizeImage scales an image to fit within max dimensions using Catmull-Rom.
func resizeImage(sourceImage image.Image, maxWidth, maxHeight int) image.Image {
	bounds := sourceImage.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	scaleFactorX := float64(maxWidth) / float64(width)
	scaleFactorY := float64(maxHeight) / float64(height)
	scale := min(scaleFactorX, scaleFactorY)

	if scale >= 1 {
		return sourceImage
	}

	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), sourceImage, sourceImage.Bounds(), draw.Over, nil)

	return dst
}

// Process validates imageData and returns the bytes that should be stored.
func Process(imageData []byte, config Config) (*ProcessingResult, error) {
	originalInfo, err := extractImageInfo(imageData)
	if err != nil {
		return nil, err
	}

	if err := validateImage(originalInfo, config); err != nil {
		return nil, err
	}

	if isAlreadyTargetSize(originalInfo, config) {
		return createSkippedResult(imageData, originalInfo), nil
	}

	return optimizeImageData(imageData, originalInfo, config)
}

// validateImage checks format support and dimension requirements.
func validateImage(info *Info, config Config) error {
	if err := util.ValidateImageFormat(info.Format); err != nil {
		return err
	}
	return validateImageDimensions(info, config)
}

// extractImageInfo decodes image metadata into an Info struct.
func extractImageInfo(imageData []byte) (*Info, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(imageData))
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("failed to get image information: %v", err))
	}

	return &Info{
		Format: format,
		Width:  config.Width,
		Height: config.Height,
		Size:   len(imageData),
	}, nil
}

// validateImageDimensions checks minimum and maximum size requirements.
func validateImageDimensions(info *Info, config Config) error {
	if exceedsMaxPixels(info.Width, info.Height, config.MaxPixels) {
		return types.NewValidationError("dimensions", fmt.Sprintf(
			"image is too large: %dx%d (%d pixels exceeds maximum %d)",
			info.Width, info.Height, int64(info.Width)*int64(info.Height), config.MaxPixels))
	}

	if config.RejectSmaller && (info.Width < config.TargetWidth || info.Height < config.TargetHeight) {
		return types.NewValidationError("dimensions", fmt.Sprintf(
			"image is too small: %dx%d (minimum %dx%d required)",
			info.Width, info.Height, config.TargetWidth, config.TargetHeight))
	}
	return nil
}

// exceedsMaxPixels reports whether width*height exceeds maxPixels, rearranged
// as division so the product cannot overflow int64 for hostile dimensions.
func exceedsMaxPixels(width, height int, maxPixels int64) bool {
	if maxPixels <= 0 || width <= 0 || height <= 0 {
		return false
	}
	return int64(width) > maxPixels/int64(height)
}

// isAlreadyTargetSize returns true if image matches target dimensions exactly.
func isAlreadyTargetSize(info *Info, config Config) bool {
	return info.Width == config.TargetWidth && info.Height == config.TargetHeight
}

// createSkippedResult creates a result for images needing no optimization.
func createSkippedResult(imageData []byte, originalInfo *Info) *ProcessingResult {
	return &ProcessingResult{
		Data:      imageData,
		Original:  *originalInfo,
		Optimized: *originalInfo,
		Savings:   0,
	}
}

// optimizeImageData runs the optimization pipeline and returns processing results.
func optimizeImageData(imageData []byte, originalInfo *Info, config Config) (*ProcessingResult, error) {
	optimizedData, err := optimizeImage(imageData, originalInfo.Format, config)
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("optimization failed: %v", err))
	}

	optimizedInfo, err := extractImageInfo(optimizedData)
	if err != nil {
		optimizedInfo = &Info{
			Format: originalInfo.Format,
			Width:  originalInfo.Width,
			Height: originalInfo.Height,
			Size:   len(optimizedData),
		}
	}

	savings := float64(originalInfo.Size-optimizedInfo.Size) / float64(originalInfo.Size) * 100

	return &ProcessingResult{
		Data:      optimizedData,
		Original:  *originalInfo,
		Optimized: *optimizedInfo,
		Savings:   savings,
	}, nil
}
