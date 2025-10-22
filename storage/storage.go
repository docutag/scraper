package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config contains storage configuration
type Config struct {
	BasePath string // Base directory for all stored files
}

// DefaultConfig returns default storage configuration
func DefaultConfig() Config {
	return Config{
		BasePath: "./storage",
	}
}

// Storage handles filesystem storage operations
type Storage struct {
	config Config
}

// New creates a new Storage instance
func New(config Config) (*Storage, error) {
	// Create base directory if it doesn't exist
	if err := os.MkdirAll(config.BasePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base storage directory: %w", err)
	}

	return &Storage{
		config: config,
	}, nil
}

// SaveImage saves an image to the filesystem
// Returns the relative file path from the base storage directory
func (s *Storage) SaveImage(imageData []byte, slug, contentType string) (string, error) {
	// Determine file extension from content type
	ext := extensionFromContentType(contentType)
	if ext == "" {
		ext = ".jpg" // Default extension
	}

	// Generate directory structure: images/YYYY/MM/
	now := time.Now()
	year := fmt.Sprintf("%04d", now.Year())
	month := fmt.Sprintf("%02d", int(now.Month()))

	dirPath := filepath.Join(s.config.BasePath, "images", year, month)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create image directory: %w", err)
	}

	// Generate filename: slug.ext
	filename := slug + ext
	filePath := filepath.Join(dirPath, filename)

	// Check if file already exists and make unique if necessary
	counter := 1
	for fileExists(filePath) {
		filename = fmt.Sprintf("%s-%d%s", slug, counter, ext)
		filePath = filepath.Join(dirPath, filename)
		counter++
	}

	// Write file
	if err := os.WriteFile(filePath, imageData, 0644); err != nil {
		return "", fmt.Errorf("failed to write image file: %w", err)
	}

	// Return relative path from base storage directory
	relPath, err := filepath.Rel(s.config.BasePath, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path: %w", err)
	}

	return relPath, nil
}

// SaveContent saves scraped HTML content to the filesystem
// Returns the relative file path from the base storage directory
func (s *Storage) SaveContent(content, slug string) (string, error) {
	// Generate directory structure: content/YYYY/MM/
	now := time.Now()
	year := fmt.Sprintf("%04d", now.Year())
	month := fmt.Sprintf("%02d", int(now.Month()))

	dirPath := filepath.Join(s.config.BasePath, "content", year, month)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create content directory: %w", err)
	}

	// Generate filename: slug.html
	filename := slug + ".html"
	filePath := filepath.Join(dirPath, filename)

	// Check if file already exists and make unique if necessary
	counter := 1
	for fileExists(filePath) {
		filename = fmt.Sprintf("%s-%d.html", slug, counter)
		filePath = filepath.Join(dirPath, filename)
		counter++
	}

	// Write file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write content file: %w", err)
	}

	// Return relative path from base storage directory
	relPath, err := filepath.Rel(s.config.BasePath, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path: %w", err)
	}

	return relPath, nil
}

// ReadImage reads an image from the filesystem
func (s *Storage) ReadImage(relPath string) ([]byte, error) {
	fullPath := filepath.Join(s.config.BasePath, relPath)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read image file: %w", err)
	}

	return data, nil
}

// ReadContent reads content from the filesystem
func (s *Storage) ReadContent(relPath string) (string, error) {
	fullPath := filepath.Join(s.config.BasePath, relPath)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read content file: %w", err)
	}

	return string(data), nil
}

// DeleteImage deletes an image from the filesystem
func (s *Storage) DeleteImage(relPath string) error {
	fullPath := filepath.Join(s.config.BasePath, relPath)

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete image file: %w", err)
	}

	return nil
}

// DeleteContent deletes content from the filesystem
func (s *Storage) DeleteContent(relPath string) error {
	fullPath := filepath.Join(s.config.BasePath, relPath)

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete content file: %w", err)
	}

	return nil
}

// GetFullPath returns the full filesystem path for a relative path
func (s *Storage) GetFullPath(relPath string) string {
	return filepath.Join(s.config.BasePath, relPath)
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
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
