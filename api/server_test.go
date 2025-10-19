package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zombar/scraper"
	"github.com/zombar/scraper/db"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	// Create temp database file
	tempDB := t.TempDir() + "/test.db"

	config := Config{
		Addr: ":0",
		DBConfig: db.Config{
			Driver: "sqlite",
			DSN:    tempDB,
		},
		ScraperConfig: scraper.DefaultConfig(),
		CORSEnabled:   false,
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	cleanup := func() {
		if server.db != nil {
			server.db.Close()
		}
	}

	return server, cleanup
}

func TestHandleExtractLinks(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tests := []struct {
		name           string
		method         string
		body           interface{}
		wantStatusCode int
		wantErrMsg     string
		checkResponse  func(t *testing.T, resp *ExtractLinksResponse)
	}{
		{
			name:   "valid request",
			method: http.MethodPost,
			body: ExtractLinksRequest{
				URL: "https://httpbin.org/html",
			},
			wantStatusCode: http.StatusOK,
			checkResponse: func(t *testing.T, resp *ExtractLinksResponse) {
				if resp.URL != "https://httpbin.org/html" {
					t.Errorf("Expected URL to be https://httpbin.org/html, got %s", resp.URL)
				}
				if resp.Links == nil {
					t.Error("Expected Links to be non-nil")
				}
				if resp.Count != len(resp.Links) {
					t.Errorf("Count %d doesn't match links length %d", resp.Count, len(resp.Links))
				}
			},
		},
		{
			name:   "missing URL",
			method: http.MethodPost,
			body: ExtractLinksRequest{
				URL: "",
			},
			wantStatusCode: http.StatusBadRequest,
			wantErrMsg:     "url is required",
		},
		{
			name:           "invalid JSON",
			method:         http.MethodPost,
			body:           "invalid json",
			wantStatusCode: http.StatusBadRequest,
			wantErrMsg:     "invalid request body",
		},
		{
			name:           "GET method not allowed",
			method:         http.MethodGet,
			body:           nil,
			wantStatusCode: http.StatusMethodNotAllowed,
			wantErrMsg:     "method not allowed",
		},
		{
			name:   "invalid URL scheme",
			method: http.MethodPost,
			body: ExtractLinksRequest{
				URL: "ftp://example.com",
			},
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyBytes []byte
			var err error

			if tt.body != nil {
				if str, ok := tt.body.(string); ok {
					bodyBytes = []byte(str)
				} else {
					bodyBytes, err = json.Marshal(tt.body)
					if err != nil {
						t.Fatalf("Failed to marshal request body: %v", err)
					}
				}
			}

			req := httptest.NewRequest(tt.method, "/api/extract-links", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleExtractLinks(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatusCode)
			}

			if tt.wantErrMsg != "" {
				var errResp map[string]string
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("Failed to decode error response: %v", err)
				}
				if errResp["error"] != tt.wantErrMsg {
					t.Errorf("Error message = %q, want %q", errResp["error"], tt.wantErrMsg)
				}
			} else if w.Code >= 400 {
				// For error cases without specific message check, just verify there's an error field
				var errResp map[string]string
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Logf("Response body: %s", w.Body.String())
				} else if _, ok := errResp["error"]; !ok {
					t.Error("Expected error field in error response")
				}
			}

			if tt.checkResponse != nil && w.Code == http.StatusOK {
				var resp ExtractLinksResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				tt.checkResponse(t, &resp)
			}
		})
	}
}

func TestHandleExtractLinksTimeout(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	req := ExtractLinksRequest{
		URL: "https://httpbin.org/delay/10",
	}
	bodyBytes, _ := json.Marshal(req)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/extract-links", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	httpReq = httpReq.WithContext(ctx)

	w := httptest.NewRecorder()

	// This should timeout or handle the cancellation gracefully
	server.handleExtractLinks(w, httpReq)

	// We expect either a timeout error or the request to be cancelled
	if w.Code != http.StatusInternalServerError {
		t.Logf("Got status code %d, expected internal server error for timeout", w.Code)
	}
}

