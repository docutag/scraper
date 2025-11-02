package storage

import (
	"context"
	"testing"
)

// TestNewStorage tests creating S3 storage with valid config
func TestNewStorage(t *testing.T) {
	config := S3Config{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		Bucket:          "test-bucket",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		UsePathStyle:    true,
	}

	ctx := context.Background()
	storage, err := NewStorage(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	if storage == nil {
		t.Fatal("Expected storage to be non-nil")
	}
}

// TestNewStorageMissingBucket tests error handling for missing bucket
func TestNewStorageMissingBucket(t *testing.T) {
	config := S3Config{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		Bucket:          "", // Missing bucket
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		UsePathStyle:    true,
	}

	ctx := context.Background()
	_, err := NewStorage(ctx, config)
	if err == nil {
		t.Fatal("Expected error for missing bucket, got nil")
	}
}

// TestNewStorageMissingRegion tests error handling for missing region
func TestNewStorageMissingRegion(t *testing.T) {
	config := S3Config{
		Endpoint:        "http://localhost:9000",
		Region:          "", // Missing region
		Bucket:          "test-bucket",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		UsePathStyle:    true,
	}

	ctx := context.Background()
	_, err := NewStorage(ctx, config)
	if err == nil {
		t.Fatal("Expected error for missing region, got nil")
	}
}

// TestNewStorageMissingCredentials tests error handling for missing credentials
func TestNewStorageMissingCredentials(t *testing.T) {
	config := S3Config{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		Bucket:          "test-bucket",
		AccessKeyID:     "", // Missing credentials
		SecretAccessKey: "",
		UsePathStyle:    true,
	}

	ctx := context.Background()
	_, err := NewStorage(ctx, config)
	if err == nil {
		t.Fatal("Expected error for missing credentials, got nil")
	}
}

// TestExtensionFromContentType tests content type to extension mapping
func TestExtensionFromContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{"jpeg", "image/jpeg", ".jpg"},
		{"jpg", "image/jpg", ".jpg"},
		{"png", "image/png", ".png"},
		{"gif", "image/gif", ".gif"},
		{"webp", "image/webp", ".webp"},
		{"svg", "image/svg+xml", ".svg"},
		{"bmp", "image/bmp", ".bmp"},
		{"tiff", "image/tiff", ".tiff"},
		{"with charset", "image/jpeg; charset=utf-8", ".jpg"},
		{"unknown", "image/unknown", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extensionFromContentType(tt.contentType)
			if got != tt.want {
				t.Errorf("extensionFromContentType(%q) = %q, want %q", tt.contentType, got, tt.want)
			}
		})
	}
}
