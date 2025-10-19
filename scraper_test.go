package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zombar/scraper/models"
)

func TestNew(t *testing.T) {
	config := DefaultConfig()
	s := New(config, nil)

	if s == nil {
		t.Fatal("Expected scraper to be non-nil")
	}

	if s.httpClient == nil {
		t.Error("Expected httpClient to be non-nil")
	}

	if s.ollamaClient == nil {
		t.Error("Expected ollamaClient to be non-nil")
	}
}

func TestExtractLinks(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock response for both ExtractContent and link filtering
		var req models.OllamaRequest
		json.NewDecoder(r.Body).Decode(&req)

		var response string
		// Check if it's a link filtering request
		if contains(req.Prompt, "link filtering") {
			response = `["https://example.com/article-1", "https://example.com/article-2"]`
		} else {
			response = "Extracted article content"
		}

		resp := models.OllamaResponse{
			Response: response,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head>
	<title>Test Page</title>
</head>
<body>
	<h1>Test Article</h1>
	<p>This is test content.</p>
	<a href="https://example.com/article-1">Article 1</a>
	<a href="https://example.com/article-2">Article 2</a>
	<a href="https://example.com/privacy">Privacy</a>
	<a href="/relative">Relative Link</a>
</body>
</html>
`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:   10 * time.Second,
		OllamaBaseURL: ollamaServer.URL,
		OllamaModel:   "test-model",
	}
	s := New(config, nil)

	ctx := context.Background()
	links, err := s.ExtractLinks(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("ExtractLinks failed: %v", err)
	}

	if len(links) != 2 {
		t.Errorf("Expected 2 sanitized links, got %d", len(links))
	}

	expectedLinks := []string{
		"https://example.com/article-1",
		"https://example.com/article-2",
	}

	for i, link := range links {
		if link != expectedLinks[i] {
			t.Errorf("Link[%d] = %s, want %s", i, link, expectedLinks[i])
		}
	}
}

func TestExtractLinksInvalidURL(t *testing.T) {
	config := DefaultConfig()
	s := New(config, nil)

	ctx := context.Background()

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "invalid scheme",
			url:  "ftp://example.com",
		},
		{
			name: "malformed URL",
			url:  "ht!tp://invalid",
		},
		{
			name: "empty URL",
			url:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.ExtractLinks(ctx, tt.url)
			if err == nil {
				t.Error("Expected error for invalid URL, got nil")
			}
		})
	}
}

func TestExtractLinksHTTPError(t *testing.T) {
	// Create mock web server that returns errors
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer webServer.Close()

	config := DefaultConfig()
	s := New(config, nil)

	ctx := context.Background()
	_, err := s.ExtractLinks(ctx, webServer.URL)

	if err == nil {
		t.Error("Expected error for HTTP 404, got nil")
	}
}

func TestExtractLinksMalformedHTML(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: `[]`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server with malformed HTML
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<html><head><title>Test</title><body><p>Unclosed tags<a href="test"`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:   10 * time.Second,
		OllamaBaseURL: ollamaServer.URL,
		OllamaModel:   "test-model",
	}
	s := New(config, nil)

	ctx := context.Background()
	// Should not panic, should handle gracefully
	links, err := s.ExtractLinks(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("ExtractLinks should handle malformed HTML gracefully: %v", err)
	}

	// Should return empty or minimal links
	if links == nil {
		t.Error("Expected non-nil links slice")
	}
}