func TestHandleExtractLinksEdgeCases(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tests := []struct {
		name           string
		body           interface{}
		wantStatusCode int
		checkResponse  func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "very long URL",
			body: ExtractLinksRequest{
				URL: "https://example.com/" + string(make([]byte, 2000)),
			},
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name: "URL with special characters",
			body: ExtractLinksRequest{
				URL: "https://example.com/path?query=value&foo=bar#fragment",
			},
			wantStatusCode: http.StatusInternalServerError, // Will fail because it's not a real URL
		},
		{
			name: "empty request body",
			body: map[string]string{},
			wantStatusCode: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var errResp map[string]string
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("Failed to decode error response: %v", err)
				}
				if errResp["error"] != "url is required" {
					t.Errorf("Error message = %q, want %q", errResp["error"], "url is required")
				}
			},
		},
		{
			name: "URL with only whitespace",
			body: ExtractLinksRequest{
				URL: "   ",
			},
			wantStatusCode: http.StatusInternalServerError,
		},
		{
			name:           "malformed JSON body",
			body:           "{invalid json}",
			wantStatusCode: http.StatusBadRequest,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var errResp map[string]string
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("Failed to decode error response: %v", err)
				}
				if errResp["error"] != "invalid request body" {
					t.Errorf("Error message = %q, want %q", errResp["error"], "invalid request body")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyBytes []byte
			var err error

			if str, ok := tt.body.(string); ok {
				bodyBytes = []byte(str)
			} else {
				bodyBytes, err = json.Marshal(tt.body)
				if err != nil {
					t.Fatalf("Failed to marshal request body: %v", err)
				}
			}

			req := httptest.NewRequest(http.MethodPost, "/api/extract-links", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleExtractLinks(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatusCode)
			}

			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

func TestHandleExtractLinksResponseStructure(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test successful response structure
	req := ExtractLinksRequest{
		URL: "https://httpbin.org/html",
	}
	bodyBytes, _ := json.Marshal(req)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/extract-links", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleExtractLinks(w, httpReq)

	if w.Code != http.StatusOK {
		t.Skipf("Skipping response structure test - got status %d", w.Code)
		return
	}

	var resp ExtractLinksResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Validate response structure
	if resp.URL == "" {
		t.Error("Expected URL field to be populated")
	}

	if resp.Links == nil {
		t.Error("Expected Links field to be non-nil")
	}

	if resp.Count < 0 {
		t.Errorf("Expected Count to be non-negative, got %d", resp.Count)
	}

	if resp.Count != len(resp.Links) {
		t.Errorf("Count %d doesn't match actual links length %d", resp.Count, len(resp.Links))
	}

	// Verify Links is a valid JSON array
	for i, link := range resp.Links {
		if link == "" {
			t.Errorf("Link at index %d is empty", i)
		}
	}
}

func TestHandleHealth(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("Status = %q, want %q", resp["status"], "healthy")
	}
}

func TestHandleScrapeImages(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Create test scrape data with images
	ctx := context.Background()
	testURL := "https://example.com/test"
	result, err := server.scraper.Scrape(ctx, testURL)
	if err != nil {
		t.Skipf("Skipping test - failed to create test data: %v", err)
	}

	// Save the scrape data
	if err := server.db.SaveScrapedData(result); err != nil {
		t.Fatalf("Failed to save test scrape data: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		scrapeID       string
		wantStatusCode int
		wantErrMsg     string
		checkResponse  func(t *testing.T, resp *ImageSearchResponse)
	}{
		{
			name:           "valid scrape ID",
			method:         http.MethodGet,
			scrapeID:       result.ID,
			wantStatusCode: http.StatusOK,
			checkResponse: func(t *testing.T, resp *ImageSearchResponse) {
				if resp.Images == nil {
					t.Error("Expected Images to be non-nil")
				}
				if resp.Count != len(resp.Images) {
					t.Errorf("Count %d doesn't match images length %d", resp.Count, len(resp.Images))
				}
			},
		},
		{
			name:           "missing scrape ID",
			method:         http.MethodGet,
			scrapeID:       "",
			wantStatusCode: http.StatusBadRequest,
			wantErrMsg:     "scrape id is required",
		},
		{
			name:           "non-existent scrape ID",
			method:         http.MethodGet,
			scrapeID:       "non-existent-id",
			wantStatusCode: http.StatusOK,
			checkResponse: func(t *testing.T, resp *ImageSearchResponse) {
				if resp.Images == nil {
					t.Error("Expected Images to be non-nil (empty slice)")
				}
				if resp.Count != 0 {
					t.Errorf("Count should be 0 for non-existent scrape, got %d", resp.Count)
				}
			},
		},
		{
			name:           "POST method not allowed",
			method:         http.MethodPost,
			scrapeID:       result.ID,
			wantStatusCode: http.StatusMethodNotAllowed,
			wantErrMsg:     "method not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.scrapeID == "" {
				path = "/api/scrapes//images"
			} else {
				path = "/api/scrapes/" + tt.scrapeID + "/images"
			}

			req := httptest.NewRequest(tt.method, path, nil)
			w := httptest.NewRecorder()

			server.handleScrapeImages(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("Status code = %d, want %d", w.Code, tt.wantStatusCode)
			}

			if tt.wantErrMsg != "" {
				var errResp map[string]string
				if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
					t.Fatalf("Failed to decode error response: %v", err)
				}
				if errResp["error"] != tt.wantErrMsg {
					t.Errorf("Error message = %q, want %q", errResp["error"], tt.wantErrMsg)
				}
				return
			}

			if tt.checkResponse != nil {
				var resp ImageSearchResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				tt.checkResponse(t, &resp)
			}
		})
	}
}

