package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()

	config := Config{
		BasePath: tmpDir,
	}

	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	if storage == nil {
		t.Fatal("Expected storage to be non-nil")
	}

	// Verify base directory was created
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("Base directory was not created")
	}
}

func TestSaveImage(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	imageData := []byte("fake image data")
	slug := "test-image"
	contentType := "image/jpeg"

	filePath, err := storage.SaveImage(imageData, slug, contentType)
	if err != nil {
		t.Fatalf("Failed to save image: %v", err)
	}

	if filePath == "" {
		t.Error("Expected non-empty file path")
	}

	// Verify file was created
	fullPath := filepath.Join(tmpDir, filePath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Error("Image file was not created")
	}

	// Verify file contents
	readData, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("Failed to read saved image: %v", err)
	}

	if string(readData) != string(imageData) {
		t.Error("Saved image data does not match original")
	}
}

func TestSaveImageWithDifferentContentTypes(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	tests := []struct {
		name        string
		contentType string
		expectedExt string
	}{
		{"jpeg", "image/jpeg", ".jpg"},
		{"png", "image/png", ".png"},
		{"gif", "image/gif", ".gif"},
		{"webp", "image/webp", ".webp"},
		{"svg", "image/svg+xml", ".svg"},
		{"default", "image/unknown", ".jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imageData := []byte("test data")
			slug := "test-" + tt.name

			filePath, err := storage.SaveImage(imageData, slug, tt.contentType)
			if err != nil {
				t.Fatalf("Failed to save image: %v", err)
			}

			ext := filepath.Ext(filePath)
			if ext != tt.expectedExt {
				t.Errorf("Expected extension %s, got %s", tt.expectedExt, ext)
			}
		})
	}
}

func TestSaveImageDuplicateSlug(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	slug := "duplicate-test"
	imageData1 := []byte("first image")
	imageData2 := []byte("second image")

	// Save first image
	path1, err := storage.SaveImage(imageData1, slug, "image/jpeg")
	if err != nil {
		t.Fatalf("Failed to save first image: %v", err)
	}

	// Save second image with same slug
	path2, err := storage.SaveImage(imageData2, slug, "image/jpeg")
	if err != nil {
		t.Fatalf("Failed to save second image: %v", err)
	}

	// Paths should be different
	if path1 == path2 {
		t.Error("Expected different paths for duplicate slugs")
	}

	// Both files should exist
	fullPath1 := filepath.Join(tmpDir, path1)
	fullPath2 := filepath.Join(tmpDir, path2)

	if _, err := os.Stat(fullPath1); os.IsNotExist(err) {
		t.Error("First image file does not exist")
	}
	if _, err := os.Stat(fullPath2); os.IsNotExist(err) {
		t.Error("Second image file does not exist")
	}
}

func TestSaveContent(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	content := "<html><body>Test content</body></html>"
	slug := "test-content"

	filePath, err := storage.SaveContent(content, slug)
	if err != nil {
		t.Fatalf("Failed to save content: %v", err)
	}

	if filePath == "" {
		t.Error("Expected non-empty file path")
	}

	// Verify file was created
	fullPath := filepath.Join(tmpDir, filePath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Error("Content file was not created")
	}

	// Verify file has .html extension
	if filepath.Ext(filePath) != ".html" {
		t.Errorf("Expected .html extension, got %s", filepath.Ext(filePath))
	}

	// Verify file contents
	readData, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("Failed to read saved content: %v", err)
	}

	if string(readData) != content {
		t.Error("Saved content does not match original")
	}
}

func TestReadImage(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	imageData := []byte("test image data")
	slug := "read-test"

	// Save image
	filePath, err := storage.SaveImage(imageData, slug, "image/jpeg")
	if err != nil {
		t.Fatalf("Failed to save image: %v", err)
	}

	// Read image
	readData, err := storage.ReadImage(filePath)
	if err != nil {
		t.Fatalf("Failed to read image: %v", err)
	}

	if string(readData) != string(imageData) {
		t.Error("Read image data does not match original")
	}
}

func TestReadContent(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	content := "<html><body>Test</body></html>"
	slug := "read-content-test"

	// Save content
	filePath, err := storage.SaveContent(content, slug)
	if err != nil {
		t.Fatalf("Failed to save content: %v", err)
	}

	// Read content
	readContent, err := storage.ReadContent(filePath)
	if err != nil {
		t.Fatalf("Failed to read content: %v", err)
	}

	if readContent != content {
		t.Error("Read content does not match original")
	}
}

func TestDeleteImage(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	imageData := []byte("delete test")
	slug := "delete-test"

	// Save image
	filePath, err := storage.SaveImage(imageData, slug, "image/jpeg")
	if err != nil {
		t.Fatalf("Failed to save image: %v", err)
	}

	fullPath := filepath.Join(tmpDir, filePath)

	// Verify file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Fatal("Image file was not created")
	}

	// Delete image
	if err := storage.DeleteImage(filePath); err != nil {
		t.Fatalf("Failed to delete image: %v", err)
	}

	// Verify file is deleted
	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Error("Image file was not deleted")
	}
}

func TestDeleteNonExistentImage(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Delete non-existent image should not error
	err = storage.DeleteImage("non-existent/path.jpg")
	if err != nil {
		t.Errorf("Delete non-existent image returned error: %v", err)
	}
}

func TestGetFullPath(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{BasePath: tmpDir}
	storage, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	relPath := "images/2025/10/test.jpg"
	expectedPath := filepath.Join(tmpDir, relPath)

	fullPath := storage.GetFullPath(relPath)
	if fullPath != expectedPath {
		t.Errorf("Expected full path %s, got %s", expectedPath, fullPath)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.BasePath != "./storage" {
		t.Errorf("Expected default base path './storage', got %s", config.BasePath)
	}
}
