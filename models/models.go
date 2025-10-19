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
	ID         string   `json:"id,omitempty"` // UUID for the image
	URL        string   `json:"url"`
	AltText    string   `json:"alt_text"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Base64Data string   `json:"base64_data,omitempty"` // Base64 encoded image data
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