func TestExtractLinksContextCancellation(t *testing.T) {
	// Create mock web server with delay
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte("<html><body>Test</body></html>"))
	}))
	defer webServer.Close()

	config := DefaultConfig()
	s := New(config, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := s.ExtractLinks(ctx, webServer.URL)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestExtractLinksSanitizationFallback(t *testing.T) {
	// Create mock Ollama server that fails
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
	<a href="https://example.com/link1">Link 1</a>
	<a href="https://example.com/link2">Link 2</a>
</body>
</html>
`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:   10 * time.Second,
		OllamaBaseURL: ollamaServer.URL,
		OllamaModel:   "test-model",
	}
	s := New(config, nil)

	ctx := context.Background()
	links, err := s.ExtractLinks(ctx, webServer.URL)

	// Should return all raw links when Ollama fails (fallback behavior)
	if err != nil {
		t.Errorf("ExtractLinks should not return error on Ollama failure: %v", err)
	}

	if len(links) != 2 {
		t.Errorf("Expected 2 raw links as fallback, got %d", len(links))
	}

	expectedLinks := []string{
		"https://example.com/link1",
		"https://example.com/link2",
	}

	for i, link := range links {
		if link != expectedLinks[i] {
			t.Errorf("Link[%d] = %s, want %s", i, link, expectedLinks[i])
		}
	}
}

func TestExtractLinksEmptyPage(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: `[]`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server with no links
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Empty</title></head><body><p>No links here</p></body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:   10 * time.Second,
		OllamaBaseURL: ollamaServer.URL,
		OllamaModel:   "test-model",
	}
	s := New(config, nil)

	ctx := context.Background()
	links, err := s.ExtractLinks(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("ExtractLinks failed: %v", err)
	}

	if len(links) != 0 {
		t.Errorf("Expected 0 links from empty page, got %d", len(links))
	}
}

func TestImageProcessing(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string   `json:"model"`
			Prompt string   `json:"prompt"`
			Images []string `json:"images"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		// Check if it's an image analysis request
		if len(req.Images) > 0 {
			resp := models.OllamaResponse{
				Response: `{"summary": "A test image showing a red square on white background", "tags": ["test", "red", "square", "geometric"]}`,
				Done:     true,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		} else {
			resp := models.OllamaResponse{
				Response: "Extracted content",
				Done:     true,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ollamaServer.Close()

	// Create mock image server
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a simple 1x1 red pixel PNG
		imageData := []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
			0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
			0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
			0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d, 0xb4, 0x00, 0x00, 0x00,
			0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(imageData)
	}))
	defer imageServer.Close()

	// Create mock web server with image
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head><title>Test Page with Images</title></head>
<body>
	<h1>Test</h1>
	<img src="` + imageServer.URL + `/test.png" alt="Test image">
</body>
</html>
`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:         10 * time.Second,
		OllamaBaseURL:       ollamaServer.URL,
		OllamaModel:         "test-model",
		EnableImageAnalysis: true,
		MaxImageSizeBytes:   10 * 1024 * 1024,
		ImageTimeout:        5 * time.Second,
	}
	s := New(config, nil)

	ctx := context.Background()
	data, err := s.Scrape(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	if len(data.Images) != 1 {
		t.Fatalf("Expected 1 image, got %d", len(data.Images))
	}

	img := data.Images[0]

	if img.URL != imageServer.URL+"/test.png" {
		t.Errorf("Image URL = %s, want %s", img.URL, imageServer.URL+"/test.png")
	}

	if img.AltText != "Test image" {
		t.Errorf("Alt text = %s, want 'Test image'", img.AltText)
	}

	if img.Summary == "" {
		t.Error("Expected image summary to be populated")
	}

	if len(img.Tags) == 0 {
		t.Error("Expected image tags to be populated")
	}

	// Check image metadata
	if img.Width != 1 {
		t.Errorf("Image width = %d, want 1", img.Width)
	}

	if img.Height != 1 {
		t.Errorf("Image height = %d, want 1", img.Height)
	}

	if img.FileSizeBytes == 0 {
		t.Error("Expected FileSizeBytes to be populated")
	}

	if img.ContentType != "image/png" {
		t.Errorf("Image ContentType = %s, want 'image/png'", img.ContentType)
	}

	t.Logf("Image summary: %s", img.Summary)
	t.Logf("Image tags: %v", img.Tags)
	t.Logf("Image metadata: %dx%d, %d bytes, %s", img.Width, img.Height, img.FileSizeBytes, img.ContentType)
}

func TestImageProcessingDisabled(t *testing.T) {
	// Create mock web server with image
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<img src="https://example.com/image.jpg" alt="Test">
</body>
</html>
`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:         10 * time.Second,
		OllamaBaseURL:       "http://localhost:11434",
		OllamaModel:         "test-model",
		EnableImageAnalysis: false, // Disabled
		MaxImageSizeBytes:   10 * 1024 * 1024,
		ImageTimeout:        5 * time.Second,
	}
	s := New(config, nil)

	ctx := context.Background()
	data, err := s.Scrape(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	if len(data.Images) != 1 {
		t.Fatalf("Expected 1 image, got %d", len(data.Images))
	}

	img := data.Images[0]

	// When disabled, summary and tags should be empty
	if img.Summary != "" {
		t.Errorf("Expected empty summary when image analysis disabled, got: %s", img.Summary)
	}

	if len(img.Tags) != 0 {
		t.Errorf("Expected empty tags when image analysis disabled, got: %v", img.Tags)
	}
}

func TestScoreLinkContent(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: `{"score": 0.8, "reason": "High quality technical article", "categories": ["technical", "education"], "malicious_indicators": []}`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head>
	<title>Technical Article</title>
</head>
<body>
	<h1>Understanding Go Concurrency</h1>
	<p>This is a technical article about Go programming language concurrency patterns.</p>
</body>
</html>
`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:        10 * time.Second,
		OllamaBaseURL:      ollamaServer.URL,
		OllamaModel:        "test-model",
		LinkScoreThreshold: 0.5,
	}
	s := New(config, nil)

	ctx := context.Background()
	score, err := s.ScoreLinkContent(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("ScoreLinkContent failed: %v", err)
	}

	if score.URL != webServer.URL {
		t.Errorf("URL = %s, want %s", score.URL, webServer.URL)
	}

	if score.Score != 0.8 {
		t.Errorf("Score = %f, want 0.8", score.Score)
	}

	if !score.IsRecommended {
		t.Error("Expected IsRecommended to be true for score 0.8 with threshold 0.5")
	}

	if score.Reason != "High quality technical article" {
		t.Errorf("Reason = %s, want 'High quality technical article'", score.Reason)
	}

	if len(score.Categories) != 2 {
		t.Errorf("Expected 2 categories, got %d", len(score.Categories))
	}
}

func TestScoreLinkContentLowScore(t *testing.T) {
	// Create mock Ollama server returning low score
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: `{"score": 0.2, "reason": "Social media platform", "categories": ["social_media"], "malicious_indicators": []}`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Social Media</title></head><body><p>Social platform</p></body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:        10 * time.Second,
		OllamaBaseURL:      ollamaServer.URL,
		OllamaModel:        "test-model",
		LinkScoreThreshold: 0.5,
	}
	s := New(config, nil)

	ctx := context.Background()
	score, err := s.ScoreLinkContent(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("ScoreLinkContent failed: %v", err)
	}

	if score.Score != 0.2 {
		t.Errorf("Score = %f, want 0.2", score.Score)
	}

	if score.IsRecommended {
		t.Error("Expected IsRecommended to be false for score 0.2 with threshold 0.5")
	}

	if len(score.Categories) != 1 || score.Categories[0] != "social-media" {
		t.Errorf("Categories = %v, want ['social-media']", score.Categories)
	}
}

func TestScoreLinkContentMalicious(t *testing.T) {
	// Create mock Ollama server returning malicious indicators
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: `{"score": 0.1, "reason": "Suspected phishing site", "categories": ["malicious"], "malicious_indicators": ["phishing", "suspicious_url"]}`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Suspicious Site</title></head><body><p>Click here to win!</p></body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:        10 * time.Second,
		OllamaBaseURL:      ollamaServer.URL,
		OllamaModel:        "test-model",
		LinkScoreThreshold: 0.5,
	}
	s := New(config, nil)

	ctx := context.Background()
	score, err := s.ScoreLinkContent(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("ScoreLinkContent failed: %v", err)
	}

	if score.Score != 0.1 {
		t.Errorf("Score = %f, want 0.1", score.Score)
	}

	if score.IsRecommended {
		t.Error("Expected IsRecommended to be false for malicious content")
	}

	if len(score.MaliciousIndicators) != 2 {
		t.Errorf("Expected 2 malicious indicators, got %d", len(score.MaliciousIndicators))
	}
}

func TestScoreLinkContentInvalidURL(t *testing.T) {
	config := DefaultConfig()
	s := New(config, nil)

	ctx := context.Background()

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "invalid scheme",
			url:  "ftp://example.com",
		},
		{
			name: "malformed URL",
			url:  "ht!tp://invalid",
		},
		{
			name: "empty URL",
			url:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.ScoreLinkContent(ctx, tt.url)
			if err == nil {
				t.Error("Expected error for invalid URL, got nil")
			}
		})
	}
}

func TestScoreLinkContentOllamaFailure(t *testing.T) {
	// Create mock Ollama server that fails
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Test</title></head><body><p>Test content</p></body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:        10 * time.Second,
		OllamaBaseURL:      ollamaServer.URL,
		OllamaModel:        "test-model",
		LinkScoreThreshold: 0.5,
	}
	s := New(config, nil)

	ctx := context.Background()
	score, err := s.ScoreLinkContent(ctx, webServer.URL)

	// Should not error, should return default low score
	if err != nil {
		t.Fatalf("ScoreLinkContent should handle Ollama failure gracefully: %v", err)
	}

	if score.Score != 0.0 {
		t.Errorf("Expected score 0.0 on Ollama failure, got %f", score.Score)
	}

	if score.IsRecommended {
		t.Error("Expected IsRecommended to be false when Ollama fails")
	}
}

func TestScoreLinkContentCustomThreshold(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: `{"score": 0.6, "reason": "Moderate quality content", "categories": ["business"], "malicious_indicators": []}`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Business Article</title></head><body><p>Business content</p></body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	tests := []struct {
		name          string
		threshold     float64
		shouldBeRecommended bool
	}{
		{
			name:          "threshold 0.5",
			threshold:     0.5,
			shouldBeRecommended: true, // 0.6 >= 0.5
		},
		{
			name:          "threshold 0.7",
			threshold:     0.7,
			shouldBeRecommended: false, // 0.6 < 0.7
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				HTTPTimeout:        10 * time.Second,
				OllamaBaseURL:      ollamaServer.URL,
				OllamaModel:        "test-model",
				LinkScoreThreshold: tt.threshold,
			}
			s := New(config, nil)

			ctx := context.Background()
			score, err := s.ScoreLinkContent(ctx, webServer.URL)

			if err != nil {
				t.Fatalf("ScoreLinkContent failed: %v", err)
			}

			if score.IsRecommended != tt.shouldBeRecommended {
				t.Errorf("IsRecommended = %v, want %v (threshold %f, score %f)",
					score.IsRecommended, tt.shouldBeRecommended, tt.threshold, score.Score)
			}
		})
	}
}

func TestScrapeIncludesScore(t *testing.T) {
	// Create mock Ollama server that returns scoring
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return different responses based on the request
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		prompt, _ := reqBody["prompt"].(string)

		// Scoring request
		if containsHelper(prompt, "quality score") || containsHelper(prompt, "quality assessment") {
			resp := models.OllamaResponse{
				Response: `{"score": 0.85, "reason": "High quality technical content", "categories": ["technical", "education"], "malicious_indicators": []}`,
				Done:     true,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Content extraction or link filtering - just return simple text
		resp := models.OllamaResponse{
			Response: "Cleaned content",
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock web server
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head>
	<title>Test Article</title>
	<meta name="description" content="Test description">
</head>
<body>
	<h1>Test Content</h1>
	<p>This is test content for scraping.</p>
	<a href="/link1">Link 1</a>
</body>
</html>
`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:         10 * time.Second,
		OllamaBaseURL:       ollamaServer.URL,
		OllamaModel:         "test-model",
		LinkScoreThreshold:  0.5,
		EnableImageAnalysis: false, // Disable to simplify test
	}
	s := New(config, nil)

	ctx := context.Background()
	data, err := s.Scrape(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	// Verify basic scraped data
	if data.URL != webServer.URL {
		t.Errorf("URL = %s, want %s", data.URL, webServer.URL)
	}

	if data.Title == "" {
		t.Error("Expected non-empty title")
	}

	// Verify score metadata is present
	if data.Score == nil {
		t.Fatal("Expected Score to be present in ScrapedData")
	}

	if data.Score.Score != 0.85 {
		t.Errorf("Score = %f, want 0.85", data.Score.Score)
	}

	if data.Score.Reason != "High quality technical content" {
		t.Errorf("Reason = %s, want 'High quality technical content'", data.Score.Reason)
	}

	if len(data.Score.Categories) != 2 {
		t.Errorf("Expected 2 categories, got %d", len(data.Score.Categories))
	}

	if !data.Score.IsRecommended {
		t.Error("Expected IsRecommended to be true for score 0.85 with threshold 0.5")
	}

	t.Logf("✓ Scrape includes score metadata: score=%.2f, recommended=%v",
		data.Score.Score, data.Score.IsRecommended)
}

// Helper function
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestScoreContentFallbackSocialMedia tests fallback scoring for social media
func TestScoreContentFallbackSocialMedia(t *testing.T) {
	score, reason, categories, indicators := scoreContentFallback(
		"https://www.facebook.com/profile",
		"Facebook Profile",
		"This is my Facebook profile with posts and photos.",
	)

	if score != 0.1 {
		t.Errorf("Expected score 0.1 for social media, got %.2f", score)
	}

	if !containsString(categories, "social-media") {
		t.Error("Expected 'social-media' category")
	}

	if !containsString(categories, "low-quality") {
		t.Error("Expected 'low-quality' category")
	}

	if !strings.Contains(reason, "Blocked content type") {
		t.Errorf("Expected reason to mention blocked content, got: %s", reason)
	}

	if len(indicators) == 0 {
		t.Error("Expected malicious indicators for social media")
	}
}

// TestScoreContentFallbackQualityDomain tests fallback scoring for quality domains
func TestScoreContentFallbackQualityDomain(t *testing.T) {
	score, reason, categories, _ := scoreContentFallback(
		"https://en.wikipedia.org/wiki/Artificial_Intelligence",
		"Artificial Intelligence - Wikipedia",
		strings.Repeat("This is a comprehensive article about artificial intelligence. ", 50),
	)

	if score < 0.7 {
		t.Errorf("Expected high score for Wikipedia, got %.2f", score)
	}

	if !containsString(categories, "reference") || !containsString(categories, "trusted-source") {
		t.Errorf("Expected quality categories, got: %v", categories)
	}

	if !strings.Contains(reason, "Quality domain") {
		t.Errorf("Expected reason to mention quality domain, got: %s", reason)
	}
}

// TestScoreContentFallbackShortContent tests fallback scoring for short content
func TestScoreContentFallbackShortContent(t *testing.T) {
	score, reason, categories, _ := scoreContentFallback(
		"https://example.com/short",
		"Short Page",
		"Very short content here.",
	)

	if score >= 0.5 {
		t.Errorf("Expected low score for short content, got %.2f", score)
	}

	if !containsString(categories, "low-quality") {
		t.Errorf("Expected 'low-quality' category, got: %v", categories)
	}

	if !strings.Contains(reason, "short") {
		t.Errorf("Expected reason to mention short content, got: %s", reason)
	}
}

// TestScoreContentFallbackSpam tests fallback scoring for spam content
func TestScoreContentFallbackSpam(t *testing.T) {
	spamContent := "Click here! Click here! Click here! Buy now! Buy now! Limited offer!"
	score, reason, categories, indicators := scoreContentFallback(
		"https://example.com/spam",
		"Amazing Offer",
		spamContent,
	)

	if score >= 0.3 {
		t.Errorf("Expected very low score for spam, got %.2f", score)
	}

	if !containsString(categories, "spam") {
		t.Errorf("Expected 'spam' category, got: %v", categories)
	}

	if !strings.Contains(reason, "Spam indicators") {
		t.Errorf("Expected reason to mention spam, got: %s", reason)
	}

	if !containsString(indicators, "spam-keywords") {
		t.Errorf("Expected spam-keywords in malicious indicators, got: %v", indicators)
	}
}

// TestScoreContentFallbackTechnical tests fallback scoring for technical content
func TestScoreContentFallbackTechnical(t *testing.T) {
	technicalContent := strings.Repeat("This is a technical guide about software development and programming best practices. ", 20)
	score, reason, categories, _ := scoreContentFallback(
		"https://example.com/tutorial",
		"Software Development Tutorial",
		technicalContent,
	)

	if score < 0.6 {
		t.Errorf("Expected good score for technical content, got %.2f", score)
	}

	if !containsString(categories, "technical") || !containsString(categories, "educational") {
		t.Errorf("Expected technical/educational categories, got: %v", categories)
	}

	if !strings.Contains(reason, "Rule-based") {
		t.Errorf("Expected reason to mention rule-based assessment, got: %s", reason)
	}
}

// TestScoreContentFallbackGambling tests fallback scoring for gambling sites
func TestScoreContentFallbackGambling(t *testing.T) {
	score, _, categories, indicators := scoreContentFallback(
		"https://www.betcasino.com",
		"Online Casino",
		"Place your bets and win big!",
	)

	if score != 0.1 {
		t.Errorf("Expected score 0.1 for gambling site, got %.2f", score)
	}

	if !containsString(categories, "gambling") {
		t.Errorf("Expected 'gambling' category, got: %v", categories)
	}

	if len(indicators) == 0 {
		t.Error("Expected malicious indicators for gambling site")
	}
}

func TestCheckForLowQualityPatternsAudioVideo(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		title        string
		wantCategory string
	}{
		{
			name:         "MP3 audio file",
			url:          "https://example.com/music/song.mp3",
			title:        "My Song",
			wantCategory: "audio-video",
		},
		{
			name:         "MP4 video file",
			url:          "https://example.com/videos/movie.mp4",
			title:        "Movie",
			wantCategory: "audio-video",
		},
		{
			name:         "YouTube link",
			url:          "https://www.youtube.com/watch?v=abc123",
			title:        "YouTube Video",
			wantCategory: "streaming",
		},
		{
			name:         "Spotify link",
			url:          "https://open.spotify.com/track/xyz789",
			title:        "Spotify Track",
			wantCategory: "streaming",
		},
		{
			name:         "Audio file with query params",
			url:          "https://cdn.example.com/audio.mp3?token=abc",
			title:        "Audio",
			wantCategory: "audio-video",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldSkip, score, reason, categories, indicators := checkForLowQualityPatterns(
				tt.url,
				tt.title,
			)

			if !shouldSkip {
				t.Errorf("Expected shouldSkip=true for %s", tt.name)
			}

			if score != 0.15 {
				t.Errorf("Expected score 0.15 for %s, got %.2f", tt.name, score)
			}

			if !containsString(categories, tt.wantCategory) {
				t.Errorf("Expected '%s' category for %s, got: %v", tt.wantCategory, tt.name, categories)
			}

			if !containsString(categories, "media") {
				t.Errorf("Expected 'media' category for %s, got: %v", tt.name, categories)
			}

			if !containsString(categories, "low-quality") {
				t.Errorf("Expected 'low-quality' category for %s, got: %v", tt.name, categories)
			}

			if len(indicators) == 0 {
				t.Errorf("Expected malicious indicators for %s", tt.name)
			}

			if reason == "" {
				t.Errorf("Expected reason for %s", tt.name)
			}
		})
	}
}

