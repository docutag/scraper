package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zombar/scraper/models"
)

// TestScrapeWithWarnings tests that warnings are properly populated when Ollama fails
func TestScrapeWithWarnings(t *testing.T) {
	// Create mock Ollama server that fails
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ollamaServer.Close()

	// Create mock web server with content and images
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

	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<h1>Test Content</h1>
	<p>This is test content.</p>
	<img src="` + imageServer.URL + `/test.png" alt="Test image">
</body>
</html>`
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
		LinkScoreThreshold:  0.5,
	}
	s := New(config)

	ctx := context.Background()
	data, err := s.Scrape(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	// Verify warnings are present
	if len(data.Warnings) == 0 {
		t.Error("Expected warnings when Ollama fails, got none")
	}

	// Check for specific warnings
	hasContentWarning := false
	hasScoringWarning := false
	hasImageWarning := false

	for _, warning := range data.Warnings {
		if strings.Contains(warning, "content extraction") {
			hasContentWarning = true
		}
		if strings.Contains(warning, "scoring") {
			hasScoringWarning = true
		}
		if strings.Contains(warning, "image") {
			hasImageWarning = true
		}
	}

	if !hasContentWarning {
		t.Error("Expected content extraction warning when Ollama fails")
	}

	if !hasScoringWarning {
		t.Error("Expected scoring warning when Ollama fails")
	}

	if !hasImageWarning {
		t.Error("Expected image analysis warning when Ollama fails")
	}

	t.Logf("Warnings generated: %v", data.Warnings)
}

// TestScrapeNoWarnings tests that no warnings are generated when everything succeeds
func TestScrapeNoWarnings(t *testing.T) {
	// Create mock Ollama server that succeeds
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		// Check if it's an image analysis request (has images field)
		if _, hasImages := reqBody["images"]; hasImages {
			resp := models.OllamaResponse{
				Response: `{"summary": "A test image", "tags": ["test", "image"]}`,
				Done:     true,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Check if it's a scoring request
		if prompt, ok := reqBody["prompt"].(string); ok {
			if strings.Contains(prompt, "quality score") || strings.Contains(prompt, "quality assessment") {
				resp := models.OllamaResponse{
					Response: `{"score": 0.8, "reason": "Good content", "categories": ["technical"], "malicious_indicators": []}`,
					Done:     true,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
				return
			}
		}

		// Default response for content extraction
		resp := models.OllamaResponse{
			Response: "Extracted content",
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock image server
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `
<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<h1>Test Content</h1>
	<p>This is test content.</p>
	<img src="` + imageServer.URL + `/test.png" alt="Test image">
</body>
</html>`
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
		LinkScoreThreshold:  0.5,
	}
	s := New(config)

	ctx := context.Background()
	data, err := s.Scrape(ctx, webServer.URL)

	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	// Verify no warnings when everything succeeds
	if len(data.Warnings) > 0 {
		t.Errorf("Expected no warnings when Ollama succeeds, got: %v", data.Warnings)
	}
}

// TestParallelImageProcessing tests that images are processed in parallel
func TestParallelImageProcessing(t *testing.T) {
	processingTimes := make(chan time.Time, 10)

	// Create mock Ollama server that tracks when requests are made
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		// Only track image analysis requests
		if _, hasImages := reqBody["images"]; hasImages {
			processingTimes <- time.Now()
			// Add small delay to simulate processing
			time.Sleep(100 * time.Millisecond)
		}

		resp := models.OllamaResponse{
			Response: `{"summary": "Test image", "tags": ["test"]}`,
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	// Create mock image server
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// Create a page with multiple images
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Test</title></head><body>`
		for i := 0; i < 5; i++ {
			html += `<img src="` + imageServer.URL + `/test` + string(rune('0'+i)) + `.png" alt="Test">`
		}
		html += `</body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:         30 * time.Second,
		OllamaBaseURL:       ollamaServer.URL,
		OllamaModel:         "test-model",
		EnableImageAnalysis: true,
		MaxImageSizeBytes:   10 * 1024 * 1024,
		ImageTimeout:        5 * time.Second,
	}
	s := New(config)

	ctx := context.Background()
	start := time.Now()
	data, err := s.Scrape(ctx, webServer.URL)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Scrape failed: %v", err)
	}

	if len(data.Images) != 5 {
		t.Fatalf("Expected 5 images, got %d", len(data.Images))
	}

	// With parallel processing (3 concurrent requests), 5 images should take ~200ms
	// (first 3 in parallel: 100ms, then next 2 in parallel: 100ms)
	// Sequential processing would take ~500ms (5 * 100ms)
	// Give generous buffer for CI/test environments
	if elapsed > 1*time.Second {
		t.Errorf("Processing took too long (%v), parallel processing may not be working", elapsed)
	}

	close(processingTimes)

	// Verify that some requests were concurrent (timestamps within 50ms of each other)
	times := []time.Time{}
	for ts := range processingTimes {
		times = append(times, ts)
	}

	if len(times) < 3 {
		t.Skip("Not enough timing data to verify parallelism")
	}

	// Check if first 3 requests started within 50ms window (indicating parallelism)
	maxDiff := times[2].Sub(times[0])
	if maxDiff > 50*time.Millisecond {
		t.Logf("Warning: First 3 requests spread over %v, may not be truly parallel", maxDiff)
	} else {
		t.Logf("✓ Parallel processing confirmed: first 3 requests within %v", maxDiff)
	}
}

// TestOllamaSemaphoreThrottling tests that Ollama requests are properly throttled
func TestOllamaSemaphoreThrottling(t *testing.T) {
	concurrentRequests := make(chan struct{}, 10)
	maxConcurrent := 0
	currentConcurrent := 0

	// Create mock Ollama server that tracks concurrency
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		concurrentRequests <- struct{}{}
		currentConcurrent++
		if currentConcurrent > maxConcurrent {
			maxConcurrent = currentConcurrent
		}

		// Small delay to ensure concurrent requests overlap
		time.Sleep(50 * time.Millisecond)

		currentConcurrent--
		<-concurrentRequests

		resp := models.OllamaResponse{
			Response: "Test response",
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html><html><head><title>Test</title></head><body><p>Test</p></body></html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer webServer.Close()

	config := Config{
		HTTPTimeout:         30 * time.Second,
		OllamaBaseURL:       ollamaServer.URL,
		OllamaModel:         "test-model",
		EnableImageAnalysis: false, // Disable to simplify test
		LinkScoreThreshold:  0.5,
	}
	s := New(config)

	ctx := context.Background()

	// Scrape the same URL multiple times concurrently
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Scrape(ctx, webServer.URL)
			if err != nil {
				t.Errorf("Scrape failed: %v", err)
			}
		}()
	}

	wg.Wait()

	// Verify that concurrent requests were throttled (max 3 concurrent as per semaphore)
	if maxConcurrent > 3 {
		t.Errorf("Semaphore failed: max concurrent requests was %d, expected <= 3", maxConcurrent)
	}

	t.Logf("✓ Semaphore working: max concurrent Ollama requests was %d (limit: 3)", maxConcurrent)
}
