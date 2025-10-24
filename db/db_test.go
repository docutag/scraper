package db

import (
	"os"
	"testing"
	"time"

	"github.com/zombar/scraper/models"
)

func setupTestDB(t *testing.T) *DB {
	// Use in-memory database for tests
	config := Config{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	return db
}

func TestNewDatabase(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if db.conn == nil {
		t.Error("Database connection is nil")
	}
}

func TestMigrations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Check that tables were created
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM scraped_data").Scan(&count)
	if err != nil {
		t.Errorf("Failed to query scraped_data table: %v", err)
	}

	err = db.conn.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Errorf("Failed to query schema_migrations table: %v", err)
	}

	err = db.conn.QueryRow("SELECT COUNT(*) FROM images").Scan(&count)
	if err != nil {
		t.Errorf("Failed to query images table: %v", err)
	}
}

func TestSaveAndGetByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	data := &models.ScrapedData{
		ID:             "test-id-123",
		URL:            "https://example.com",
		Title:          "Test Page",
		Content:        "Test content",
		Images:         []models.ImageInfo{},
		Links:          []string{"https://example.com/link1"},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.5,
		Metadata: models.PageMetadata{
			Description: "Test description",
		},
	}

	// Save data
	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Retrieve data
	retrieved, err := db.GetByID("test-id-123")
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved data is nil")
	}

	if retrieved.ID != data.ID {
		t.Errorf("ID mismatch: got %s, want %s", retrieved.ID, data.ID)
	}

	if retrieved.URL != data.URL {
		t.Errorf("URL mismatch: got %s, want %s", retrieved.URL, data.URL)
	}

	if retrieved.Title != data.Title {
		t.Errorf("Title mismatch: got %s, want %s", retrieved.Title, data.Title)
	}
}