func TestCheckForLowQualityPatternsUtilityPages(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		title        string
		wantCategory string
		wantScore    float64
	}{
		{
			name:         "Subscription page",
			url:          "https://example.com/subscribe",
			title:        "Subscribe Now",
			wantCategory: "subscription",
			wantScore:    0.1,
		},
		{
			name:         "Pricing page",
			url:          "https://example.com/pricing",
			title:        "Our Pricing",
			wantCategory: "subscription",
			wantScore:    0.1,
		},
		{
			name:         "Settings page",
			url:          "https://example.com/settings",
			title:        "Account Settings",
			wantCategory: "settings",
			wantScore:    0.1,
		},
		{
			name:         "Preferences page (singular)",
			url:          "https://example.com/preference",
			title:        "Preferences",
			wantCategory: "settings",
			wantScore:    0.1,
		},
		{
			name:         "Login page",
			url:          "https://example.com/login",
			title:        "Sign In",
			wantCategory: "account",
			wantScore:    0.1,
		},
		{
			name:         "Shopping cart",
			url:          "https://example.com/cart",
			title:        "Your Cart",
			wantCategory: "commerce",
			wantScore:    0.1,
		},
		{
			name:         "Unsubscribe page",
			url:          "https://example.com/unsubscribe",
			title:        "Unsubscribe",
			wantCategory: "unsubscribe",
			wantScore:    0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldSkip, score, reason, categories, indicators := checkForLowQualityPatterns(
				tt.url,
				tt.title,
			)

			if !shouldSkip {
				t.Errorf("Expected shouldSkip=true for %s", tt.name)
			}

			if score != tt.wantScore {
				t.Errorf("Expected score %.2f for %s, got %.2f", tt.wantScore, tt.name, score)
			}

			if !containsString(categories, tt.wantCategory) {
				t.Errorf("Expected '%s' category for %s, got: %v", tt.wantCategory, tt.name, categories)
			}

			if !containsString(categories, "low-quality") {
				t.Errorf("Expected 'low-quality' category for %s, got: %v", tt.name, categories)
			}

			if !containsString(categories, "utility-page") {
				t.Errorf("Expected 'utility-page' category for %s, got: %v", tt.name, categories)
			}

			if len(indicators) == 0 {
				t.Errorf("Expected malicious indicators for %s", tt.name)
			}

			if reason == "" {
				t.Errorf("Expected reason for %s", tt.name)
			}
		})
	}
}

func TestIsAudioVideoURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{"MP3 file", "https://example.com/audio.mp3", true},
		{"MP4 file", "https://example.com/video.mp4", true},
		{"YouTube", "https://www.youtube.com/watch?v=abc123", true},
		{"Spotify", "https://open.spotify.com/track/xyz", true},
		{"Audio with query", "https://cdn.example.com/song.mp3?token=abc", true},
		{"Regular webpage", "https://example.com/article", false},
		{"GitHub", "https://github.com/user/repo", false},
		{"Wikipedia", "https://en.wikipedia.org/wiki/Article", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAudioVideoURL(tt.url)
			if result != tt.expected {
				t.Errorf("isAudioVideoURL(%s) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestScoreLinkContentAudioVideoWithOllama(t *testing.T) {
	// Create mock Ollama server (should NOT be called for audio/video URLs)
	ollamaCallCount := 0
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ollamaCallCount++
		t.Error("Ollama should not be called for audio/video URLs")
	}))
	defer ollamaServer.Close()

	// Create mock web server for YouTube-like page
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Video Title</title></head><body>Video content</body></html>`))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:        10 * time.Second,
		OllamaBaseURL:      ollamaServer.URL,
		OllamaModel:        "test-model",
		LinkScoreThreshold: 0.5,
	}
	s := New(config, nil)

	ctx := context.Background()

	// Test with YouTube URL (should be detected before calling Ollama)
	youtubeURL := "https://www.youtube.com/watch?v=test123"
	score, err := s.ScoreLinkContent(ctx, youtubeURL)
	if err != nil {
		t.Fatalf("ScoreLinkContent failed: %v", err)
	}

	if score.Score != 0.15 {
		t.Errorf("Expected score 0.15 for YouTube URL, got %.2f", score.Score)
	}

	if score.AIUsed {
		t.Error("Expected AIUsed to be false for audio/video URL")
	}

	if ollamaCallCount > 0 {
		t.Errorf("Ollama was called %d times, expected 0", ollamaCallCount)
	}

	if !containsString(score.Categories, "streaming") {
		t.Errorf("Expected 'streaming' category, got: %v", score.Categories)
	}
}

func TestScoreLinkContentImageURLSkipsScoring(t *testing.T) {
	// Create mock Ollama server (should NOT be called for image URLs)
	ollamaCallCount := 0
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ollamaCallCount++
		t.Error("Ollama should not be called for image URLs")
	}))
	defer ollamaServer.Close()

	config := Config{
		HTTPTimeout:        10 * time.Second,
		OllamaBaseURL:      ollamaServer.URL,
		OllamaModel:        "test-model",
		LinkScoreThreshold: 0.5,
	}
	s := New(config, nil)

	ctx := context.Background()

	// Test various image extensions and URL patterns
	imageURLs := []string{
		"https://example.com/photo.jpg",
		"https://example.com/image.jpeg",
		"https://example.com/picture.png",
		"https://example.com/graphic.gif",
		"https://example.com/photo.webp",
		"https://example.com/icon.svg",
		"https://example.com/image.jpg?size=large",
		"https://images.unsplash.com/photo-1591738802175-709fedef8288?crop=entropy&cs=tinysrgb&fit=max&fm=jpg&ixlib=rb-4.1.0&q=80&w=1080",
		"https://example.com/api/image?format=png&width=500",
		"https://cdn.example.com/img?ext=webp",
		"https://i.imgur.com/abc123.jpg",
		"https://images.pexels.com/photos/123/nature.jpg?auto=compress",
		"https://source.unsplash.com/random/800x600",
	}

	for _, imageURL := range imageURLs {
		t.Run(imageURL, func(t *testing.T) {
			score, err := s.ScoreLinkContent(ctx, imageURL)
			if err != nil {
				t.Fatalf("ScoreLinkContent failed for %s: %v", imageURL, err)
			}

			if score.Score != 0.0 {
				t.Errorf("Expected score 0.0 for image URL %s, got %.2f", imageURL, score.Score)
			}

			if score.AIUsed {
				t.Error("Expected AIUsed to be false for image URL")
			}

			if !containsString(score.Categories, "image") {
				t.Errorf("Expected 'image' category for %s, got: %v", imageURL, score.Categories)
			}

			if !containsString(score.Categories, "media") {
				t.Errorf("Expected 'media' category for %s, got: %v", imageURL, score.Categories)
			}

			if !strings.Contains(score.Reason, "Image file detected") {
				t.Errorf("Expected reason to mention image detection for %s, got: %s", imageURL, score.Reason)
			}

			if score.IsRecommended {
				t.Error("Expected IsRecommended to be false for image URL")
			}
		})
	}

	if ollamaCallCount > 0 {
		t.Errorf("Ollama was called %d times, expected 0", ollamaCallCount)
	}
}

// TestScrapeWithFallbackScoring tests that scraping works with fallback scoring when Ollama is down
func TestScrapeWithFallbackScoring(t *testing.T) {
	// Create a mock web server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<html><head><title>Test Article</title></head><body>` +
			strings.Repeat("<p>This is a substantial article about important topics. </p>", 30) +
			`</body></html>`
		w.Write([]byte(html))
	})
	webServer := httptest.NewServer(handler)
	defer webServer.Close()

	// Create scraper WITHOUT Ollama client (will fail and use fallback)
	config := DefaultConfig()
	config.LinkScoreThreshold = 0.5
	s := New(config, nil)

	ctx := context.Background()
	data, err := s.Scrape(ctx, webServer.URL)
	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	// Verify score is present (from fallback)
	if data.Score == nil {
		t.Fatal("Expected Score to be present from fallback scoring")
	}

	// Score should be decent for substantial content
	if data.Score.Score < 0.4 {
		t.Errorf("Expected reasonable fallback score for good content, got %.2f", data.Score.Score)
	}

	// Reason should indicate rule-based assessment
	if !strings.Contains(data.Score.Reason, "Rule-based") {
		t.Errorf("Expected reason to indicate rule-based fallback, got: %s", data.Score.Reason)
	}

	// Categories should not be empty
	if len(data.Score.Categories) == 0 {
		t.Error("Expected categories from fallback scoring")
	}

	// Verify AIUsed is false for rule-based fallback
	if data.Score.AIUsed {
		t.Error("Expected AIUsed to be false for rule-based fallback")
	}
}

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase conversion",
			input:    "Machine Learning",
			expected: "machine-learning",
		},
		{
			name:     "underscore to hyphen",
			input:    "social_media",
			expected: "social-media",
		},
		{
			name:     "multiple spaces",
			input:    "New  York  City",
			expected: "new-york-city",
		},
		{
			name:     "mixed spaces and underscores",
			input:    "low_quality content",
			expected: "low-quality-content",
		},
		{
			name:     "leading and trailing spaces",
			input:    "  spam  ",
			expected: "spam",
		},
		{
			name:     "multiple consecutive hyphens",
			input:    "foo--bar---baz",
			expected: "foo-bar-baz",
		},
		{
			name:     "already normalized",
			input:    "technical",
			expected: "technical",
		},
		{
			name:     "single word uppercase",
			input:    "EDUCATIONAL",
			expected: "educational",
		},
		{
			name:     "malicious indicator",
			input:    "spam_keywords",
			expected: "spam-keywords",
		},
		{
			name:     "trusted source",
			input:    "trusted_source",
			expected: "trusted-source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeTag(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestShouldSkipImage(t *testing.T) {
	tests := []struct {
		name     string
		imageURL string
		expected bool
	}{
		// Should skip
		{
			name:     "placeholder in URL",
			imageURL: "https://example.com/placeholder.png",
			expected: true,
		},
		{
			name:     "temp in URL",
			imageURL: "https://example.com/temp-image.jpg",
			expected: true,
		},
		{
			name:     "temporary in URL",
			imageURL: "https://example.com/temporary_file.png",
			expected: true,
		},
		{
			name:     "icon in URL",
			imageURL: "https://example.com/facebook-icon.svg",
			expected: true,
		},
		{
			name:     "logo in URL",
			imageURL: "https://example.com/company-logo.png",
			expected: true,
		},
		{
			name:     "button in URL",
			imageURL: "https://example.com/share-button.png",
			expected: true,
		},
		{
			name:     "sprite in URL",
			imageURL: "https://example.com/ui-sprite.png",
			expected: true,
		},
		{
			name:     "default avatar in URL",
			imageURL: "https://example.com/avatar-default.jpg",
			expected: true,
		},
		{
			name:     "tracking pixel in URL",
			imageURL: "https://example.com/pixel.gif",
			expected: true,
		},
		{
			name:     "1x1 tracking pixel",
			imageURL: "https://example.com/track-1x1.png",
			expected: true,
		},
		{
			name:     "spacer image",
			imageURL: "https://example.com/spacer.gif",
			expected: true,
		},
		{
			name:     "loader animation",
			imageURL: "https://example.com/loader.svg",
			expected: true,
		},
		{
			name:     "advertisement banner",
			imageURL: "https://example.com/ad-banner.jpg",
			expected: true,
		},
		// Should not skip
		{
			name:     "regular image",
			imageURL: "https://example.com/article-photo.jpg",
			expected: false,
		},
		{
			name:     "product image",
			imageURL: "https://example.com/products/widget-2000.png",
			expected: false,
		},
		{
			name:     "case sensitive check - uppercase PLACEHOLDER",
			imageURL: "https://example.com/PLACEHOLDER.jpg",
			expected: true,
		},
		{
			name:     "profile picture",
			imageURL: "https://example.com/users/john-smith.jpg",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldSkipImage(tt.imageURL)
			if result != tt.expected {
				t.Errorf("shouldSkipImage(%q) = %v, expected %v", tt.imageURL, result, tt.expected)
			}
		})
	}
}

