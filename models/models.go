package models

import "time"

// ScrapedData represents the complete output of a web scraping operation
type ScrapedData struct {
	ID             string       `json:"id"`
	URL            string       `json:"url"`
	Title          string       `json:"title"`
	Content        string       `json:"content"`
	Images         []ImageInfo  `json:"images"`
	Links          []string     `json:"links"`
	FetchedAt      time.Time    `json:"fetched_at"`
	CreatedAt      time.Time    `json:"created_at"`
	ProcessingTime float64      `json:"processing_time_seconds"`
	Cached         bool         `json:"cached"`
	Metadata       PageMetadata `json:"metadata"`
	Score          *LinkScore   `json:"score,omitempty"` // Quality score for the URL
	Warnings       []string     `json:"warnings,omitempty"` // Non-fatal processing warnings
}

// ImageInfo contains information about an extracted image
type ImageInfo struct {
	ID                 string     `json:"id,omitempty"` // UUID for the image
	URL                string     `json:"url"`
	AltText            string     `json:"alt_text"`
	Summary            string     `json:"summary"`
	Tags               []string   `json:"tags"`
	Base64Data         string     `json:"base64_data,omitempty"` // Base64 encoded image data
	ScraperUUID        string     `json:"scraper_uuid,omitempty"` // UUID of the parent scraped data
	TombstoneDatetime  *time.Time `json:"tombstone_datetime,omitempty"` // When the image was tombstoned
	Width              int        `json:"width,omitempty"`       // Image width in pixels
	Height             int        `json:"height,omitempty"`      // Image height in pixels
	FileSizeBytes      int64      `json:"file_size_bytes,omitempty"` // File size in bytes
	ContentType        string     `json:"content_type,omitempty"` // MIME type (e.g., "image/jpeg")
	EXIF               *EXIFData  `json:"exif,omitempty"`        // EXIF metadata from image file
}

// EXIFData contains EXIF metadata extracted from an image
type EXIFData struct {
	DateTime         string   `json:"date_time,omitempty"`          // When photo was taken (EXIF DateTime)
	DateTimeOriginal string   `json:"date_time_original,omitempty"` // Original date/time (EXIF DateTimeOriginal)
	Make             string   `json:"make,omitempty"`               // Camera manufacturer
	Model            string   `json:"model,omitempty"`              // Camera model
	Copyright        string   `json:"copyright,omitempty"`          // Copyright notice
	Artist           string   `json:"artist,omitempty"`             // Photographer/creator name
	Software         string   `json:"software,omitempty"`           // Software used to process image
	ImageDescription string   `json:"image_description,omitempty"`  // Embedded image description
	Orientation      int      `json:"orientation,omitempty"`        // Image orientation (1-8)
	GPS              *GPSData `json:"gps,omitempty"`                // GPS location data
}

// GPSData contains GPS coordinates from EXIF
type GPSData struct {
	Latitude  float64 `json:"latitude"`            // GPS latitude in decimal degrees
	Longitude float64 `json:"longitude"`           // GPS longitude in decimal degrees
	Altitude  float64 `json:"altitude,omitempty"`  // GPS altitude in meters
}

// PageMetadata contains additional metadata about the scraped page
type PageMetadata struct {
	Description          string                 `json:"description,omitempty"`
	Keywords             []string               `json:"keywords,omitempty"`
	Author               string                 `json:"author,omitempty"`
	PublishedDate        string                 `json:"published_date,omitempty"`
	ExistingImageRefs    []ExistingImageRef     `json:"existing_image_refs,omitempty"` // References to images already in database
}

// ExistingImageRef represents a reference to an existing image that was not re-downloaded
type ExistingImageRef struct {
	ImageID  string `json:"image_id"`  // ID of the existing image
	ImageURL string `json:"image_url"` // URL of the image (for reference)
}

// OllamaRequest represents a request to the Ollama API
type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format,omitempty"`
}

// OllamaResponse represents a response from the Ollama API
type OllamaResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
}

// OllamaVisionRequest represents a vision request to the Ollama API
type OllamaVisionRequest struct {
	Model  string   `json:"model"`
	Prompt string   `json:"prompt"`
	Images []string `json:"images"` // base64 encoded images
	Stream bool     `json:"stream"`
}

// LinkScore represents a scored link with quality assessment
type LinkScore struct {
	URL               string   `json:"url"`
	Score             float64  `json:"score"`              // 0.0 to 1.0, higher is better quality
	Reason            string   `json:"reason"`             // Explanation for the score
	Categories        []string `json:"categories"`         // Detected categories (e.g., "social_media", "spam")
	IsRecommended       bool     `json:"is_recommended"`     // Whether the link is recommended for ingestion
	MaliciousIndicators []string `json:"malicious_indicators,omitempty"` // Any detected malicious patterns
	AIUsed              bool     `json:"ai_used"`            // Whether AI (Ollama) was used for scoring (true) or rule-based fallback (false)
}

// ScoreRequest represents a request to score a URL
type ScoreRequest struct {
	URL string `json:"url"`
}

// ScoreResponse represents a response containing link score
type ScoreResponse struct {
	URL   string    `json:"url"`
	Score LinkScore `json:"score"`
}
