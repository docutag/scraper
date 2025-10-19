package ollama

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zombar/scraper/models"
)

const (
	DefaultBaseURL = "http://localhost:11434"
	DefaultModel   = "llama3.2"
	DefaultTimeout = 120 * time.Second
)

// Client is a client for interacting with Ollama
type Client struct {
	baseURL    string
	httpClient *http.Client
	model      string
}

// NewClient creates a new Ollama client
func NewClient(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		model: model,
	}
}

// Generate sends a text generation request to Ollama
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	reqBody := models.OllamaRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp models.OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return ollamaResp.Response, nil
}

// GenerateWithVision sends a vision request to Ollama with an image
func (c *Client) GenerateWithVision(ctx context.Context, prompt string, imageData []byte) (string, error) {
	// Base64 encode the image
	encodedImage := base64.StdEncoding.EncodeToString(imageData)

	reqBody := models.OllamaVisionRequest{
		Model:  c.model,
		Prompt: prompt,
		Images: []string{encodedImage},
		Stream: false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp models.OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return ollamaResp.Response, nil
}

// ExtractContent uses Ollama to extract meaningful content from HTML text
func (c *Client) ExtractContent(ctx context.Context, rawText string) (string, error) {
	prompt := fmt.Sprintf(`You are a content extraction assistant. Given the following text extracted from a webpage, identify and return ONLY the meaningful human-readable content. Remove advertisements, navigation menus, footers, cookie notices, social media widgets, and other non-essential elements.

Return only the main content that a human would want to read. Do not add any commentary or explanations.

Text:
%s

Extracted content:`, rawText)

	return c.Generate(ctx, prompt)
}

// AnalyzeImage uses Ollama vision to generate a summary and tags for an image
func (c *Client) AnalyzeImage(ctx context.Context, imageData []byte, altText string) (summary string, tags []string, err error) {
	prompt := `Analyze this image and provide:
1. A 4-5 sentence summary describing what you see
2. A list of up to 10 relevant tags for categorizing the image

Tag formatting rules:
- Prefer single-word tags whenever possible
- Multi-word tags should use hyphens only (no spaces or underscores)
- Names of people, places, and things make excellent tags
- All tags should be lowercase
- Examples: "landscape", "urban-architecture", "golden-gate-bridge", "sunset", "picasso"

Format your response as JSON with the following structure:
{
  "summary": "Your 4-5 sentence description here",
  "tags": ["tag1", "tag2", "tag3"]
}`

	if altText != "" {
		prompt += fmt.Sprintf("\n\nImage alt text (may provide context): %s", altText)
	}

	response, err := c.GenerateWithVision(ctx, prompt, imageData)
	if err != nil {
		return "", nil, fmt.Errorf("failed to analyze image: %w", err)
	}

	// Strip markdown code blocks if present
	response = stripMarkdownCodeBlocks(response)

	// Parse JSON response
	var result struct {
		Summary string   `json:"summary"`
		Tags    []string `json:"tags"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		// If JSON parsing fails, try to extract manually
		return response, []string{}, nil
	}

	// Normalize tags
	for i, tag := range result.Tags {
		result.Tags[i] = normalizeTag(tag)
	}

	return result.Summary, result.Tags, nil
}

// normalizeTag normalizes a tag according to the tagging rules:
// - Converts to lowercase
// - Replaces spaces and underscores with hyphens
// - Removes multiple consecutive hyphens
// - Trims leading/trailing hyphens and whitespace
func normalizeTag(tag string) string {
	// Convert to lowercase
	tag = strings.ToLower(tag)

	// Replace spaces and underscores with hyphens
	tag = strings.ReplaceAll(tag, " ", "-")
	tag = strings.ReplaceAll(tag, "_", "-")

	// Remove multiple consecutive hyphens
	for strings.Contains(tag, "--") {
		tag = strings.ReplaceAll(tag, "--", "-")
	}

	// Trim leading/trailing hyphens and whitespace
	tag = strings.Trim(tag, "- \t\n\r")

	return tag
}

// stripMarkdownCodeBlocks removes markdown code block wrappers from a string
// This handles cases like ```json\n{...}\n``` and returns just the {...} content
func stripMarkdownCodeBlocks(s string) string {
	// Trim whitespace
	s = string(bytes.TrimSpace([]byte(s)))

	// Check if string starts with markdown code block
	if len(s) > 3 && s[:3] == "```" {
		// Find the end of the opening ```[language] line
		lines := bytes.Split([]byte(s), []byte("\n"))
		if len(lines) > 2 {
			// Remove first line (```json or similar) and last line (```)
			result := bytes.Join(lines[1:len(lines)-1], []byte("\n"))
			return string(bytes.TrimSpace(result))
		}
	}

	return s
}

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ScoreContent analyzes content and assigns a quality score for ingestion
// Returns a score (0.0-1.0), reason, categories, and malicious indicators
func (c *Client) ScoreContent(ctx context.Context, url string, title string, content string) (score float64, reason string, categories []string, maliciousIndicators []string, err error) {
	prompt := fmt.Sprintf(`You are a content quality assessment assistant. Analyze the following webpage and determine if it should be ingested into a knowledge database.

URL: %s
Title: %s
Content Preview: %s

Evaluate the content and assign a quality score from 0.0 to 1.0 where:
- 1.0 = High quality, substantive content (articles, research, documentation, guides)
- 0.5-0.9 = Moderate quality content
- 0.0-0.4 = Low quality or inappropriate content

REJECT (score 0.0-0.3) the following types of content:
- Social media platforms (Facebook, Twitter, Instagram, LinkedIn, TikTok, Reddit, etc.)
- Gambling websites (casinos, betting, lottery, poker sites)
- Adult content / pornography
- Drug marketplaces or illegal substance promotion
- Forums and chatrooms (except high-quality technical forums like Stack Overflow)
- General marketplaces (eBay, Amazon product pages, Craigslist, etc.)
- Spam, clickbait, or misleading content
- Malicious websites (phishing, malware distribution, scams)
- Paywalled content with no preview
- Login/signup walls with no content
- Pure advertisement pages

ACCEPT (score 0.7-1.0) the following types of content:
- News articles and journalism
- Educational content and tutorials
- Research papers and academic content
- Technical documentation
- Blog posts with substantive content
- Government and official resources
- Non-profit and educational institutions
- Business websites with informative content

Provide your assessment in JSON format:
{
  "score": 0.0-1.0,
  "reason": "Brief explanation of the score",
  "categories": ["category1", "category2"],
  "malicious_indicators": ["indicator1", "indicator2"]
}

Categories should include any applicable labels: "social_media", "gambling", "adult_content", "drugs", "forum", "marketplace", "spam", "malicious", "news", "education", "technical", "business", etc.

Malicious indicators should list any suspicious patterns detected: "phishing", "malware", "scam", "misleading", etc.`,
		url,
		truncateString(title, 200),
		truncateString(content, 1000))

	response, err := c.Generate(ctx, prompt)
	if err != nil {
		return 0.0, "", nil, nil, fmt.Errorf("failed to score content: %w", err)
	}

	// Strip markdown code blocks if present
	response = stripMarkdownCodeBlocks(response)

	// Parse JSON response
	var result struct {
		Score               float64  `json:"score"`
		Reason              string   `json:"reason"`
		Categories          []string `json:"categories"`
		MaliciousIndicators []string `json:"malicious_indicators"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return 0.0, "", nil, nil, fmt.Errorf("failed to parse scoring response: %w", err)
	}

	// Ensure score is within bounds
	if result.Score < 0.0 {
		result.Score = 0.0
	}
	if result.Score > 1.0 {
		result.Score = 1.0
	}

	// Ensure slices are not nil
	if result.Categories == nil {
		result.Categories = []string{}
	}
	if result.MaliciousIndicators == nil {
		result.MaliciousIndicators = []string{}
	}

	// Normalize all categories
	for i, category := range result.Categories {
		result.Categories[i] = normalizeTag(category)
	}

	return result.Score, result.Reason, result.Categories, result.MaliciousIndicators, nil
}