func TestImageFiltering(t *testing.T) {
	// Create mock Ollama server
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock image analysis response
		resp := models.OllamaResponse{
			Response: `{"summary": "Test image", "tags": ["test"]}`,
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create scraper with image analysis enabled
	config := DefaultConfig()
	config.OllamaBaseURL = ollamaServer.URL
	config.EnableImageAnalysis = true
	s := New(config, nil)

	// Create test images - mix of valid and junk images
	images := []models.ImageInfo{
		{URL: "https://example.com/article-photo.jpg", AltText: "Article photo"},
		{URL: "https://example.com/placeholder.png", AltText: "Placeholder"},
		{URL: "https://example.com/temp-image.jpg", AltText: "Temp"},
		{URL: "https://example.com/product.jpg", AltText: "Product"},
		{URL: "https://example.com/icon.svg", AltText: "Icon"},
		{URL: "https://example.com/logo.png", AltText: "Logo"},
		{URL: "https://example.com/hero-image.jpg", AltText: "Hero"},
	}

	ctx := context.Background()
	processedImages, existingRefs, warnings := s.processImages(ctx, images)

	// Check that no existing refs were found (all new images)
	if len(existingRefs) != 0 {
		t.Errorf("Expected 0 existing image refs, got %d", len(existingRefs))
	}

	// Should filter out: placeholder, temp, icon, logo (4 images)
	// Should keep: article-photo, product, hero-image (3 images)
	expectedCount := 3
	if len(processedImages) != expectedCount {
		t.Errorf("Expected %d images after filtering, got %d", expectedCount, len(processedImages))
	}

	// Check that the warning was added
	hasSkipWarning := false
	for _, warning := range warnings {
		if contains(warning, "Skipped") && contains(warning, "placeholder/temp/UI component") {
			hasSkipWarning = true
			break
		}
	}
	if !hasSkipWarning {
		t.Error("Expected warning about skipped images")
	}

	// Verify the correct images were kept
	keptURLs := make(map[string]bool)
	for _, img := range processedImages {
		keptURLs[img.URL] = true
	}

	expectedKept := []string{
		"https://example.com/article-photo.jpg",
		"https://example.com/product.jpg",
		"https://example.com/hero-image.jpg",
	}

	for _, url := range expectedKept {
		if !keptURLs[url] {
			t.Errorf("Expected image %q to be kept, but it was filtered out", url)
		}
	}

	// Verify the junk images were filtered out
	unexpectedKept := []string{
		"https://example.com/placeholder.png",
		"https://example.com/temp-image.jpg",
		"https://example.com/icon.svg",
		"https://example.com/logo.png",
	}

	for _, url := range unexpectedKept {
		if keptURLs[url] {
			t.Errorf("Expected image %q to be filtered out, but it was kept", url)
		}
	}
}

func TestExtractEXIF(t *testing.T) {
	tests := []struct {
		name        string
		imageData   []byte
		expectEXIF  bool
		checkFields func(*testing.T, *models.EXIFData)
	}{
		{
			name: "PNG without EXIF",
			imageData: []byte{
				0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
				0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
				0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
				0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
				0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d, 0xb4, 0x00, 0x00, 0x00,
				0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
			},
			expectEXIF: false,
		},
		{
			name: "Invalid image data",
			imageData: []byte{0x00, 0x01, 0x02, 0x03},
			expectEXIF: false,
		},
		{
			name: "Empty data",
			imageData: []byte{},
			expectEXIF: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exifData := extractEXIF(tt.imageData)

			if tt.expectEXIF && exifData == nil {
				t.Error("Expected EXIF data but got nil")
			}

			if !tt.expectEXIF && exifData != nil {
				t.Error("Expected no EXIF data but got data")
			}

			if tt.checkFields != nil && exifData != nil {
				tt.checkFields(t, exifData)
			}
		})
	}
}

