package db

import (
	"testing"
	"time"

	"github.com/zombar/scraper/models"
)

// TestTombstoneImagePersistence verifies that tombstone_datetime persists through various query paths
func TestTombstoneImagePersistence(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create test data
	data := &models.ScrapedData{
		ID:      "persist-test",
		URL:     "https://example.com/persist",
		Title:   "Persistence Test",
		Content: "Test content",
		Images: []models.ImageInfo{
			{
				ID:      "persist-img",
				URL:     "https://example.com/persist.jpg",
				AltText: "Test image",
				Tags:    []string{"test", "persist"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	// Save the data
	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Tombstone the image
	err = db.TombstoneImageByID("persist-img")
	if err != nil {
		t.Fatalf("Failed to tombstone image: %v", err)
	}

	// Test 1: GetImageByID should return tombstone_datetime
	t.Run("GetImageByID", func(t *testing.T) {
		img, err := db.GetImageByID("persist-img")
		if err != nil {
			t.Fatalf("Failed to get image: %v", err)
		}
		if img == nil {
			t.Fatal("Image not found")
		}
		if img.TombstoneDatetime == nil {
			t.Error("TombstoneDatetime should be set after GetImageByID")
		}
	})

	// Test 2: SearchImagesByTags should return tombstone_datetime
	t.Run("SearchImagesByTags", func(t *testing.T) {
		images, err := db.SearchImagesByTags([]string{"test"})
		if err != nil {
			t.Fatalf("Failed to search images: %v", err)
		}
		if len(images) == 0 {
			t.Fatal("No images found in search")
		}

		found := false
		for _, img := range images {
			if img.ID == "persist-img" {
				found = true
				if img.TombstoneDatetime == nil {
					t.Error("TombstoneDatetime should be set in search results")
				}
				break
			}
		}
		if !found {
			t.Error("Tombstoned image not found in search results")
		}
	})

	// Test 3: GetImagesByScrapeID should return tombstone_datetime
	t.Run("GetImagesByScrapeID", func(t *testing.T) {
		images, err := db.GetImagesByScrapeID("persist-test")
		if err != nil {
			t.Fatalf("Failed to get images by scrape ID: %v", err)
		}
		if len(images) == 0 {
			t.Fatal("No images found for scrape ID")
		}
		if images[0].TombstoneDatetime == nil {
			t.Error("TombstoneDatetime should be set when querying by scrape ID")
		}
	})
}
