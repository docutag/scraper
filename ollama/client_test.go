package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zombar/scraper/models"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		model       string
		wantBaseURL string
		wantModel   string
	}{
		{
			name:        "default values",
			baseURL:     "",
			model:       "",
			wantBaseURL: DefaultBaseURL,
			wantModel:   DefaultModel,
		},
		{
			name:        "custom values",
			baseURL:     "http://custom:11434",
			model:       "custom-model",
			wantBaseURL: "http://custom:11434",
			wantModel:   "custom-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.baseURL, tt.model)
			if client.baseURL != tt.wantBaseURL {
				t.Errorf("baseURL = %s, want %s", client.baseURL, tt.wantBaseURL)
			}
			if client.model != tt.wantModel {
				t.Errorf("model = %s, want %s", client.model, tt.wantModel)
			}
			if client.httpClient == nil {
				t.Error("httpClient is nil")
			}
		})
	}
}

func TestGenerate(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/api/generate" {
			t.Errorf("Expected /api/generate path, got %s", r.URL.Path)
		}

		// Decode request
		var req models.OllamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
		}

		// Send response
		resp := models.OllamaResponse{
			Model:     req.Model,
			CreatedAt: time.Now().Format(time.RFC3339),
			Response:  "This is a test response",
			Done:      true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	response, err := client.Generate(ctx, "test prompt")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if response != "This is a test response" {
		t.Errorf("Unexpected response: %s", response)
	}
}