func TestWebPImageDimensions(t *testing.T) {
	// Minimal valid WebP image (1x1 pixel)
	// This is a valid WebP file with RIFF container
	webpData := []byte{
		// RIFF header
		0x52, 0x49, 0x46, 0x46, // "RIFF"
		0x1a, 0x00, 0x00, 0x00, // File size
		0x57, 0x45, 0x42, 0x50, // "WEBP"
		// VP8 chunk
		0x56, 0x50, 0x38, 0x20, // "VP8 "
		0x0e, 0x00, 0x00, 0x00, // Chunk size
		// VP8 bitstream
		0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a,
		0x01, 0x00, 0x01, 0x00, 0x00, 0x47, 0x08, 0x85,
	}

	width, height, err := getImageDimensions(webpData)
	if err != nil {
		t.Fatalf("Failed to get dimensions from WebP image: %v", err)
	}

	// The test WebP image is 1x1
	if width != 1 {
		t.Errorf("WebP width = %d, want 1", width)
	}

	if height != 1 {
		t.Errorf("WebP height = %d, want 1", height)
	}

	t.Logf("Successfully extracted WebP dimensions: %dx%d", width, height)
}

func TestIsCategoryPage(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		isCategory bool
	}{
		// Should be detected as category pages
		{
			name:       "BBC Arts section",
			url:        "https://www.bbc.com/arts",
			isCategory: true,
		},
		{
			name:       "Guardian World Asia",
			url:        "https://www.theguardian.com/world/asia",
			isCategory: true,
		},
		{
			name:       "NYTimes Politics",
			url:        "https://www.nytimes.com/section/politics",
			isCategory: true,
		},
		{
			name:       "CNN Business",
			url:        "https://www.cnn.com/business",
			isCategory: true,
		},
		{
			name:       "Tech category",
			url:        "https://example.com/technology",
			isCategory: true,
		},
		{
			name:       "Sports section",
			url:        "https://example.com/sports",
			isCategory: true,
		},
		{
			name:       "Category with subcategory",
			url:        "https://example.com/category/tech",
			isCategory: true,
		},
		{
			name:       "Year archive",
			url:        "https://example.com/2024",
			isCategory: true,
		},
		{
			name:       "Year/Month archive",
			url:        "https://example.com/2024/01",
			isCategory: true,
		},
		{
			name:       "Year/Month/Day archive (all numbers)",
			url:        "https://example.com/2024/01/15",
			isCategory: true,
		},
		{
			name:       "Tag page",
			url:        "https://example.com/tag/ai",
			isCategory: true,
		},
		{
			name:       "Topic page",
			url:        "https://example.com/topic/climate",
			isCategory: true,
		},
		{
			name:       "Multiple sections",
			url:        "https://example.com/world",
			isCategory: true,
		},
		{
			name:       "Opinion section",
			url:        "https://example.com/opinion",
			isCategory: true,
		},
		{
			name:       "Guardian Science section",
			url:        "https://www.theguardian.com/science",
			isCategory: true,
		},
		{
			name:       "DailyMail sciencetech index",
			url:        "https://www.dailymail.co.uk/sciencetech/index.html",
			isCategory: true,
		},
		{
			name:       "BBC News section",
			url:        "https://www.bbc.com/news",
			isCategory: true,
		},
		{
			name:       "BBC World/Asia section",
			url:        "https://www.bbc.com/news/world/asia",
			isCategory: true,
		},
		{
			name:       "BBC World/Europe section",
			url:        "https://www.bbc.com/news/world/europe",
			isCategory: true,
		},

		// Should NOT be detected as category pages
		{
			name:       "Homepage",
			url:        "https://www.bbc.com",
			isCategory: false,
		},
		{
			name:       "Article with world in path",
			url:        "https://www.theguardian.com/world/asia/2024/jan/15/story-title",
			isCategory: false,
		},
		{
			name:       "Specific article",
			url:        "https://www.bbc.com/news/article-title-12345",
			isCategory: false,
		},
		{
			name:       "BBC article with 8-digit ID",
			url:        "https://www.bbc.com/news/world-middle-east-12345678",
			isCategory: false,
		},
		{
			name:       "BBC article in articles path",
			url:        "https://www.bbc.com/news/articles/c1234567",
			isCategory: false,
		},
		{
			name:       "Article with date in path",
			url:        "https://example.com/2024/01/15/article-title",
			isCategory: false,
		},
		{
			name:       "Deep nested article",
			url:        "https://example.com/world/asia/china/article-name",
			isCategory: false,
		},
		{
			name:       "Article with ID",
			url:        "https://example.com/article/12345",
			isCategory: false,
		},
		{
			name:       "Blog post",
			url:        "https://example.com/blog/my-post-title",
			isCategory: false,
		},
		{
			name:       "Invalid URL",
			url:        "not a url",
			isCategory: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCategoryPage(tt.url)
			if result != tt.isCategory {
				t.Errorf("isCategoryPage(%q) = %v, want %v", tt.url, result, tt.isCategory)
			}
		})
	}
}

