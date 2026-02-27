// Package image provides image processing and optimization functionality.
package image

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
	"golang.org/x/image/draw"
)

// Config contains image processing settings.
type Config struct {
	TargetWidth   int
	TargetHeight  int
	Quality       int
	RejectSmaller bool
}

// ProcessingResult contains the results of image processing operations.
type ProcessingResult struct {
	Data      []byte
	Format    string
	Encoder   string
	Original  Info
	Optimized Info
	Savings   float64
}

// Info contains image metadata.
type Info struct {
	Format string
	Width  int
	Height int
	Size   int
}

// Optimizer handles image optimization operations.
type Optimizer struct {
	Config Config
}

// NewOptimizer returns an Optimizer configured with the specified settings.
func NewOptimizer(config Config) *Optimizer {
	return &Optimizer{Config: config}
}

// DownloadImage downloads an image from a URL with SSRF protection.
func DownloadImage(urlString string, maxSize int64) ([]byte, error) {
	return util.ValidateAndDownloadImage(urlString, maxSize)
}

// getImageInfo extracts format, width, and height metadata from image data.
func getImageInfo(data []byte) (format string, width, height int, err error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, err
	}
	return format, config.Width, config.Height, nil
}

// OptimizeImage processes and optimizes image data according to the configured settings.
func (o *Optimizer) OptimizeImage(data []byte) (optimized []byte, format, encoder string, err error) {
	_, format, err = image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", err
	}

	switch format {
	case "jpeg", "jpg":
		return o.optimizeJPEG(data)
	case "png":
		return o.convertPNGToJPEG(data)
	default:
		return data, format, "original", nil
	}
}

// optimizeJPEG processes JPEG image data to optimize size and dimensions.
func (o *Optimizer) optimizeJPEG(data []byte) (optimized []byte, format, encoder string, err error) {
	var sourceImage image.Image
	sourceImage, err = jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", types.NewValidationError("image", fmt.Sprintf("failed to decode JPEG: %v", err))
	}

	return o.processImage(sourceImage, data, "jpeg", "jpeg")
}

// convertPNGToJPEG converts PNG image data to optimized JPEG format.
func (o *Optimizer) convertPNGToJPEG(data []byte) (optimized []byte, format, encoder string, err error) {
	var sourceImage image.Image
	sourceImage, err = png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", "", types.NewValidationError("image", fmt.Sprintf("failed to decode PNG: %v", err))
	}

	return o.processImage(sourceImage, data, "png", "jpeg")
}

// processImage resizes and encodes an image, returning optimized data if smaller.
// If the optimized version is not smaller, it returns the original data with its original format.
func (o *Optimizer) processImage(sourceImage image.Image, originalData []byte, originalFormat, targetFormat string) (optimized []byte, format, encoder string, err error) {
	bounds := sourceImage.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	if width > o.Config.TargetWidth || height > o.Config.TargetHeight {
		sourceImage = o.resizeImage(sourceImage, o.Config.TargetWidth, o.Config.TargetHeight)
	}

	var jpegBuffer bytes.Buffer
	if err := jpeg.Encode(&jpegBuffer, sourceImage, &jpeg.Options{Quality: o.Config.Quality}); err != nil {
		return nil, "", "", types.NewValidationError("image", fmt.Sprintf("JPEG encoding failed: %v", err))
	}
	optimizedData := jpegBuffer.Bytes()

	if len(optimizedData) < len(originalData) {
		return optimizedData, targetFormat, "optimized", nil
	}

	return originalData, originalFormat, "original", nil
}

// resizeImage scales an image to fit within max dimensions using Catmull-Rom.
func (o *Optimizer) resizeImage(sourceImage image.Image, maxWidth, maxHeight int) image.Image {
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

// Process is the main entry point for image processing.
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
	format, width, height, err := getImageInfo(imageData)
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("failed to get image information: %v", err))
	}

	return &Info{
		Format: format,
		Width:  width,
		Height: height,
		Size:   len(imageData),
	}, nil
}

// validateImageDimensions checks minimum size requirements when RejectSmaller is set.
func validateImageDimensions(info *Info, config Config) error {
	if config.RejectSmaller && (info.Width < config.TargetWidth || info.Height < config.TargetHeight) {
		return &types.ValidationError{
			Field: "dimensions",
			Message: fmt.Sprintf("image is too small: %dx%d (minimum %dx%d required)",
				info.Width, info.Height, config.TargetWidth, config.TargetHeight),
		}
	}
	return nil
}

// isAlreadyTargetSize returns true if image matches target dimensions exactly.
func isAlreadyTargetSize(info *Info, config Config) bool {
	return info.Width == config.TargetWidth && info.Height == config.TargetHeight
}

// createSkippedResult creates a result for images needing no optimization.
func createSkippedResult(imageData []byte, originalInfo *Info) *ProcessingResult {
	return &ProcessingResult{
		Data:      imageData,
		Format:    originalInfo.Format,
		Encoder:   "original (no optimization needed)",
		Original:  *originalInfo,
		Optimized: *originalInfo,
		Savings:   0,
	}
}

// optimizeImageData runs the optimization pipeline and returns processing results.
func optimizeImageData(imageData []byte, originalInfo *Info, config Config) (*ProcessingResult, error) {
	optimizer := NewOptimizer(config)
	optimizedData, optFormat, optEncoder, err := optimizer.OptimizeImage(imageData)
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("optimization failed: %v", err))
	}

	optimizedInfo, err := extractImageInfo(optimizedData)
	if err != nil {
		optimizedInfo = &Info{
			Format: optFormat,
			Width:  originalInfo.Width,
			Height: originalInfo.Height,
			Size:   len(optimizedData),
		}
	}

	savings := float64(originalInfo.Size-optimizedInfo.Size) / float64(originalInfo.Size) * 100

	return &ProcessingResult{
		Data:      optimizedData,
		Format:    optFormat,
		Encoder:   optEncoder,
		Original:  *originalInfo,
		Optimized: *optimizedInfo,
		Savings:   savings,
	}, nil
}