func TestGenerateError(t *testing.T) {
	// Create a test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	_, err := client.Generate(ctx, "test prompt")
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestGenerateContextCancellation(t *testing.T) {
	// Create a test server that delays
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		resp := models.OllamaResponse{
			Response: "Too late",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.Generate(ctx, "test prompt")
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestExtractContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.OllamaResponse{
			Response: "Extracted content without ads",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	rawText := "Article content here. Advertisement: Buy now! More content."
	result, err := client.ExtractContent(ctx, rawText)
	if err != nil {
		t.Fatalf("ExtractContent failed: %v", err)
	}

	if result != "Extracted content without ads" {
		t.Errorf("Unexpected result: %s", result)
	}
}

func TestAnalyzeImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a vision request with images
		var req models.OllamaVisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode vision request: %v", err)
		}

		if len(req.Images) == 0 {
			t.Error("Expected images in request")
		}

		// Return JSON response
		jsonResp := `{"summary": "A test image showing various elements", "tags": ["test", "image", "example"]}`
		resp := models.OllamaResponse{
			Response: jsonResp,
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	imageData := []byte("fake image data")
	summary, tags, err := client.AnalyzeImage(ctx, imageData, "alt text")
	if err != nil {
		t.Fatalf("AnalyzeImage failed: %v", err)
	}

	if summary != "A test image showing various elements" {
		t.Errorf("Unexpected summary: %s", summary)
	}

	expectedTags := []string{"test", "image", "example"}
	if len(tags) != len(expectedTags) {
		t.Errorf("Expected %d tags, got %d", len(expectedTags), len(tags))
	}
}

func TestAnalyzeImageInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return non-JSON response
		resp := models.OllamaResponse{
			Response: "This is not JSON",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	imageData := []byte("fake image data")
	summary, tags, err := client.AnalyzeImage(ctx, imageData, "")

	// Should not error, but return the raw response
	if err != nil {
		t.Fatalf("AnalyzeImage failed: %v", err)
	}

	if summary != "This is not JSON" {
		t.Errorf("Expected raw response as summary, got: %s", summary)
	}

	if len(tags) != 0 {
		t.Errorf("Expected empty tags for non-JSON response, got: %v", tags)
	}
}

func TestAnalyzeImageWithMarkdownWrappedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return JSON wrapped in markdown code blocks (common LLM behavior)
		resp := models.OllamaResponse{
			Response: "```json\n{\n  \"summary\": \"George Santos speaking at the Capitol\",\n  \"tags\": [\"politics\", \"capitol\", \"speech\"]\n}\n```",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	imageData := []byte("fake image data")
	summary, tags, err := client.AnalyzeImage(ctx, imageData, "")

	if err != nil {
		t.Fatalf("AnalyzeImage failed: %v", err)
	}

	// Should successfully parse the JSON after stripping markdown
	expectedSummary := "George Santos speaking at the Capitol"
	if summary != expectedSummary {
		t.Errorf("Expected summary %q, got: %q", expectedSummary, summary)
	}

	expectedTags := []string{"politics", "capitol", "speech"}
	if len(tags) != len(expectedTags) {
		t.Errorf("Expected %d tags, got %d", len(expectedTags), len(tags))
	}

	for i, tag := range expectedTags {
		if i >= len(tags) || tags[i] != tag {
			t.Errorf("Expected tag %q at index %d, got: %v", tag, i, tags)
		}
	}
}

func TestStripMarkdownCodeBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "json wrapped in markdown",
			input: "```json\n{\"key\": \"value\"}\n```",
			want:  "{\"key\": \"value\"}",
		},
		{
			name:  "plain json",
			input: "{\"key\": \"value\"}",
			want:  "{\"key\": \"value\"}",
		},
		{
			name:  "markdown without language specifier",
			input: "```\n{\"key\": \"value\"}\n```",
			want:  "{\"key\": \"value\"}",
		},
		{
			name:  "multiline json in markdown",
			input: "```json\n{\n  \"summary\": \"Test\",\n  \"tags\": [\"a\", \"b\"]\n}\n```",
			want:  "{\n  \"summary\": \"Test\",\n  \"tags\": [\"a\", \"b\"]\n}",
		},
		{
			name:  "plain text",
			input: "This is plain text",
			want:  "This is plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripMarkdownCodeBlocks(tt.input)
			if result != tt.want {
				t.Errorf("StripMarkdownCodeBlocks() = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "string shorter than max",
			input:  "short",
			maxLen: 10,
			want:   "short",
		},
		{
			name:   "string equal to max",
			input:  "exactly10c",
			maxLen: 10,
			want:   "exactly10c",
		},
		{
			name:   "string longer than max",
			input:  "this is a very long string",
			maxLen: 10,
			want:   "this is a ...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			if result != tt.want {
				t.Errorf("truncateString() = %q, want %q", result, tt.want)
			}
		})
	}
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
			input:    "Golden  Gate  Bridge",
			expected: "golden-gate-bridge",
		},
		{
			name:     "mixed spaces and underscores",
			input:    "urban_architecture space",
			expected: "urban-architecture-space",
		},
		{
			name:     "leading and trailing spaces",
			input:    "  picasso  ",
			expected: "picasso",
		},
		{
			name:     "multiple consecutive hyphens",
			input:    "foo--bar---baz",
			expected: "foo-bar-baz",
		},
		{
			name:     "already normalized",
			input:    "sunset",
			expected: "sunset",
		},
		{
			name:     "single word uppercase",
			input:    "LANDSCAPE",
			expected: "landscape",
		},
		{
			name:     "category tag",
			input:    "adult_content",
			expected: "adult-content",
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

func TestExtractTextFromImage(t *testing.T) {
	tests := []struct {
		name           string
		responseText   string
		expectedText   string
		expectError    bool
		statusCode     int
	}{
		{
			name:         "successful text extraction",
			responseText: "This is extracted text from the image",
			expectedText: "This is extracted text from the image",
			expectError:  false,
			statusCode:   http.StatusOK,
		},
		{
			name:         "empty image (no text)",
			responseText: "",
			expectedText: "",
			expectError:  false,
			statusCode:   http.StatusOK,
		},
		{
			name:         "text with leading/trailing whitespace",
			responseText: "  \n\n  Text with whitespace  \n  ",
			expectedText: "Text with whitespace",
			expectError:  false,
			statusCode:   http.StatusOK,
		},
		{
			name:         "multiline text extraction",
			responseText: "Line 1\nLine 2\nLine 3",
			expectedText: "Line 1\nLine 2\nLine 3",
			expectError:  false,
			statusCode:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify it's a vision request with images
				var req models.OllamaVisionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("Failed to decode vision request: %v", err)
				}

				if len(req.Images) == 0 {
					t.Error("Expected images in request")
				}

				// Verify prompt contains OCR instructions
				if req.Prompt == "" {
					t.Error("Expected non-empty prompt")
				}

				// Return response
				resp := models.OllamaResponse{
					Response: tt.responseText,
					Done:     true,
				}
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			client := NewClient(server.URL, "test-model")
			ctx := context.Background()

			imageData := []byte("fake image data for OCR test")
			result, err := client.ExtractTextFromImage(ctx, imageData)

			if tt.expectError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if result != tt.expectedText {
				t.Errorf("Expected extracted text %q, got %q", tt.expectedText, result)
			}
		})
	}
}

func TestExtractTextFromImageError(t *testing.T) {
	// Create a test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Vision model error"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx := context.Background()

	imageData := []byte("fake image data")
	_, err := client.ExtractTextFromImage(ctx, imageData)

	if err == nil {
		t.Error("Expected error from vision model failure, got nil")
	}
}

func TestExtractTextFromImageContextCancellation(t *testing.T) {
	// Create a test server that delays
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		resp := models.OllamaResponse{
			Response: "Too late",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	imageData := []byte("fake image data")
	_, err := client.ExtractTextFromImage(ctx, imageData)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}
