package storage

import (
	"context"
	"strings"
)

// StorageInterface defines the interface that all storage backends must implement
type StorageInterface interface {
	SaveImage(imageData []byte, slug, contentType string) (string, error)
	SaveContent(content, slug string) (string, error)
	ReadImage(relPath string) ([]byte, error)
	ReadContent(relPath string) (string, error)
	DeleteImage(relPath string) error
	DeleteContent(relPath string) error
}

// NewStorage creates a new S3 storage instance
// The scraper now only supports S3-compatible storage (MinIO for dev/staging, DO Spaces for production)
func NewStorage(ctx context.Context, config S3Config) (StorageInterface, error) {
	return NewS3Storage(ctx, config)
}

// extensionFromContentType returns the file extension for a content type
func extensionFromContentType(contentType string) string {
	// Normalize content type (remove charset, etc.)
	contentType = strings.ToLower(strings.Split(contentType, ";")[0])
	contentType = strings.TrimSpace(contentType)

	switch contentType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	default:
		return ""
	}
}