func TestDeduplicateLinks(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "no duplicates",
			input:    []string{"https://example.com/1", "https://example.com/2", "https://example.com/3"},
			expected: []string{"https://example.com/1", "https://example.com/2", "https://example.com/3"},
		},
		{
			name:     "with duplicates",
			input:    []string{"https://example.com/1", "https://example.com/2", "https://example.com/1", "https://example.com/3", "https://example.com/2"},
			expected: []string{"https://example.com/1", "https://example.com/2", "https://example.com/3"},
		},
		{
			name:     "all duplicates",
			input:    []string{"https://example.com/same", "https://example.com/same", "https://example.com/same"},
			expected: []string{"https://example.com/same"},
		},
		{
			name:     "empty list",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "single item",
			input:    []string{"https://example.com/single"},
			expected: []string{"https://example.com/single"},
		},
		{
			name:     "preserves order",
			input:    []string{"https://z.com", "https://a.com", "https://m.com", "https://a.com", "https://z.com"},
			expected: []string{"https://z.com", "https://a.com", "https://m.com"},
		},
		{
			name:     "consecutive duplicates",
			input:    []string{"https://a.com", "https://a.com", "https://b.com", "https://b.com", "https://c.com"},
			expected: []string{"https://a.com", "https://b.com", "https://c.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicateLinks(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("deduplicateLinks() length = %d, want %d", len(result), len(tt.expected))
			}

			for i, url := range result {
				if i >= len(tt.expected) || url != tt.expected[i] {
					t.Errorf("deduplicateLinks() at index %d = %q, want %q", i, url, tt.expected[i])
				}
			}
		})
	}
}
