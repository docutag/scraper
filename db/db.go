package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver

	"github.com/zombar/scraper/models"
)

// DB wraps the database connection and provides data access methods
type DB struct {
	conn *sql.DB
}

// Config contains database configuration
type Config struct {
	DSN string // PostgreSQL connection string
}

// New creates a new database connection
func New(config Config) (*DB, error) {
	conn, err := sql.Open("postgres", config.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	db := &DB{conn: conn}

	// Run PostgreSQL migrations
	if err := Migrate(conn); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// DB returns the underlying database connection for metrics collection
func (db *DB) DB() *sql.DB {
	return db.conn
}

// SaveScrapedData saves scraped data to the database
func (db *DB) SaveScrapedData(data *models.ScrapedData) error {
	// Begin transaction to save both scraped data and images atomically
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Serialize the data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Insert or replace scraped data
	query := `
		INSERT INTO scraped_data (id, url, data, slug, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(url) DO UPDATE SET
			id = excluded.id,
			data = excluded.data,
			slug = excluded.slug,
			updated_at = excluded.updated_at
	`

	_, err = tx.Exec(
		query,
		data.ID,
		data.URL,
		string(jsonData),
		data.Slug,
		data.FetchedAt,
		time.Now(),
	)

	if err != nil {
		return fmt.Errorf("failed to save data: %w", err)
	}

	// Delete old images for this scrape_id (if re-scraping)
	_, err = tx.Exec("DELETE FROM images WHERE scrape_id = $1", data.ID)
	if err != nil {
		return fmt.Errorf("failed to delete old images: %w", err)
	}

	// Save images to separate table
	for _, image := range data.Images {
		if image.ID == "" {
			// Skip images without IDs (shouldn't happen, but be defensive)
			continue
		}

		tagsJSON, err := json.Marshal(image.Tags)
		if err != nil {
			return fmt.Errorf("failed to marshal image tags: %w", err)
		}

		// Serialize EXIF data to JSON if present
		var exifJSON []byte
		if image.EXIF != nil {
			exifJSON, err = json.Marshal(image.EXIF)
			if err != nil {
				return fmt.Errorf("failed to marshal EXIF: %w", err)
			}
		}

		imageQuery := `
			INSERT INTO images (id, scrape_id, url, alt_text, summary, tags, base64_data, file_path, slug, width, height, file_size_bytes, content_type, exif_data, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		`

		_, err = tx.Exec(
			imageQuery,
			image.ID,
			data.ID,
			image.URL,
			image.AltText,
			image.Summary,
			string(tagsJSON),
			image.Base64Data,
			image.FilePath,
			image.Slug,
			image.Width,
			image.Height,
			image.FileSizeBytes,
			image.ContentType,
			string(exifJSON),
			time.Now(),
			time.Now(),
		)

		if err != nil {
			return fmt.Errorf("failed to save image %s: %w", image.ID, err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetByID retrieves scraped data by ID
func (db *DB) GetByID(id string) (*models.ScrapedData, error) {
	var jsonData string
	query := "SELECT data FROM scraped_data WHERE id = $1"

	err := db.conn.QueryRow(query, id).Scan(&jsonData)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}

	var data models.ScrapedData
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return &data, nil
}

// GetByURL retrieves scraped data by URL
func (db *DB) GetByURL(url string) (*models.ScrapedData, error) {
	var jsonData string
	query := "SELECT data FROM scraped_data WHERE url = $1"

	err := db.conn.QueryRow(query, url).Scan(&jsonData)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}

	var data models.ScrapedData
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return &data, nil
}

// DeleteByID deletes scraped data by ID
func (db *DB) DeleteByID(id string) error {
	result, err := db.conn.Exec("DELETE FROM scraped_data WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete data: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no data found with id: %s", id)
	}

	return nil
}

// List returns all scraped data with optional pagination
func (db *DB) List(limit, offset int) ([]*models.ScrapedData, error) {
	query := `
		SELECT data FROM scraped_data
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query data: %w", err)
	}
	defer rows.Close()

	var results []*models.ScrapedData
	for rows.Next() {
		var jsonData string
		if err := rows.Scan(&jsonData); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var data models.ScrapedData
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			return nil, fmt.Errorf("failed to unmarshal data: %w", err)
		}

		results = append(results, &data)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// Count returns the total count of scraped data entries
func (db *DB) Count() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM scraped_data").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count data: %w", err)
	}
	return count, nil
}

// URLExists checks if a URL already exists in the database
func (db *DB) URLExists(url string) (bool, error) {
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM scraped_data WHERE url = $1)"
	err := db.conn.QueryRow(query, url).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check URL existence: %w", err)
	}
	return exists, nil
}

// SaveImage saves an image to the database
func (db *DB) SaveImage(image *models.ImageInfo, scrapeID string) error {
	tagsJSON, err := json.Marshal(image.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}

	// Serialize EXIF data to JSON if present
	var exifJSON []byte
	if image.EXIF != nil {
		exifJSON, err = json.Marshal(image.EXIF)
		if err != nil {
			return fmt.Errorf("failed to marshal EXIF: %w", err)
		}
	}

	query := `
		INSERT INTO images (id, scrape_id, url, alt_text, summary, tags, extracted_text, base64_data, file_path, slug, width, height, file_size_bytes, content_type, exif_data, relevance_score, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`

	_, err = db.conn.Exec(
		query,
		image.ID,
		scrapeID,
		image.URL,
		image.AltText,
		image.Summary,
		string(tagsJSON),
		image.ExtractedText,
		image.Base64Data,
		image.FilePath,
		image.Slug,
		image.Width,
		image.Height,
		image.FileSizeBytes,
		image.ContentType,
		string(exifJSON),
		image.RelevanceScore,
		time.Now(),
		time.Now(),
	)

	if err != nil {
		return fmt.Errorf("failed to save image: %w", err)
	}

	return nil
}

// GetImageByID retrieves an image by its ID
func (db *DB) GetImageByID(id string) (*models.ImageInfo, error) {
	var (
		imageID           string
		url               string
		altText           string
		summary           string
		tagsJSON          string
		extractedText     sql.NullString
		base64Data        string
		filePath          sql.NullString
		slugVal           sql.NullString
		scrapeID          string
		tombstoneDatetime sql.NullTime
		width             sql.NullInt64
		height            sql.NullInt64
		fileSizeBytes     sql.NullInt64
		contentType       sql.NullString
		exifJSON          sql.NullString
		relevanceScore    sql.NullFloat64
	)

	query := "SELECT id, url, alt_text, summary, tags, extracted_text, base64_data, file_path, slug, scrape_id, tombstone_datetime, width, height, file_size_bytes, content_type, exif_data, relevance_score FROM images WHERE id = $1"
	err := db.conn.QueryRow(query, id).Scan(&imageID, &url, &altText, &summary, &tagsJSON, &extractedText, &base64Data, &filePath, &slugVal, &scrapeID, &tombstoneDatetime, &width, &height, &fileSizeBytes, &contentType, &exifJSON, &relevanceScore)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query image: %w", err)
	}

	var tags []string
	if tagsJSON != "" && tagsJSON != "null" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
		}
	}

	image := &models.ImageInfo{
		ID:          imageID,
		URL:         url,
		AltText:     altText,
		Summary:     summary,
		Tags:        tags,
		Base64Data:  base64Data,
		ScraperUUID: scrapeID,
	}
	if extractedText.Valid {
		image.ExtractedText = extractedText.String
	}
	if filePath.Valid {
		image.FilePath = filePath.String
	}
	if slugVal.Valid {
		image.Slug = slugVal.String
	}
	if tombstoneDatetime.Valid {
		image.TombstoneDatetime = &tombstoneDatetime.Time
	}
	if width.Valid {
		image.Width = int(width.Int64)
	}
	if height.Valid {
		image.Height = int(height.Int64)
	}
	if fileSizeBytes.Valid {
		image.FileSizeBytes = fileSizeBytes.Int64
	}
	if contentType.Valid {
		image.ContentType = contentType.String
	}
	if exifJSON.Valid && exifJSON.String != "" && exifJSON.String != "null" {
		var exif models.EXIFData
		if err := json.Unmarshal([]byte(exifJSON.String), &exif); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EXIF: %w", err)
		}
		image.EXIF = &exif
	}
	if relevanceScore.Valid {
		image.RelevanceScore = relevanceScore.Float64
	}

	return image, nil
}

// GetImageByURL retrieves an image by its source URL
func (db *DB) GetImageByURL(url string) (*models.ImageInfo, error) {
	var (
		imageID        string
		imageURL       string
		altText        string
		summary        string
		tagsJSON       string
		extractedText  sql.NullString
		base64Data     string
		filePath       sql.NullString
		slugVal        sql.NullString
		scrapeID       string
		width          sql.NullInt64
		height         sql.NullInt64
		fileSizeBytes  sql.NullInt64
		contentType    sql.NullString
		exifJSON       sql.NullString
		relevanceScore sql.NullFloat64
	)

	query := "SELECT id, url, alt_text, summary, tags, extracted_text, base64_data, file_path, slug, scrape_id, width, height, file_size_bytes, content_type, exif_data, relevance_score FROM images WHERE url = $1 LIMIT 1"
	err := db.conn.QueryRow(query, url).Scan(&imageID, &imageURL, &altText, &summary, &tagsJSON, &extractedText, &base64Data, &filePath, &slugVal, &scrapeID, &width, &height, &fileSizeBytes, &contentType, &exifJSON, &relevanceScore)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query image by URL: %w", err)
	}

	var tags []string
	if tagsJSON != "" && tagsJSON != "null" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
		}
	}

	image := &models.ImageInfo{
		ID:          imageID,
		URL:         imageURL,
		AltText:     altText,
		Summary:     summary,
		Tags:        tags,
		Base64Data:  base64Data,
		ScraperUUID: scrapeID,
	}
	if filePath.Valid {
		image.FilePath = filePath.String
	}
	if slugVal.Valid {
		image.Slug = slugVal.String
	}
	if width.Valid {
		image.Width = int(width.Int64)
	}
	if height.Valid {
		image.Height = int(height.Int64)
	}
	if fileSizeBytes.Valid {
		image.FileSizeBytes = fileSizeBytes.Int64
	}
	if contentType.Valid {
		image.ContentType = contentType.String
	}
	if exifJSON.Valid && exifJSON.String != "" && exifJSON.String != "null" {
		var exif models.EXIFData
		if err := json.Unmarshal([]byte(exifJSON.String), &exif); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EXIF: %w", err)
		}
		image.EXIF = &exif
	}
	if relevanceScore.Valid {
		image.RelevanceScore = relevanceScore.Float64
	}

	return image, nil
}

// GetImageBySlug retrieves an image by its slug
func (db *DB) GetImageBySlug(slug string) (*models.ImageInfo, error) {
	var (
		imageID           string
		url               string
		altText           string
		summary           string
		tagsJSON          string
		extractedText     sql.NullString
		base64Data        string
		filePath          sql.NullString
		slugVal           sql.NullString
		scrapeID          string
		tombstoneDatetime sql.NullTime
		width             sql.NullInt64
		height            sql.NullInt64
		fileSizeBytes     sql.NullInt64
		contentType       sql.NullString
		exifJSON          sql.NullString
		relevanceScore    sql.NullFloat64
	)

	query := "SELECT id, url, alt_text, summary, tags, extracted_text, base64_data, file_path, slug, scrape_id, tombstone_datetime, width, height, file_size_bytes, content_type, exif_data, relevance_score FROM images WHERE slug = $1 LIMIT 1"
	err := db.conn.QueryRow(query, slug).Scan(&imageID, &url, &altText, &summary, &tagsJSON, &extractedText, &base64Data, &filePath, &slugVal, &scrapeID, &tombstoneDatetime, &width, &height, &fileSizeBytes, &contentType, &exifJSON, &relevanceScore)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query image by slug: %w", err)
	}

	var tags []string
	if tagsJSON != "" && tagsJSON != "null" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
		}
	}

	image := &models.ImageInfo{
		ID:          imageID,
		URL:         url,
		AltText:     altText,
		Summary:     summary,
		Tags:        tags,
		Base64Data:  base64Data,
		ScraperUUID: scrapeID,
	}
	if extractedText.Valid {
		image.ExtractedText = extractedText.String
	}
	if filePath.Valid {
		image.FilePath = filePath.String
	}
	if slugVal.Valid {
		image.Slug = slugVal.String
	}
	if tombstoneDatetime.Valid {
		image.TombstoneDatetime = &tombstoneDatetime.Time
	}
	if width.Valid {
		image.Width = int(width.Int64)
	}
	if height.Valid {
		image.Height = int(height.Int64)
	}
	if fileSizeBytes.Valid {
		image.FileSizeBytes = fileSizeBytes.Int64
	}
	if contentType.Valid {
		image.ContentType = contentType.String
	}
	if exifJSON.Valid && exifJSON.String != "" && exifJSON.String != "null" {
		var exif models.EXIFData
		if err := json.Unmarshal([]byte(exifJSON.String), &exif); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EXIF: %w", err)
		}
		image.EXIF = &exif
	}
	if relevanceScore.Valid {
		image.RelevanceScore = relevanceScore.Float64
	}

	return image, nil
}

// SearchImagesByTags searches for images by tags using fuzzy matching
// Returns images that contain any of the search tags (case-insensitive)
func (db *DB) SearchImagesByTags(searchTags []string) ([]*models.ImageInfo, error) {
	if len(searchTags) == 0 {
		return []*models.ImageInfo{}, nil
	}

	// Query all images
	query := "SELECT id, url, alt_text, summary, tags, base64_data, scrape_id, tombstone_datetime, width, height, file_size_bytes, content_type, exif_data FROM images ORDER BY created_at DESC"
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query images: %w", err)
	}
	defer rows.Close()

	results := []*models.ImageInfo{}
	for rows.Next() {
		var (
			imageID           string
			url               string
			altText           string
			summary           string
			tagsJSON          string
			base64Data        string
			scrapeID          string
			tombstoneDatetime sql.NullTime
			width             sql.NullInt64
			height            sql.NullInt64
			fileSizeBytes     sql.NullInt64
			contentType       sql.NullString
			exifJSON          sql.NullString
		)

		if err := rows.Scan(&imageID, &url, &altText, &summary, &tagsJSON, &base64Data, &scrapeID, &tombstoneDatetime, &width, &height, &fileSizeBytes, &contentType, &exifJSON); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var tags []string
		if tagsJSON != "" && tagsJSON != "null" {
			if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
				continue // Skip malformed entries
			}
		}

		// Fuzzy match: check if any search tag is contained in any image tag (case-insensitive)
		matched := false
		for _, searchTag := range searchTags {
			searchTagLower := strings.ToLower(searchTag)
			for _, tag := range tags {
				tagLower := strings.ToLower(tag)
				if strings.Contains(tagLower, searchTagLower) || strings.Contains(searchTagLower, tagLower) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}

		if matched {
			image := &models.ImageInfo{
				ID:          imageID,
				URL:         url,
				AltText:     altText,
				Summary:     summary,
				Tags:        tags,
				Base64Data:  base64Data,
				ScraperUUID: scrapeID,
			}
			if tombstoneDatetime.Valid {
				image.TombstoneDatetime = &tombstoneDatetime.Time
			}
			if width.Valid {
				image.Width = int(width.Int64)
			}
			if height.Valid {
				image.Height = int(height.Int64)
			}
			if fileSizeBytes.Valid {
				image.FileSizeBytes = fileSizeBytes.Int64
			}
			if contentType.Valid {
				image.ContentType = contentType.String
			}
			if exifJSON.Valid && exifJSON.String != "" && exifJSON.String != "null" {
				var exif models.EXIFData
				if err := json.Unmarshal([]byte(exifJSON.String), &exif); err == nil {
					image.EXIF = &exif
				}
			}
			results = append(results, image)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// GetImagesByScrapeID retrieves all images associated with a scrape ID
func (db *DB) GetImagesByScrapeID(scrapeID string) ([]*models.ImageInfo, error) {
	query := "SELECT id, url, alt_text, summary, tags, extracted_text, base64_data, scrape_id, tombstone_datetime, width, height, file_size_bytes, content_type, exif_data FROM images WHERE scrape_id = $1 ORDER BY created_at"
	rows, err := db.conn.Query(query, scrapeID)
	if err != nil {
		return nil, fmt.Errorf("failed to query images: %w", err)
	}
	defer rows.Close()

	var results []*models.ImageInfo
	for rows.Next() {
		var (
			imageID           string
			url               string
			altText           string
			summary           string
			tagsJSON          string
			extractedText     sql.NullString
			base64Data        string
			imageScrapeID     string
			tombstoneDatetime sql.NullTime
			width             sql.NullInt64
			height            sql.NullInt64
			fileSizeBytes     sql.NullInt64
			contentType       sql.NullString
			exifJSON          sql.NullString
		)

		if err := rows.Scan(&imageID, &url, &altText, &summary, &tagsJSON, &extractedText, &base64Data, &imageScrapeID, &tombstoneDatetime, &width, &height, &fileSizeBytes, &contentType, &exifJSON); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var tags []string
		if tagsJSON != "" && tagsJSON != "null" {
			if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
			}
		}

		image := &models.ImageInfo{
			ID:          imageID,
			URL:         url,
			AltText:     altText,
			Summary:     summary,
			Tags:        tags,
			Base64Data:  base64Data,
			ScraperUUID: imageScrapeID,
		}
		if extractedText.Valid {
			image.ExtractedText = extractedText.String
		}
		if tombstoneDatetime.Valid {
			image.TombstoneDatetime = &tombstoneDatetime.Time
		}
		if width.Valid {
			image.Width = int(width.Int64)
		}
		if height.Valid {
			image.Height = int(height.Int64)
		}
		if fileSizeBytes.Valid {
			image.FileSizeBytes = fileSizeBytes.Int64
		}
		if contentType.Valid {
			image.ContentType = contentType.String
		}
		if exifJSON.Valid && exifJSON.String != "" && exifJSON.String != "null" {
			var exif models.EXIFData
			if err := json.Unmarshal([]byte(exifJSON.String), &exif); err != nil {
				return nil, fmt.Errorf("failed to unmarshal EXIF: %w", err)
			}
			image.EXIF = &exif
		}

		results = append(results, image)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// DeleteImageByID deletes an image by its ID
func (db *DB) DeleteImageByID(id string) error {
	result, err := db.conn.Exec("DELETE FROM images WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no image found with id: %s", id)
	}

	return nil
}

// TombstoneImageByID sets the tombstone_datetime for an image
func (db *DB) TombstoneImageByID(id string) error {
	// Set tombstone date to 90 days from now (same as manual request tombstoning)
	tombstoneTime := time.Now().UTC().Add(90 * 24 * time.Hour)

	result, err := db.conn.Exec(
		"UPDATE images SET tombstone_datetime = $1 WHERE id = $2",
		tombstoneTime,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to tombstone image: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no image found with id: %s", id)
	}

	return nil
}

// UntombstoneImageByID removes the tombstone_datetime for an image
func (db *DB) UntombstoneImageByID(id string) error {
	result, err := db.conn.Exec(
		"UPDATE images SET tombstone_datetime = NULL WHERE id = $1",
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to untombstone image: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("no image found with id: %s", id)
	}

	return nil
}

// UpdateImageTags updates the tags for a specific image
func (db *DB) UpdateImageTags(id string, tags []string) error {
	// Marshal tags to JSON
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}

	// Update tags in database
	result, err := db.conn.Exec("UPDATE images SET tags = $1 WHERE id = $2", string(tagsJSON), id)
	if err != nil {
		return fmt.Errorf("failed to update image tags: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("image not found")
	}

	return nil
}

// ImageStats contains statistics about images
type ImageStats struct {
	TotalStored      int   // Total images including tombstoned
	TotalStorageSize int64 // Total storage size in bytes including tombstoned
}

// GetImageStats returns statistics about images for Prometheus metrics
// Note: This counts ALL images including tombstoned ones for complete accounting.
// Tombstoned images are scheduled for deletion but still consume storage until purged.
func (db *DB) GetImageStats() (*ImageStats, error) {
	stats := &ImageStats{}

	// Count all images (including tombstoned - they still exist until purged)
	countQuery := "SELECT COUNT(*) FROM images"
	err := db.conn.QueryRow(countQuery).Scan(&stats.TotalStored)
	if err != nil {
		return nil, fmt.Errorf("failed to count images: %w", err)
	}

	// Sum total storage size (including tombstoned - they still consume disk space)
	sizeQuery := "SELECT COALESCE(SUM(file_size_bytes), 0) FROM images"
	err = db.conn.QueryRow(sizeQuery).Scan(&stats.TotalStorageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to sum storage size: %w", err)
	}

	return stats, nil
}