func TestGetByURL(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	data := &models.ScrapedData{
		ID:             "test-id-456",
		URL:            "https://example.com/test",
		Title:          "Test Page",
		Content:        "Test content",
		Images:         []models.ImageInfo{},
		Links:          []string{},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	retrieved, err := db.GetByURL("https://example.com/test")
	if err != nil {
		t.Fatalf("Failed to get data by URL: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved data is nil")
	}

	if retrieved.URL != data.URL {
		t.Errorf("URL mismatch: got %s, want %s", retrieved.URL, data.URL)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	retrieved, err := db.GetByID("nonexistent-id")
	if err != nil {
		t.Fatalf("GetByID returned error: %v", err)
	}

	if retrieved != nil {
		t.Error("Expected nil for nonexistent ID")
	}
}

func TestDeleteByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	data := &models.ScrapedData{
		ID:             "delete-test",
		URL:            "https://example.com/delete",
		Title:          "Delete Test",
		Content:        "Content",
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	// Save data
	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Delete data
	err = db.DeleteByID("delete-test")
	if err != nil {
		t.Fatalf("Failed to delete data: %v", err)
	}

	// Verify deletion
	retrieved, err := db.GetByID("delete-test")
	if err != nil {
		t.Fatalf("GetByID returned error: %v", err)
	}

	if retrieved != nil {
		t.Error("Data was not deleted")
	}
}

func TestDeleteByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.DeleteByID("nonexistent-id")
	if err == nil {
		t.Error("Expected error when deleting nonexistent ID")
	}
}

func TestList(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Save multiple entries
	for i := 0; i < 5; i++ {
		data := &models.ScrapedData{
			ID:             string(rune('a' + i)),
			URL:            "https://example.com/" + string(rune('a'+i)),
			Title:          "Test Article Number " + string(rune('0'+i)),
			Content:        "Content",
			FetchedAt:      time.Now(),
			ProcessingTime: 1.0,
			Slug:           "test-article-number-" + string(rune('0'+i)),
		}
		if err := db.SaveScrapedData(data); err != nil {
			t.Fatalf("Failed to save data: %v", err)
		}
	}

	// List with limit
	results, err := db.List(3, 0)
	if err != nil {
		t.Fatalf("Failed to list data: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// List with offset
	results, err = db.List(10, 2)
	if err != nil {
		t.Fatalf("Failed to list data with offset: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results with offset, got %d", len(results))
	}
}

func TestCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Initial count should be 0
	count, err := db.Count()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected initial count 0, got %d", count)
	}

	// Add entries
	for i := 0; i < 3; i++ {
		data := &models.ScrapedData{
			ID:             string(rune('a' + i)),
			URL:            "https://example.com/" + string(rune('a'+i)),
			Title:          "Test Document " + string(rune('0'+i)),
			Content:        "Content",
			FetchedAt:      time.Now(),
			ProcessingTime: 1.0,
			Slug:           "test-document-" + string(rune('0'+i)),
		}
		if err := db.SaveScrapedData(data); err != nil {
			t.Fatalf("Failed to save data: %v", err)
		}
	}

	// Count should be 3
	count, err = db.Count()
	if err != nil {
		t.Fatalf("Failed to get count: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected count 3, got %d", count)
	}
}

func TestURLExists(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	url := "https://example.com/exists"

	// Check non-existent URL
	exists, err := db.URLExists(url)
	if err != nil {
		t.Fatalf("URLExists returned error: %v", err)
	}

	if exists {
		t.Error("URL should not exist initially")
	}

	// Add data
	data := &models.ScrapedData{
		ID:             "exists-test",
		URL:            url,
		Title:          "Test",
		Content:        "Content",
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	if err := db.SaveScrapedData(data); err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Check existing URL
	exists, err = db.URLExists(url)
	if err != nil {
		t.Fatalf("URLExists returned error: %v", err)
	}

	if !exists {
		t.Error("URL should exist after saving")
	}
}

func TestUpsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	url := "https://example.com/upsert"

	// Initial save
	data1 := &models.ScrapedData{
		ID:             "upsert-1",
		URL:            url,
		Title:          "Original Title",
		Content:        "Original content",
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	if err := db.SaveScrapedData(data1); err != nil {
		t.Fatalf("Failed to save initial data: %v", err)
	}

	// Update with same URL, different ID
	data2 := &models.ScrapedData{
		ID:             "upsert-2",
		URL:            url,
		Title:          "Updated Title",
		Content:        "Updated content",
		FetchedAt:      time.Now(),
		ProcessingTime: 2.0,
	}

	if err := db.SaveScrapedData(data2); err != nil {
		t.Fatalf("Failed to update data: %v", err)
	}

	// Retrieve and verify it was updated
	retrieved, err := db.GetByURL(url)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if retrieved.ID != "upsert-2" {
		t.Errorf("Expected ID 'upsert-2', got %s", retrieved.ID)
	}

	if retrieved.Title != "Updated Title" {
		t.Errorf("Expected title 'Updated Title', got %s", retrieved.Title)
	}

	// Verify old ID doesn't exist
	old, err := db.GetByID("upsert-1")
	if err != nil {
		t.Fatalf("GetByID returned error: %v", err)
	}

	if old != nil {
		t.Error("Old ID should not exist after upsert")
	}
}

func TestFileDatabase(t *testing.T) {
	// Test with actual file database
	dbPath := "test-scraper.db"
	defer os.Remove(dbPath)

	config := Config{
		Driver: "sqlite",
		DSN:    dbPath,
	}

	db, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create file database: %v", err)
	}

	// Save data
	data := &models.ScrapedData{
		ID:             "file-test",
		URL:            "https://example.com/file",
		Title:          "File Test",
		Content:        "Content",
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	if err := db.SaveScrapedData(data); err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	db.Close()

	// Reopen and verify data persisted
	db, err = New(config)
	if err != nil {
		t.Fatalf("Failed to reopen database: %v", err)
	}
	defer db.Close()

	retrieved, err := db.GetByID("file-test")
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Data was not persisted")
	}

	if retrieved.Title != "File Test" {
		t.Errorf("Title mismatch: got %s, want File Test", retrieved.Title)
	}
}

func TestSaveAndGetImages(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create scraped data with images
	data := &models.ScrapedData{
		ID:             "scrape-with-images",
		URL:            "https://example.com/images",
		Title:          "Page with Images",
		Content:        "Content",
		Images: []models.ImageInfo{
			{
				ID:         "img-1",
				URL:        "https://example.com/image1.jpg",
				AltText:    "Test image 1",
				Summary:    "A test image",
				Tags:       []string{"test", "example", "photo"},
				Base64Data: "base64data1",
			},
			{
				ID:         "img-2",
				URL:        "https://example.com/image2.jpg",
				AltText:    "Test image 2",
				Summary:    "Another test image",
				Tags:       []string{"illustration", "diagram"},
				Base64Data: "base64data2",
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	// Save data (should also save images)
	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Get image by ID
	img1, err := db.GetImageByID("img-1")
	if err != nil {
		t.Fatalf("Failed to get image by ID: %v", err)
	}

	if img1 == nil {
		t.Fatal("Image not found")
	}

	if img1.URL != "https://example.com/image1.jpg" {
		t.Errorf("Image URL mismatch: got %s", img1.URL)
	}

	if len(img1.Tags) != 3 {
		t.Errorf("Expected 3 tags, got %d", len(img1.Tags))
	}

	// Get images by scrape ID
	images, err := db.GetImagesByScrapeID("scrape-with-images")
	if err != nil {
		t.Fatalf("Failed to get images by scrape ID: %v", err)
	}

	if len(images) != 2 {
		t.Errorf("Expected 2 images, got %d", len(images))
	}
}

func TestSearchImagesByTags(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create scraped data with various images
	data := &models.ScrapedData{
		ID:      "scrape-for-search",
		URL:     "https://example.com/search",
		Title:   "Search Test",
		Content: "Content",
		Images: []models.ImageInfo{
			{
				ID:      "img-cat",
				URL:     "https://example.com/cat.jpg",
				AltText: "Cat photo",
				Tags:    []string{"cat", "animal", "pet"},
			},
			{
				ID:      "img-dog",
				URL:     "https://example.com/dog.jpg",
				AltText: "Dog photo",
				Tags:    []string{"dog", "animal", "pet"},
			},
			{
				ID:      "img-car",
				URL:     "https://example.com/car.jpg",
				AltText: "Car photo",
				Tags:    []string{"car", "vehicle", "transportation"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Test exact match
	results, err := db.SearchImagesByTags([]string{"cat"})
	if err != nil {
		t.Fatalf("Failed to search images: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result for 'cat', got %d", len(results))
	}

	// Test fuzzy match (should match both cat and car due to substring)
	results, err = db.SearchImagesByTags([]string{"ca"})
	if err != nil {
		t.Fatalf("Failed to search images: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results for fuzzy 'ca', got %d", len(results))
	}

	// Test multiple tags
	results, err = db.SearchImagesByTags([]string{"animal", "vehicle"})
	if err != nil {
		t.Fatalf("Failed to search images: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results for multiple tags, got %d", len(results))
	}

	// Test case-insensitive search
	results, err = db.SearchImagesByTags([]string{"CAT"})
	if err != nil {
		t.Fatalf("Failed to search images: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result for case-insensitive 'CAT', got %d", len(results))
	}

	// Test empty tags
	results, err = db.SearchImagesByTags([]string{})
	if err != nil {
		t.Fatalf("Failed to search with empty tags: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results for empty tags, got %d", len(results))
	}
}

func TestImageCascadeDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create scraped data with images
	data := &models.ScrapedData{
		ID:      "cascade-test",
		URL:     "https://example.com/cascade",
		Title:   "Cascade Test",
		Content: "Content",
		Images: []models.ImageInfo{
			{
				ID:      "cascade-img",
				URL:     "https://example.com/cascade.jpg",
				AltText: "Cascade test image",
				Tags:    []string{"test"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Verify image exists
	img, err := db.GetImageByID("cascade-img")
	if err != nil {
		t.Fatalf("Failed to get image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should exist")
	}

	// Delete scraped data
	err = db.DeleteByID("cascade-test")
	if err != nil {
		t.Fatalf("Failed to delete scraped data: %v", err)
	}

	// Verify image was also deleted (cascade)
	img, err = db.GetImageByID("cascade-img")
	if err != nil {
		t.Fatalf("Failed to get image after cascade: %v", err)
	}
	if img != nil {
		t.Error("Image should have been deleted via cascade")
	}
}

func TestTombstoneImageByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a scraped data with an image
	data := &models.ScrapedData{
		ID:      "tombstone-test",
		URL:     "https://example.com/tombstone",
		Title:   "Tombstone Test",
		Content: "Test content",
		Images: []models.ImageInfo{
			{
				ID:      "tombstone-img",
				URL:     "https://example.com/tombstone.jpg",
				AltText: "Tombstone test image",
				Tags:    []string{"test"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Verify image exists without tombstone
	img, err := db.GetImageByID("tombstone-img")
	if err != nil {
		t.Fatalf("Failed to get image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should exist")
	}
	if img.TombstoneDatetime != nil {
		t.Error("Image should not have tombstone_datetime initially")
	}

	// Tombstone the image
	err = db.TombstoneImageByID("tombstone-img")
	if err != nil {
		t.Fatalf("Failed to tombstone image: %v", err)
	}

	// Verify image now has tombstone_datetime
	img, err = db.GetImageByID("tombstone-img")
	if err != nil {
		t.Fatalf("Failed to get tombstoned image: %v", err)
	}
	if img == nil {
		t.Fatal("Tombstoned image should still exist")
	}
	if img.TombstoneDatetime == nil {
		t.Error("Image should have tombstone_datetime after tombstoning")
	}
	if time.Since(*img.TombstoneDatetime) > time.Minute {
		t.Error("Tombstone datetime should be recent")
	}
}

func TestTombstoneImageByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.TombstoneImageByID("non-existent-img")
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
	if err.Error() != "no image found with id: non-existent-img" {
		t.Errorf("Expected 'no image found' error, got: %v", err)
	}
}

func TestUntombstoneImageByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a scraped data with an image
	data := &models.ScrapedData{
		ID:      "untombstone-test",
		URL:     "https://example.com/untombstone",
		Title:   "Untombstone Test",
		Content: "Test content",
		Images: []models.ImageInfo{
			{
				ID:      "untombstone-img",
				URL:     "https://example.com/untombstone.jpg",
				AltText: "Untombstone test image",
				Tags:    []string{"test"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Tombstone the image first
	err = db.TombstoneImageByID("untombstone-img")
	if err != nil {
		t.Fatalf("Failed to tombstone image: %v", err)
	}

	// Verify it has tombstone_datetime
	img, err := db.GetImageByID("untombstone-img")
	if err != nil {
		t.Fatalf("Failed to get image: %v", err)
	}
	if img.TombstoneDatetime == nil {
		t.Fatal("Image should have tombstone_datetime")
	}

	// Untombstone the image
	err = db.UntombstoneImageByID("untombstone-img")
	if err != nil {
		t.Fatalf("Failed to untombstone image: %v", err)
	}

	// Verify tombstone_datetime is removed
	img, err = db.GetImageByID("untombstone-img")
	if err != nil {
		t.Fatalf("Failed to get untombstoned image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should still exist after untombstone")
	}
	if img.TombstoneDatetime != nil {
		t.Error("Image should not have tombstone_datetime after untombstoning")
	}
}

func TestUntombstoneImageByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.UntombstoneImageByID("non-existent-img")
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
	if err.Error() != "no image found with id: non-existent-img" {
		t.Errorf("Expected 'no image found' error, got: %v", err)
	}
}

func TestDeleteImageByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a scraped data with an image
	data := &models.ScrapedData{
		ID:      "delete-img-test",
		URL:     "https://example.com/deleteimg",
		Title:   "Delete Image Test",
		Content: "Test content",
		Images: []models.ImageInfo{
			{
				ID:      "delete-img",
				URL:     "https://example.com/delete.jpg",
				AltText: "Delete test image",
				Tags:    []string{"test"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Verify image exists
	img, err := db.GetImageByID("delete-img")
	if err != nil {
		t.Fatalf("Failed to get image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should exist")
	}

	// Delete the image
	err = db.DeleteImageByID("delete-img")
	if err != nil {
		t.Fatalf("Failed to delete image: %v", err)
	}

	// Verify image no longer exists
	img, err = db.GetImageByID("delete-img")
	if err != nil {
		t.Fatalf("Failed to get deleted image: %v", err)
	}
	if img != nil {
		t.Error("Image should not exist after deletion")
	}
}

func TestDeleteImageByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.DeleteImageByID("non-existent-img")
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
	if err.Error() != "no image found with id: non-existent-img" {
		t.Errorf("Expected 'no image found' error, got: %v", err)
	}
}

func TestUpdateImageTags(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a scraped data with an image
	data := &models.ScrapedData{
		ID:      "update-tags-test",
		URL:     "https://example.com/updatetags",
		Title:   "Update Tags Test",
		Content: "Test content",
		Images: []models.ImageInfo{
			{
				ID:      "update-tags-img",
				URL:     "https://example.com/image.jpg",
				AltText: "Test image",
				Tags:    []string{"initial", "tags"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Verify initial tags
	img, err := db.GetImageByID("update-tags-img")
	if err != nil {
		t.Fatalf("Failed to get image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should exist")
	}
	if len(img.Tags) != 2 {
		t.Errorf("Expected 2 initial tags, got %d", len(img.Tags))
	}

	// Update tags
	newTags := []string{"updated", "tags", "new"}
	err = db.UpdateImageTags("update-tags-img", newTags)
	if err != nil {
		t.Fatalf("Failed to update image tags: %v", err)
	}

	// Verify tags were updated
	img, err = db.GetImageByID("update-tags-img")
	if err != nil {
		t.Fatalf("Failed to get updated image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should still exist")
	}
	if len(img.Tags) != 3 {
		t.Errorf("Expected 3 updated tags, got %d", len(img.Tags))
	}

	for i, expectedTag := range newTags {
		if img.Tags[i] != expectedTag {
			t.Errorf("Expected tag %s at position %d, got %s", expectedTag, i, img.Tags[i])
		}
	}
}

func TestUpdateImageTagsNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.UpdateImageTags("non-existent-img", []string{"test"})
	if err == nil {
		t.Error("Expected error for non-existent image")
	}
	if err.Error() != "image not found" {
		t.Errorf("Expected 'image not found' error, got: %v", err)
	}
}

func TestUpdateImageTagsEmptyList(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create a scraped data with an image
	data := &models.ScrapedData{
		ID:      "empty-tags-test",
		URL:     "https://example.com/emptytags",
		Title:   "Empty Tags Test",
		Content: "Test content",
		Images: []models.ImageInfo{
			{
				ID:      "empty-tags-img",
				URL:     "https://example.com/image.jpg",
				AltText: "Test image",
				Tags:    []string{"tag1", "tag2"},
			},
		},
		FetchedAt:      time.Now(),
		ProcessingTime: 1.0,
	}

	err := db.SaveScrapedData(data)
	if err != nil {
		t.Fatalf("Failed to save data: %v", err)
	}

	// Update to empty tags
	err = db.UpdateImageTags("empty-tags-img", []string{})
	if err != nil {
		t.Fatalf("Failed to update image tags to empty: %v", err)
	}

	// Verify tags were cleared
	img, err := db.GetImageByID("empty-tags-img")
	if err != nil {
		t.Fatalf("Failed to get updated image: %v", err)
	}
	if img == nil {
		t.Fatal("Image should still exist")
	}
	if len(img.Tags) != 0 {
		t.Errorf("Expected 0 tags after clearing, got %d", len(img.Tags))
	}
}
