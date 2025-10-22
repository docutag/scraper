package db

import (
	"testing"

	"github.com/zombar/scraper/models"
)

func TestGetImageBySlug(t *testing.T) {
	// Use setupTestDB from db_test.go which sets up in-memory database
	db := setupTestDB(t)
	defer db.Close()

	// Create test image with slug
	testImage := &models.ImageInfo{
		ID:      "test-image-id",
		URL:     "https://example.com/test.jpg",
		AltText: "Test image",
		Summary: "A test image",
		Tags:    []string{"test", "image"},
		Slug:    "test-image-slug",
		Width:   800,
		Height:  600,
	}

	// Save the image
	scrapeID := "test-scrape-id"
	err := db.SaveImage(testImage, scrapeID)
	if err != nil {
		t.Fatalf("Failed to save image: %v", err)
	}

	// Test: Retrieve by slug
	retrieved, err := db.GetImageBySlug("test-image-slug")
	if err != nil {
		t.Fatalf("Failed to get image by slug: %v", err)
	}

	if retrieved == nil {
		t.Fatal("GetImageBySlug returned nil")
	}

	if retrieved.ID != testImage.ID {
		t.Errorf("Expected ID %s, got %s", testImage.ID, retrieved.ID)
	}

	if retrieved.Slug != testImage.Slug {
		t.Errorf("Expected slug %s, got %s", testImage.Slug, retrieved.Slug)
	}

	if retrieved.URL != testImage.URL {
		t.Errorf("Expected URL %s, got %s", testImage.URL, retrieved.URL)
	}

	// Test: Non-existent slug
	nonExistent, err := db.GetImageBySlug("non-existent-slug")
	if err != nil {
		t.Fatalf("Unexpected error for non-existent slug: %v", err)
	}

	if nonExistent != nil {
		t.Error("Expected nil for non-existent slug")
	}
}
