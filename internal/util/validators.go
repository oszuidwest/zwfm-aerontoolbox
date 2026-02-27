// Package util provides utility functions for input validation and HTTP operations.
package util

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/doyensec/safeurl"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// uuidRegex is a lazily-initialized pattern for validating UUID v4 format.
var uuidRegex = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
})

// GUIDPattern matches the standard GUID format (less strict than UUID v4).
var GUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidateEntityID validates that an ID is a proper UUID v4 format.
func ValidateEntityID(id, entityLabel string) error {
	if id == "" {
		return types.NewValidationError("id", fmt.Sprintf("invalid %s ID: must not be empty", entityLabel))
	}

	if !uuidRegex().MatchString(id) {
		return types.NewValidationError("id", fmt.Sprintf("invalid %s ID: must be a UUID", entityLabel))
	}

	return nil
}

// newSafeHTTPClient creates an HTTP client with SSRF protection.
func newSafeHTTPClient() *safeurl.WrappedClient {
	config := safeurl.GetConfigBuilder().Build()
	return safeurl.Client(config)
}

// ValidateURL validates a URL for allowed schemes and hostname presence.
func ValidateURL(urlString string) error {
	if urlString == "" {
		return types.NewValidationError("url", "empty URL")
	}

	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return types.NewValidationError("url", fmt.Sprintf("invalid URL: %v", err))
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return types.NewValidationError("url", "only HTTP and HTTPS URLs allowed")
	}

	if parsedURL.Host == "" {
		return types.NewValidationError("url", "no hostname specified")
	}

	return nil
}

// ValidateContentType validates that a Content-Type header indicates an image.
func ValidateContentType(contentType string) error {
	if contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return types.NewValidationError("image", fmt.Sprintf("not an image content-type: %s", contentType))
	}
	return nil
}

// ValidateImageData validates that byte data represents a valid image.
func ValidateImageData(data []byte) error {
	if len(data) == 0 {
		return types.NewValidationError("image", "image is empty")
	}

	_, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return types.NewValidationError("image", fmt.Sprintf("invalid image: %v", err))
	}

	return nil
}

// ValidateImageFormat validates that an image format is supported.
func ValidateImageFormat(format string) error {
	if !slices.Contains(types.SupportedFormats, format) {
		return types.NewValidationError("image", fmt.Sprintf("file format %s is not supported (use: %v)", format, types.SupportedFormats))
	}
	return nil
}

// ValidateAndDownloadImage validates and securely downloads an image from a URL.
func ValidateAndDownloadImage(urlString string, maxSize int64) ([]byte, error) {
	if err := ValidateURL(urlString); err != nil {
		return nil, err
	}

	client := newSafeHTTPClient()

	resp, err := client.Get(urlString)
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("download failed: %v", err))
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, types.NewValidationError("image", fmt.Sprintf("download failed: HTTP %d", resp.StatusCode))
	}

	contentType := resp.Header.Get("Content-Type")
	if err := ValidateContentType(contentType); err != nil {
		return nil, err
	}

	limitedReader := io.LimitReader(resp.Body, maxSize)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, types.NewValidationError("image", fmt.Sprintf("error reading: %v", err))
	}

	if err := ValidateImageData(data); err != nil {
		return nil, err
	}

	return data, nil
}
