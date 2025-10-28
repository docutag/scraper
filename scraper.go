package scraper

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // Register GIF format
	_ "image/jpeg" // Register JPEG format
	_ "image/png"  // Register PNG format
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	exif "github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/webp" // Register WebP format
	"github.com/zombar/scraper/models"
	"github.com/zombar/scraper/ollama"
	"github.com/zombar/scraper/slug"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/html"
)

const (
	// UserAgent mimics a recent Chrome browser to avoid being blocked
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// Config contains scraper configuration
type Config struct {
	HTTPTimeout         time.Duration
	OllamaBaseURL       string
	OllamaModel         string
	OllamaVisionModel   string        // Separate model for vision tasks (can be same as OllamaModel)
	EnableImageAnalysis bool          // Enable AI-powered image analysis
	MaxImageSizeBytes   int64         // Maximum image size to download (bytes)
	ImageTimeout        time.Duration // Timeout for downloading individual images
	LinkScoreThreshold  float64       // Minimum score for link to be recommended (0.0-1.0)
	StoragePath         string        // Base path for filesystem storage
	MaxImages           int           // Maximum number of images to download per scrape (0 = unlimited)
}

// DefaultConfig returns default scraper configuration
func DefaultConfig() Config {
	return Config{
		HTTPTimeout:         30 * time.Second,
		OllamaBaseURL:       ollama.DefaultBaseURL,
		OllamaModel:         ollama.DefaultModel,
		OllamaVisionModel:   ollama.DefaultModel, // Default to same model as text
		EnableImageAnalysis: true,                // Enable image analysis by default
		MaxImageSizeBytes:   10 * 1024 * 1024,    // 10MB max image size
		ImageTimeout:        15 * time.Second,    // 15s timeout per image
		LinkScoreThreshold:  0.5,                 // Default threshold for link scoring
		StoragePath:         "./storage",         // Default storage path
		MaxImages:           20,                  // Download max 20 images per scrape
	}
}

// DB interface defines the database operations needed by the scraper
type DB interface {
	GetImageByURL(url string) (*models.ImageInfo, error)
}

// Scraper handles web scraping operations
type Scraper struct {
	config          Config
	httpClient      *http.Client
	ollamaClient    *ollama.Client
	ollamaSemaphore chan struct{} // Semaphore to limit concurrent Ollama requests
	db              DB            // Database for checking existing images
	storage         Storage       // Filesystem storage for images and content
}

// Storage interface defines the storage operations needed by the scraper
type Storage interface {
	SaveImage(imageData []byte, slug, contentType string) (string, error)
	SaveContent(content, slug string) (string, error)
	ReadImage(relPath string) ([]byte, error)
	ReadContent(relPath string) (string, error)
	DeleteImage(relPath string) error
	DeleteContent(relPath string) error
}

// New creates a new Scraper instance
// db parameter can be nil if image deduplication is not needed
// storage parameter can be nil if filesystem storage is not needed
func New(config Config, db DB, storage Storage) *Scraper {
	// Limit concurrent Ollama requests to 3 to prevent overload during batch operations
	maxConcurrentOllamaRequests := 3

	// Create HTTP client with HTTP/1.1 only (disable HTTP/2)
	// Some servers have issues with HTTP/2 from Go clients
	transport := &http.Transport{
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}

	// Wrap transport with OpenTelemetry instrumentation for trace propagation
	instrumentedTransport := otelhttp.NewTransport(transport,
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return "scraper HTTP " + r.Method + " " + r.URL.Host
		}),
	)

	return &Scraper{
		config: config,
		httpClient: &http.Client{
			Timeout:   config.HTTPTimeout,
			Transport: instrumentedTransport,
		},
		ollamaClient:    ollama.NewClientWithVisionModel(config.OllamaBaseURL, config.OllamaModel, config.OllamaVisionModel),
		ollamaSemaphore: make(chan struct{}, maxConcurrentOllamaRequests),
		db:              db,
		storage:         storage,
	}
}

// acquireOllamaSlot acquires a slot in the Ollama semaphore or returns error if context is cancelled
func (s *Scraper) acquireOllamaSlot(ctx context.Context) error {
	select {
	case s.ollamaSemaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseOllamaSlot releases a slot in the Ollama semaphore
func (s *Scraper) releaseOllamaSlot() {
	<-s.ollamaSemaphore
}

// Config returns the scraper configuration
func (s *Scraper) Config() Config {
	return s.config
}

// OllamaClient returns the Ollama client for external use
func (s *Scraper) OllamaClient() *ollama.Client {
	return s.ollamaClient
}

// Scrape fetches and processes a URL
func (s *Scraper) Scrape(ctx context.Context, targetURL string) (*models.ScrapedData, error) {
	start := time.Now()
	warnings := []string{} // Track non-fatal processing issues

	// Validate URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("URL must be http or https")
	}

	// Check if this is a direct image URL - create minimal HTML instead of fetching
	var doc *html.Node
	if isImageURL(targetURL) {
		// Create a minimal HTML document with just the image tag
		// This allows all existing image processing code to work as-is
		htmlContent := fmt.Sprintf(`<html><body><img src="%s" alt="Direct image"></body></html>`, targetURL)
		doc, err = html.Parse(strings.NewReader(htmlContent))
		if err != nil {
			return nil, fmt.Errorf("failed to create HTML for image: %w", err)
		}
	} else {
		// Fetch the page normally
		req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("User-Agent", UserAgent)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch URL: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
		}

		// Parse HTML
		doc, err = html.Parse(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to parse HTML: %w", err)
		}
	}

	// Extract title
	title := extractTitle(doc)
	if title == "" {
		title = targetURL
	}

	// Extract text content
	textContent := extractText(doc)

	// Use Ollama to extract meaningful content
	content := textContent // Default to raw text
	if err := s.acquireOllamaSlot(ctx); err == nil {
		extractedContent, err := s.ollamaClient.ExtractContent(ctx, textContent)
		s.releaseOllamaSlot()
		if err != nil {
			slog.Warn("ollama content extraction failed, using raw text", "url", targetURL, "error", err)
			warnings = append(warnings, "AI content extraction unavailable, using raw text")
		} else {
			content = extractedContent
		}
	} else {
		slog.Warn("context cancelled while waiting for ollama slot", "operation", "content_extraction", "error", err)
		warnings = append(warnings, "Content extraction timed out, using raw text")
	}

	// Extract images
	images := extractImages(doc, parsedURL)

	// Process images (download and analyze if enabled)
	images, existingImageRefs, imageWarnings := s.processImages(ctx, images)
	warnings = append(warnings, imageWarnings...)

	// For direct image URLs, use the image's AI-generated summary and tags
	isDirectImageURL := isImageURL(targetURL)
	if isDirectImageURL && len(images) > 0 {
		img := images[0]
		// Use image summary as content
		if img.Summary != "" {
			content = img.Summary
		}
		// Generate a better title from the image summary or filename
		if img.Summary != "" {
			// Use first sentence of summary as title, or first 60 chars
			summaryTitle := img.Summary
			if len(summaryTitle) > 60 {
				summaryTitle = summaryTitle[:60] + "..."
			}
			// Find first sentence
			if idx := strings.Index(summaryTitle, "."); idx > 0 && idx < 60 {
				summaryTitle = summaryTitle[:idx]
			}
			title = summaryTitle
		} else {
			// Fallback to filename
			if idx := strings.LastIndex(parsedURL.Path, "/"); idx >= 0 {
				title = parsedURL.Path[idx+1:]
			}
		}
	}

	// Extract links with Ollama sanitization
	links := s.extractLinksWithOllama(ctx, doc, parsedURL, title, content)

	// Extract metadata
	metadata := extractMetadata(doc)

	// Add existing image references to metadata
	if len(existingImageRefs) > 0 {
		metadata.ExistingImageRefs = existingImageRefs
	}

	// For direct image URLs, add image tags to metadata keywords
	if isDirectImageURL && len(images) > 0 {
		img := images[0]
		if len(img.Tags) > 0 {
			metadata.Keywords = img.Tags
		}
	}

	// Score the content (with fallback to rule-based scoring)
	var score float64
	var reason string
	var categories []string
	var maliciousIndicators []string
	var aiUsed bool

	// Check for low-quality patterns first (before Ollama) to avoid unnecessary AI calls
	shouldSkipAI, earlyScore, earlyReason, earlyCategories, earlyIndicators := checkForLowQualityPatterns(targetURL, title)
	if shouldSkipAI {
		score = earlyScore
		reason = earlyReason
		categories = earlyCategories
		maliciousIndicators = earlyIndicators
		aiUsed = false
	} else if err := s.acquireOllamaSlot(ctx); err == nil {
		var err error
		score, reason, categories, maliciousIndicators, err = s.ollamaClient.ScoreContent(ctx, targetURL, title, content)
		s.releaseOllamaSlot()
		if err != nil {
			// Fallback to rule-based scoring when Ollama fails
			slog.Warn("ollama scoring failed, using rule-based fallback", "url", targetURL, "error", err)
			score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, content)
			aiUsed = false
			warnings = append(warnings, "AI scoring unavailable, using rule-based scoring")
		} else {
			aiUsed = true
		}
	} else {
		// Context cancelled, use rule-based fallback
		slog.Warn("context cancelled while waiting for ollama slot", "operation", "scoring", "error", err)
		score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, content)
		aiUsed = false
		warnings = append(warnings, "Scoring timed out, using rule-based scoring")
	}

	// For direct image URLs, add image tags to categories
	if isDirectImageURL && len(images) > 0 {
		img := images[0]
		if len(img.Tags) > 0 {
			// Combine existing categories with image tags (deduplicated)
			tagMap := make(map[string]bool)
			for _, cat := range categories {
				tagMap[cat] = true
			}
			for _, tag := range img.Tags {
				tagMap[tag] = true
			}
			// Convert back to slice
			combinedCategories := make([]string, 0, len(tagMap))
			for tag := range tagMap {
				combinedCategories = append(combinedCategories, tag)
			}
			categories = combinedCategories
		}
	}

	linkScore := &models.LinkScore{
		URL:                 targetURL,
		Score:               score,
		Reason:              reason,
		Categories:          categories,
		IsRecommended:       score >= s.config.LinkScoreThreshold,
		MaliciousIndicators: maliciousIndicators,
		AIUsed:              aiUsed,
	}

	// Score images for relevance (for thumbnail selection)
	if len(images) > 0 {
		for i := range images {
			images[i].RelevanceScore = scoreImageRelevance(images[i], i, len(images), title, categories)
			slog.Info("image relevance score",
				"index", i,
				"score", images[i].RelevanceScore,
				"width", images[i].Width,
				"height", images[i].Height,
				"tags", images[i].Tags)
		}
	}

	// Generate slug from title
	contentSlug := slug.GenerateWithFallback(title, targetURL)

	// Create scraped data
	data := &models.ScrapedData{
		ID:             uuid.New().String(),
		URL:            targetURL,
		Title:          title,
		Content:        content,
		RawText:        textContent, // Always store original raw text
		Images:         images,
		Links:          links,
		FetchedAt:      time.Now(),
		CreatedAt:      time.Now(),
		ProcessingTime: time.Since(start).Seconds(),
		Cached:         false,
		Metadata:       metadata,
		Score:          linkScore,
		Warnings:       warnings,
		Slug:           contentSlug,
	}

	// Save content to filesystem if storage is available
	if s.storage != nil && content != "" {
		// Create simple HTML wrapper for content
		htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<title>%s</title>
	<meta name="description" content="%s">
</head>
<body>
	<h1>%s</h1>
	<div>%s</div>
</body>
</html>`, title, metadata.Description, title, content)

		_, err := s.storage.SaveContent(htmlContent, contentSlug)
		if err != nil {
			slog.Error("failed to save content to filesystem", "url", targetURL, "error", err)
			warnings = append(warnings, "Failed to save content to filesystem")
			data.Warnings = warnings
		}
	}

	return data, nil
}

// ExtractLinks fetches a URL and returns links using Ollama with fallback to basic extraction
func (s *Scraper) ExtractLinks(ctx context.Context, targetURL string) ([]string, error) {
	// Validate URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("URL must be http or https")
	}

	// Fetch the page
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Parse HTML
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Extract title
	title := extractTitle(doc)
	if title == "" {
		title = targetURL
	}

	// Extract text content
	textContent := extractText(doc)

	// Use Ollama to extract meaningful content
	content := textContent // Default to raw text
	if err := s.acquireOllamaSlot(ctx); err == nil {
		extractedContent, err := s.ollamaClient.ExtractContent(ctx, textContent)
		s.releaseOllamaSlot()
		if err != nil {
			slog.Warn("ollama content extraction failed, using raw text", "url", targetURL, "error", err)
		} else {
			content = extractedContent
		}
	} else {
		slog.Warn("context cancelled while waiting for ollama slot", "operation", "content_extraction", "error", err)
	}

	// Extract links with Ollama sanitization and fallback
	links := s.extractLinksWithOllama(ctx, doc, parsedURL, title, content)

	return links, nil
}

// extractTitle extracts the page title from the HTML
// Priority: og:title > twitter:title > h1 > title tag
func extractTitle(n *html.Node) string {
	var ogTitle, twitterTitle, h1Title, htmlTitle string

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "meta":
				var property, name, content string
				for _, attr := range n.Attr {
					switch attr.Key {
					case "property":
						property = strings.ToLower(attr.Val)
					case "name":
						name = strings.ToLower(attr.Val)
					case "content":
						content = attr.Val
					}
				}
				if property == "og:title" && ogTitle == "" {
					ogTitle = content
				} else if name == "twitter:title" && twitterTitle == "" {
					twitterTitle = content
				}
			case "h1":
				if h1Title == "" && n.FirstChild != nil {
					h1Title = extractTextFromNode(n)
				}
			case "title":
				if htmlTitle == "" && n.FirstChild != nil {
					htmlTitle = n.FirstChild.Data
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)

	// Return first available title in priority order
	if ogTitle != "" {
		return strings.TrimSpace(ogTitle)
	}
	if twitterTitle != "" {
		return strings.TrimSpace(twitterTitle)
	}
	if h1Title != "" {
		return strings.TrimSpace(h1Title)
	}
	return strings.TrimSpace(htmlTitle)
}

// extractTextFromNode extracts all text content from a single node and its children
func extractTextFromNode(n *html.Node) string {
	var parts []string
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			trimmed := strings.TrimSpace(n.Data)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.Join(parts, " ")
}

// extractText extracts all text content from the HTML
func extractText(n *html.Node) string {
	var buf strings.Builder

	// Block-level elements that should add line breaks
	blockElements := map[string]bool{
		"p": true, "div": true, "br": true, "h1": true, "h2": true, "h3": true,
		"h4": true, "h5": true, "h6": true, "li": true, "tr": true, "td": true,
		"th": true, "article": true, "section": true, "blockquote": true,
		"pre": true, "ul": true, "ol": true, "dl": true, "dt": true, "dd": true,
		"header": true, "footer": true, "nav": true, "aside": true, "main": true,
	}

	var f func(*html.Node, bool)
	f = func(n *html.Node, afterBlock bool) {
		// Skip script and style tags entirely
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}

		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				// Add space before text if buffer has content and doesn't end with whitespace
				if buf.Len() > 0 {
					lastChar := buf.String()[buf.Len()-1]
					if lastChar != '\n' && lastChar != ' ' {
						buf.WriteString(" ")
					}
				}
				buf.WriteString(text)
			}
		}

		// Process children
		isBlockElement := n.Type == html.ElementNode && blockElements[n.Data]
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c, isBlockElement)
		}

		// Add newline after block elements
		if isBlockElement && buf.Len() > 0 {
			// Avoid adding multiple consecutive newlines
			str := buf.String()
			if !strings.HasSuffix(str, "\n\n") {
				if strings.HasSuffix(str, "\n") {
					buf.WriteString("\n")
				} else {
					buf.WriteString("\n\n")
				}
			}
		}
	}

	f(n, false)

	// Clean up excessive newlines and trim
	text := buf.String()
	// Replace 3+ newlines with just 2 (preserve paragraph breaks but no more)
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}

// extractImages extracts image information from the HTML
func extractImages(n *html.Node, baseURL *url.URL) []models.ImageInfo {
	var images []models.ImageInfo
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			var src, alt string
			var width, height int
			for _, attr := range n.Attr {
				switch attr.Key {
				case "src":
					src = attr.Val
				case "alt":
					alt = attr.Val
				case "width":
					// Try to parse width attribute (may be in pixels or other units)
					if w, err := strconv.Atoi(strings.TrimSpace(attr.Val)); err == nil && w > 0 {
						width = w
					}
				case "height":
					// Try to parse height attribute (may be in pixels or other units)
					if h, err := strconv.Atoi(strings.TrimSpace(attr.Val)); err == nil && h > 0 {
						height = h
					}
				}
			}
			if src != "" {
				// Resolve relative URLs
				if imgURL, err := resolveURL(baseURL, src); err == nil {
					images = append(images, models.ImageInfo{
						URL:     imgURL,
						AltText: alt,
						Width:   width,  // HTML attribute hint (0 if not specified)
						Height:  height, // HTML attribute hint (0 if not specified)
						Summary: "",
						Tags:    []string{},
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return images
}

// extractLinksWithOllama extracts links from HTML and uses Ollama to sanitize them
func (s *Scraper) extractLinksWithOllama(ctx context.Context, n *html.Node, baseURL *url.URL, pageTitle string, pageContent string) []string {
	// First extract all links using the basic method
	allLinks := extractLinks(n, baseURL)

	// Ensure we always return a non-nil slice
	if allLinks == nil {
		allLinks = []string{}
	}

	if len(allLinks) == 0 {
		return allLinks
	}

	// Pre-filter links using pattern matching before AI
	filteredLinks, filteredCount := filterLowQualityLinks(allLinks)
	slog.Info("link extraction complete",
		"total_links", len(allLinks),
		"filtered_out", filteredCount,
		"remaining", len(filteredLinks))

	// If we have no links remaining, return early
	if len(filteredLinks) == 0 {
		return filteredLinks
	}

	// Skip AI only if:
	// 1. We have very few links (≤3) after filtering, AND
	// 2. We filtered out at least 50% of the original links (meaning our patterns worked well)
	// This ensures we still use AI when needed for intelligent filtering
	if len(filteredLinks) <= 3 && filteredCount > 0 && float64(filteredCount)/float64(len(allLinks)) >= 0.5 {
		slog.Info("skipping AI link extraction",
			"clean_links", len(filteredLinks),
			"skipped", filteredCount,
			"total", len(allLinks))
		return filteredLinks
	}

	// For pages with many links (like news sites), use AI to further refine
	linksJSON, err := json.Marshal(filteredLinks)
	if err != nil {
		// If marshaling fails, fall back to returning filtered links
		return filteredLinks
	}

	prompt := fmt.Sprintf(`You are a link filtering assistant. Given a list of URLs extracted from a webpage, identify and return ONLY the links that point to substantive content (articles, blog posts, reports, etc.).

INCLUDE:
- Article links (news stories, blog posts, features)
- Opinion pieces and editorials
- Reports, guides, and documentation
- Individual story/content pages
- Links to specific multimedia content (videos, podcasts with their own pages)

EXCLUDE:
- Advertising/sponsored content links
- Site navigation (home, sections, categories, topics)
- Social media share/follow buttons
- Login/signup/account links
- Footer links (privacy, terms, about, contact, jobs, press)
- Newsletter/subscription prompts
- Cookie/consent notices
- Generic section/category/tag pages (unless they're the main content)
- Search functionality links
- Pagination controls (next, previous, page numbers)
- Internal site tools (print, save, bookmark)
- Related external sites/sister publications
- Comment section links

IMPORTANT: If this is a homepage or news aggregator page, it will contain MANY article links - these should ALL be included as they are the primary content. Only filter out the navigation chrome around them.

Page Title: %s

Page Content: %s

Links to filter:
%s

Return ONLY a JSON array of the filtered URLs. Do not include any explanation or commentary.
Format: ["url1", "url2", "url3"]`,
		pageTitle,
		pageContent,
		string(linksJSON))

	// Use Ollama with semaphore protection
	sanitizedLinks := filteredLinks // Default to filtered links
	if err := s.acquireOllamaSlot(ctx); err == nil {
		response, err := s.ollamaClient.Generate(ctx, prompt)
		s.releaseOllamaSlot()
		if err != nil {
			// If Ollama fails, fall back to returning filtered links
			slog.Warn("ollama link sanitization failed, using filtered links", "error", err)
		} else {
			// Strip markdown code blocks if present
			cleanedResponse := ollama.StripMarkdownCodeBlocks(response)

			// Parse JSON response
			var parsedLinks []string
			if err := json.Unmarshal([]byte(cleanedResponse), &parsedLinks); err != nil {
				// If parsing fails, fall back to returning filtered links
				slog.Warn("failed to parse ollama link response", "error", err)
				slog.Warn("ollama response", "response", response)
			} else if parsedLinks != nil {
				sanitizedLinks = parsedLinks
				slog.Info("AI refined links", "before", len(filteredLinks), "after", len(sanitizedLinks))
			}
		}
	} else {
		slog.Warn("context cancelled while waiting for ollama slot", "operation", "link_sanitization", "error", err)
	}

	// Ensure we never return nil
	if sanitizedLinks == nil {
		sanitizedLinks = []string{}
	}

	// Deduplicate the final list while preserving order
	sanitizedLinks = deduplicateLinks(sanitizedLinks)

	return sanitizedLinks
}

// extractLinks extracts links from the HTML
func extractLinks(n *html.Node, baseURL *url.URL) []string {
	var links []string
	seen := make(map[string]bool)
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && attr.Val != "" {
					// Resolve relative URLs
					if linkURL, err := resolveURL(baseURL, attr.Val); err == nil {
						if !seen[linkURL] {
							seen[linkURL] = true
							links = append(links, linkURL)
						}
					}
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return links
}

// extractMetadata extracts page metadata from meta tags
func extractMetadata(n *html.Node) models.PageMetadata {
	metadata := models.PageMetadata{}
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			var name, property, content string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "name":
					name = strings.ToLower(attr.Val)
				case "property":
					property = strings.ToLower(attr.Val)
				case "content":
					content = attr.Val
				}
			}

			if content == "" {
				return
			}

			switch {
			case name == "description" || property == "og:description":
				if metadata.Description == "" {
					metadata.Description = content
				}
			case name == "keywords":
				if len(metadata.Keywords) == 0 {
					keywords := strings.Split(content, ",")
					for _, kw := range keywords {
						metadata.Keywords = append(metadata.Keywords, strings.TrimSpace(kw))
					}
				}
			case name == "author" || property == "article:author":
				if metadata.Author == "" {
					metadata.Author = content
				}
			case property == "article:published_time":
				if metadata.PublishedDate == "" {
					metadata.PublishedDate = content
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return metadata
}

// downloadImage downloads an image from a URL with size and timeout limits
func (s *Scraper) downloadImage(ctx context.Context, imageURL string) ([]byte, string, error) {
	// Create request with timeout context
	ctx, cancel := context.WithTimeout(ctx, s.config.ImageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Check content length if available
	if resp.ContentLength > s.config.MaxImageSizeBytes {
		return nil, "", fmt.Errorf("image too large: %d bytes (max: %d)", resp.ContentLength, s.config.MaxImageSizeBytes)
	}

	// Get content type from response
	contentType := resp.Header.Get("Content-Type")

	// Read with size limit
	limitedReader := io.LimitReader(resp.Body, s.config.MaxImageSizeBytes+1)
	imageData, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Check if we exceeded the limit
	if int64(len(imageData)) > s.config.MaxImageSizeBytes {
		return nil, "", fmt.Errorf("image too large: exceeds %d bytes", s.config.MaxImageSizeBytes)
	}

	return imageData, contentType, nil
}

// getImageDimensions extracts width and height from image data
// Returns (width, height, error)
func getImageDimensions(imageData []byte) (int, int, error) {
	// Decode image to get dimensions
	img, _, err := image.DecodeConfig(bytes.NewReader(imageData))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to decode image: %w", err)
	}
	return img.Width, img.Height, nil
}

// extractEXIF extracts EXIF metadata from image data
// Returns nil if no EXIF data is present (not an error)
func extractEXIF(imageData []byte) *models.EXIFData {
	x, err := exif.Decode(bytes.NewReader(imageData))
	if err != nil {
		// No EXIF data or unable to decode - not an error, many images don't have EXIF
		return nil
	}

	exifData := &models.EXIFData{}

	// Extract DateTime
	if dt, err := x.DateTime(); err == nil {
		exifData.DateTime = dt.Format(time.RFC3339)
	}

	// Extract DateTimeOriginal
	if tag, err := x.Get(exif.DateTimeOriginal); err == nil {
		if dtStr, err := tag.StringVal(); err == nil {
			// Parse EXIF datetime format "2006:01:02 15:04:05"
			if dt, err := time.Parse("2006:01:02 15:04:05", dtStr); err == nil {
				exifData.DateTimeOriginal = dt.Format(time.RFC3339)
			}
		}
	}

	// Extract Make (camera manufacturer)
	if tag, err := x.Get(exif.Make); err == nil {
		if val, err := tag.StringVal(); err == nil {
			exifData.Make = strings.TrimSpace(val)
		}
	}

	// Extract Model (camera model)
	if tag, err := x.Get(exif.Model); err == nil {
		if val, err := tag.StringVal(); err == nil {
			exifData.Model = strings.TrimSpace(val)
		}
	}

	// Extract Copyright
	if tag, err := x.Get(exif.Copyright); err == nil {
		if val, err := tag.StringVal(); err == nil {
			exifData.Copyright = strings.TrimSpace(val)
		}
	}

	// Extract Artist
	if tag, err := x.Get(exif.Artist); err == nil {
		if val, err := tag.StringVal(); err == nil {
			exifData.Artist = strings.TrimSpace(val)
		}
	}

	// Extract Software
	if tag, err := x.Get(exif.Software); err == nil {
		if val, err := tag.StringVal(); err == nil {
			exifData.Software = strings.TrimSpace(val)
		}
	}

	// Extract ImageDescription
	if tag, err := x.Get(exif.ImageDescription); err == nil {
		if val, err := tag.StringVal(); err == nil {
			exifData.ImageDescription = strings.TrimSpace(val)
		}
	}

	// Extract Orientation
	if tag, err := x.Get(exif.Orientation); err == nil {
		if val, err := tag.Int(0); err == nil {
			exifData.Orientation = val
		}
	}

	// Extract GPS data
	lat, lon, err := x.LatLong()
	if err == nil {
		exifData.GPS = &models.GPSData{
			Latitude:  lat,
			Longitude: lon,
		}

		// Extract altitude if available
		if tag, err := x.Get(exif.GPSAltitude); err == nil {
			if alt, err := tag.Rat(0); err == nil {
				// Altitude is stored as a rational number
				altitude, _ := alt.Float64()
				exifData.GPS.Altitude = altitude
			}
		}
	}

	return exifData
}

// shouldSkipImage determines if an image should be skipped based on URL patterns
// Returns true if the image appears to be a placeholder, temp file, UI component, or other junk data
func shouldSkipImage(imageURL string) bool {
	urlLower := strings.ToLower(imageURL)

	// Skip images with placeholder or temp in the name
	skipKeywords := []string{
		"placeholder",
		"temp",
		"temporary",
		// UI components
		"icon",
		"logo",
		"button",
		"sprite",
		"avatar-default",
		"default-avatar",
		"generic-avatar",
		// Tracking pixels and spacers
		"1x1",
		"pixel",
		"tracking",
		"spacer",
		"blank",
		"transparent",
		// Social media buttons
		"share-button",
		"facebook-icon",
		"twitter-icon",
		"social-icon",
		// Ads and promotional
		"ad-banner",
		"advertisement",
		"promo",
		// Common junk patterns
		"spinner",
		"loader",
		"loading",
		"thumbnail-placeholder",
	}

	for _, keyword := range skipKeywords {
		if strings.Contains(urlLower, keyword) {
			return true
		}
	}

	return false
}

// scoreImageRelevance calculates a relevance score for an image (0.0-1.0)
// Higher scores indicate images more suitable as article thumbnails
// Lower scores for infographics, charts, banners, and other non-content images
func scoreImageRelevance(img models.ImageInfo, position int, totalImages int, articleTitle string, articleTags []string) float64 {
	score := 0.5 // Start with neutral score

	// Dimension scoring - prefer larger images, but not extreme banners
	if img.Width > 0 && img.Height > 0 {
		// Ideal dimensions: 800-2000px wide, reasonable height
		if img.Width >= 800 && img.Width <= 2000 && img.Height >= 400 {
			score += 0.2
		} else if img.Width >= 400 && img.Width < 800 {
			score += 0.1
		} else if img.Width < 200 || img.Height < 100 {
			score -= 0.2 // Too small, likely icon or thumbnail
		}

		// Aspect ratio - penalize banner-like ratios
		aspectRatio := float64(img.Width) / float64(img.Height)

		// Ideal aspect ratio: 1.5:1 to 2:1 (typical article images)
		if aspectRatio >= 1.3 && aspectRatio <= 2.1 {
			score += 0.1
		} else if aspectRatio > 5.0 || aspectRatio < 0.5 {
			// Extreme ratios (banners, vertical strips)
			score -= 0.3
		} else if aspectRatio > 3.0 || aspectRatio < 0.7 {
			// Moderately extreme ratios
			score -= 0.15
		}

		// Penalize common banner sizes
		if (img.Width == 728 && img.Height == 90) || // Standard banner
			(img.Width == 468 && img.Height == 60) || // Full banner
			(img.Width == 234 && img.Height == 60) || // Half banner
			(img.Width == 120 && img.Height == 600) || // Skyscraper
			(img.Width == 300 && img.Height == 250) || // Medium rectangle
			(img.Width == 336 && img.Height == 280) {   // Large rectangle
			score -= 0.4
		}
	}

	// Position scoring - images earlier in the document are often more relevant
	if totalImages > 0 {
		positionRatio := float64(position) / float64(totalImages)
		if positionRatio <= 0.25 {
			// First quarter of images
			score += 0.2
		} else if positionRatio <= 0.5 {
			// Second quarter
			score += 0.1
		} else if positionRatio > 0.75 {
			// Last quarter - less likely to be main image
			score -= 0.1
		}
	}

	// Tag-based scoring - heavily penalize infographics, charts, diagrams
	for _, tag := range img.Tags {
		tagLower := strings.ToLower(tag)

		// Infographic indicators (large penalty)
		if tagLower == "banner" ||
			strings.Contains(tagLower, "infographic") ||
			strings.Contains(tagLower, "chart") ||
			strings.Contains(tagLower, "diagram") ||
			strings.Contains(tagLower, "graph") ||
			strings.Contains(tagLower, "flowchart") ||
			strings.Contains(tagLower, "visualization") ||
			strings.Contains(tagLower, "data visualization") ||
			strings.Contains(tagLower, "statistics") {
			score -= 0.4
		}

		// UI elements (medium penalty)
		if strings.Contains(tagLower, "icon") ||
			strings.Contains(tagLower, "logo") ||
			strings.Contains(tagLower, "button") ||
			strings.Contains(tagLower, "avatar") {
			score -= 0.3
		}

		// Positive indicators (photos, people, places)
		if strings.Contains(tagLower, "photo") ||
			strings.Contains(tagLower, "photograph") ||
			strings.Contains(tagLower, "portrait") ||
			strings.Contains(tagLower, "landscape") ||
			strings.Contains(tagLower, "person") ||
			strings.Contains(tagLower, "people") {
			score += 0.1
		}
	}

	// Alt text relevance - bonus if alt text matches article title or tags
	if img.AltText != "" && articleTitle != "" {
		altLower := strings.ToLower(img.AltText)
		titleLower := strings.ToLower(articleTitle)

		// Check if alt text contains words from title
		titleWords := strings.Fields(titleLower)
		matchCount := 0
		for _, word := range titleWords {
			if len(word) > 3 && strings.Contains(altLower, word) {
				matchCount++
			}
		}
		if matchCount > 2 {
			score += 0.15
		} else if matchCount > 0 {
			score += 0.05
		}

		// Check if alt text matches any tags
		for _, tag := range articleTags {
			if len(tag) > 3 && strings.Contains(altLower, strings.ToLower(tag)) {
				score += 0.05
				break
			}
		}
	}

	// Summary-based scoring - check if summary mentions infographics
	if img.Summary != "" {
		summaryLower := strings.ToLower(img.Summary)
		if strings.Contains(summaryLower, "infographic") ||
			strings.Contains(summaryLower, "chart") ||
			strings.Contains(summaryLower, "diagram") ||
			strings.Contains(summaryLower, "graph") ||
			strings.Contains(summaryLower, "data visualization") {
			score -= 0.3
		}
	}

	// Clamp score to 0.0-1.0 range
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// processImages downloads and analyzes images if image analysis is enabled
// Uses parallel processing with worker pool for better performance
// Returns processed images, existing image references, and any warnings encountered
func (s *Scraper) processImages(ctx context.Context, images []models.ImageInfo) ([]models.ImageInfo, []models.ExistingImageRef, []string) {
	warnings := []string{}
	existingRefs := []models.ExistingImageRef{}

	if !s.config.EnableImageAnalysis {
		slog.Info("image analysis disabled", "image_count", len(images))
		return images, existingRefs, warnings
	}

	if len(images) == 0 {
		return images, existingRefs, warnings
	}

	// Filter out placeholder, temp, UI component, and junk images
	filteredImages := make([]models.ImageInfo, 0, len(images))
	skippedCount := 0
	for _, img := range images {
		if shouldSkipImage(img.URL) {
			slog.Info("skipping junk image", "url", img.URL)
			skippedCount++
		} else {
			filteredImages = append(filteredImages, img)
		}
	}

	if skippedCount > 0 {
		slog.Info("filtered out junk images", "count", skippedCount)
		warnings = append(warnings, fmt.Sprintf("Skipped %d placeholder/temp/UI component images", skippedCount))
	}

	// If all images were filtered out, return early
	if len(filteredImages) == 0 {
		slog.Info("all images filtered as junk", "count", len(images))
		return []models.ImageInfo{}, existingRefs, warnings
	}

	// Use filtered images for processing
	images = filteredImages

	// Apply max images limit if configured
	if s.config.MaxImages > 0 && len(images) > s.config.MaxImages {
		// Sort images by size (pixel area) in descending order to prioritize larger images
		// Images without dimensions (0x0) will sort to the end
		sort.Slice(images, func(i, j int) bool {
			areaI := images[i].Width * images[i].Height
			areaJ := images[j].Width * images[j].Height
			// If both have dimensions, sort by area (largest first)
			if areaI > 0 && areaJ > 0 {
				return areaI > areaJ
			}
			// If only one has dimensions, prefer the one with dimensions
			if areaI > 0 {
				return true
			}
			if areaJ > 0 {
				return false
			}
			// If neither has dimensions, maintain original order (stable)
			return i < j
		})

		slog.Info("sorted and limited images", "total", len(images), "max_images", s.config.MaxImages)
		warnings = append(warnings, fmt.Sprintf("Limited to %d largest images (found %d total)", s.config.MaxImages, len(images)))
		images = images[:s.config.MaxImages]
	}

	// Use a worker pool for parallel image processing
	const maxWorkers = 5
	numWorkers := min(maxWorkers, len(images))

	type imageJob struct {
		index int
		img   models.ImageInfo
	}

	type imageResult struct {
		index       int
		img         models.ImageInfo
		warning     string
		existingRef *models.ExistingImageRef // Non-nil if image already exists in DB
	}

	jobs := make(chan imageJob, len(images))
	results := make(chan imageResult, len(images))

	// Start worker goroutines
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				img, existingRef, warning := s.processSingleImage(ctx, job.img)
				results <- imageResult{index: job.index, img: img, warning: warning, existingRef: existingRef}
			}
		}()
	}

	// Send jobs
	for i, img := range images {
		jobs <- imageJob{index: i, img: img}
	}
	close(jobs)

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and preserve original order
	resultsByIndex := make([]imageResult, len(images))
	for result := range results {
		resultsByIndex[result.index] = result
	}

	// Process results in original order
	processedImages := make([]models.ImageInfo, 0, len(images))
	failedAnalysis := 0
	for _, result := range resultsByIndex {
		if result.existingRef != nil {
			// Image already exists in database, add reference
			existingRefs = append(existingRefs, *result.existingRef)
			slog.Info("skipped existing image", "url", result.existingRef.ImageURL, "id", result.existingRef.ImageID)
		} else {
			// New image, add to processed list
			processedImages = append(processedImages, result.img)
		}
		if result.warning != "" {
			failedAnalysis++
		}
	}

	// Add summary warning if some images failed
	if failedAnalysis > 0 {
		warnings = append(warnings, fmt.Sprintf("AI analysis failed for %d/%d images", failedAnalysis, len(images)))
	}

	// Add info about deduplicated images
	if len(existingRefs) > 0 {
		slog.Info("found existing images", "count", len(existingRefs))
	}

	return processedImages, existingRefs, warnings
}

// processSingleImage processes a single image (download and analyze)
// Returns the processed image, existing image reference (if found), and a warning string (empty if no issues)
// If existingRef is non-nil, the image already exists and img should be ignored
func (s *Scraper) processSingleImage(ctx context.Context, img models.ImageInfo) (models.ImageInfo, *models.ExistingImageRef, string) {

	// Check if image already exists in database (if DB is available)
	if s.db != nil {
		existingImage, err := s.db.GetImageByURL(img.URL)
		if err != nil {
			slog.Error("failed to check for existing image", "url", img.URL, "error", err)
			// Continue with download despite error
		} else if existingImage != nil {
			// Image already exists, return reference
			ref := &models.ExistingImageRef{
				ImageID:  existingImage.ID,
				ImageURL: existingImage.URL,
			}
			return models.ImageInfo{}, ref, ""
		}
	}

	// Generate UUID for the new image
	img.ID = uuid.New().String()

	// Download the image
	imageData, contentType, err := s.downloadImage(ctx, img.URL)
	if err != nil {
		slog.Error("failed to download image", "url", img.URL, "error", err)
		return img, nil, "download_failed"
	}

	slog.Info("downloaded image", "url", img.URL, "size_bytes", len(imageData))

	// Generate slug from image info
	img.Slug = slug.FromImageInfo(img.AltText, img.URL)
	if img.Slug == "" {
		img.Slug = img.ID // Fallback to UUID if slug generation fails
	}

	// Save to filesystem if storage is available
	if s.storage != nil {
		filePath, err := s.storage.SaveImage(imageData, img.Slug, contentType)
		if err != nil {
			slog.Error("failed to save image to filesystem", "url", img.URL, "error", err)
			// Fall back to base64 if filesystem storage fails
			img.Base64Data = base64.StdEncoding.EncodeToString(imageData)
		} else {
			img.FilePath = filePath
			slog.Info("saved image to filesystem", "url", img.URL, "path", filePath)
		}
	} else {
		// No storage configured, use base64 for backward compatibility
		img.Base64Data = base64.StdEncoding.EncodeToString(imageData)
	}

	// Populate file metadata
	img.FileSizeBytes = int64(len(imageData))
	img.ContentType = contentType

	// Extract image dimensions
	width, height, err := getImageDimensions(imageData)
	if err != nil {
		slog.Warn("failed to get image dimensions", "url", img.URL, "error", err)
		// Don't fail the entire operation, just leave dimensions empty
	} else {
		img.Width = width
		img.Height = height
		slog.Info("extracted image dimensions", "url", img.URL, "width", width, "height", height)
	}

	// Extract EXIF metadata
	if exifData := extractEXIF(imageData); exifData != nil {
		img.EXIF = exifData
		slog.Info("extracted EXIF data",
			"url", img.URL,
			"make", exifData.Make,
			"model", exifData.Model,
			"has_gps", exifData.GPS != nil)
	}

	// Analyze the image with Ollama (with semaphore protection)
	if err := s.acquireOllamaSlot(ctx); err == nil {
		summary, tags, err := s.ollamaClient.AnalyzeImage(ctx, imageData, img.AltText)
		s.releaseOllamaSlot()
		if err != nil {
			slog.Error("failed to analyze image", "url", img.URL, "error", err)
			return img, nil, "analysis_failed"
		}

		// Update image info with analysis results
		img.Summary = summary
		img.Tags = tags

		// Extract text from image using OCR (with semaphore protection)
		if err := s.acquireOllamaSlot(ctx); err == nil {
			extractedText, err := s.ollamaClient.ExtractTextFromImage(ctx, imageData)
			s.releaseOllamaSlot()
			if err != nil {
				slog.Warn("failed to extract text from image", "url", img.URL, "error", err)
				// Don't return error, OCR failure is not critical
			} else if extractedText != "" {
				img.ExtractedText = extractedText
				slog.Info("extracted text from image", "url", img.URL, "text_length", len(extractedText))
			}
		}

		// Check if image contains an infographic and add "banner" tag
		if isInfographic(summary, tags) {
			// Check if "banner" tag doesn't already exist
			hasBanner := false
			for _, tag := range img.Tags {
				if strings.EqualFold(tag, "banner") {
					hasBanner = true
					break
				}
			}
			if !hasBanner {
				img.Tags = append(img.Tags, "banner")
				slog.Info("detected infographic, added banner tag", "url", img.URL)
			}
		}

		slog.Info("successfully analyzed image",
			"url", img.URL,
			"summary_length", len(summary),
			"tag_count", len(tags),
			"extracted_text_length", len(img.ExtractedText))
		return img, nil, ""
	}

	slog.Warn("context cancelled while waiting for ollama slot", "operation", "image_analysis", "url", img.URL, "error", err)
	return img, nil, "analysis_timeout"
}

// isInfographic checks if an image contains an infographic based on summary and tags
func isInfographic(summary string, tags []string) bool {
	// Keywords that indicate an infographic
	infographicKeywords := []string{
		"infographic",
		"info graphic",
		"diagram",
		"chart",
		"graph",
		"visualization",
		"data visualization",
		"flowchart",
		"timeline",
		"statistics",
		"data presentation",
	}

	// Check summary (case-insensitive)
	summaryLower := strings.ToLower(summary)
	for _, keyword := range infographicKeywords {
		if strings.Contains(summaryLower, keyword) {
			return true
		}
	}

	// Check tags (case-insensitive)
	for _, tag := range tags {
		tagLower := strings.ToLower(tag)
		for _, keyword := range infographicKeywords {
			if strings.Contains(tagLower, keyword) {
				return true
			}
		}
	}

	return false
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// resolveURL resolves a potentially relative URL against a base URL
func resolveURL(base *url.URL, href string) (string, error) {
	// Parse the href
	parsed, err := url.Parse(href)
	if err != nil {
		return "", err
	}

	// Resolve against base
	resolved := base.ResolveReference(parsed)
	return resolved.String(), nil
}

// ScoreLinkContent fetches and scores a URL to determine if it should be ingested
func (s *Scraper) ScoreLinkContent(ctx context.Context, targetURL string) (*models.LinkScore, error) {
	// Validate URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("URL must be http or https")
	}

	// Skip scoring for direct image URLs
	if isImageURL(targetURL) {
		return &models.LinkScore{
			URL:                 targetURL,
			Score:               0.0,
			Reason:              "Image file detected - skipping content scoring",
			Categories:          []string{"image", "media"},
			IsRecommended:       false,
			MaliciousIndicators: []string{},
			AIUsed:              false,
		}, nil
	}

	// Fetch the page
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Parse HTML
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Extract title
	title := extractTitle(doc)
	if title == "" {
		title = targetURL
	}

	// Extract text content
	textContent := extractText(doc)

	// Use Ollama to score the content (with fallback to rule-based scoring)
	var score float64
	var reason string
	var categories []string
	var maliciousIndicators []string
	var aiUsed bool

	// Check for low-quality patterns first (before Ollama) to avoid unnecessary AI calls
	shouldSkipAI, earlyScore, earlyReason, earlyCategories, earlyIndicators := checkForLowQualityPatterns(targetURL, title)
	if shouldSkipAI {
		score = earlyScore
		reason = earlyReason
		categories = earlyCategories
		maliciousIndicators = earlyIndicators
		aiUsed = false
	} else if err := s.acquireOllamaSlot(ctx); err == nil {
		var err error
		score, reason, categories, maliciousIndicators, err = s.ollamaClient.ScoreContent(ctx, targetURL, title, textContent)
		s.releaseOllamaSlot()
		if err != nil {
			// Fallback to rule-based scoring when Ollama fails
			slog.Warn("ollama scoring failed, using rule-based fallback", "url", targetURL, "error", err)
			score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, textContent)
			aiUsed = false
		} else {
			aiUsed = true
		}
	} else {
		// Context cancelled, use rule-based fallback
		slog.Warn("context cancelled while waiting for ollama slot", "operation", "scoring", "error", err)
		score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, textContent)
		aiUsed = false
	}

	// Determine if the link is recommended based on configurable threshold
	isRecommended := score >= s.config.LinkScoreThreshold

	linkScore := &models.LinkScore{
		URL:                 targetURL,
		Score:               score,
		Reason:              reason,
		Categories:          categories,
		IsRecommended:       isRecommended,
		MaliciousIndicators: maliciousIndicators,
		AIUsed:              aiUsed,
	}

	return linkScore, nil
}

// checkForLowQualityPatterns performs early detection of low-quality URLs that don't require AI analysis
// Returns (shouldSkipAI, score, reason, categories, maliciousIndicators)
func checkForLowQualityPatterns(targetURL, title string) (bool, float64, string, []string, []string) {
	urlLower := strings.ToLower(targetURL)
	titleLower := strings.ToLower(title)

	// Check for image content
	if isImageURL(targetURL) {
		categories := []string{"image", "media"}
		maliciousIndicators := []string{}
		return true, 0.0, "Image file detected - skipping content scoring", categories, maliciousIndicators
	}

	// Check for audio/video content
	if isAudioVideoURL(targetURL) {
		categories := []string{"media", "low-quality"}
		maliciousIndicators := []string{}

		if strings.Contains(urlLower, ".mp3") || strings.Contains(urlLower, ".wav") ||
			strings.Contains(urlLower, ".ogg") || strings.Contains(urlLower, ".flac") ||
			strings.Contains(urlLower, ".aac") || strings.Contains(urlLower, ".m4a") ||
			strings.Contains(urlLower, ".mp4") || strings.Contains(urlLower, ".avi") ||
			strings.Contains(urlLower, ".mkv") || strings.Contains(urlLower, ".mov") ||
			strings.Contains(urlLower, ".webm") || strings.Contains(urlLower, ".wmv") ||
			strings.Contains(urlLower, ".flv") || strings.Contains(urlLower, ".m4v") ||
			strings.Contains(urlLower, ".mpeg") || strings.Contains(urlLower, ".mpg") ||
			strings.Contains(urlLower, ".wma") || strings.Contains(urlLower, ".opus") ||
			strings.Contains(urlLower, ".aiff") {
			categories = append(categories, "audio-video")
			maliciousIndicators = append(maliciousIndicators, "media-file")
			return true, 0.15, "Audio/video file detected", categories, maliciousIndicators
		}
		categories = append(categories, "streaming")
		maliciousIndicators = append(maliciousIndicators, "streaming-platform")
		return true, 0.15, "Streaming platform detected", categories, maliciousIndicators
	}

	// Check for subscription/settings/preferences/account pages
	undesirablePatterns := []struct {
		patterns   []string
		category   string
		reason     string
		indicator  string
	}{
		{
			patterns:  []string{"/subscribe", "/subscription", "/subscriptions", "/pricing", "/plan", "/plans", "/premium", "/upgrade"},
			category:  "subscription",
			reason:    "Subscription/pricing page detected",
			indicator: "subscription-page",
		},
		{
			patterns:  []string{"/setting", "/settings", "/preference", "/preferences", "/config", "/configuration", "/configurations"},
			category:  "settings",
			reason:    "Settings/preferences page detected",
			indicator: "settings-page",
		},
		{
			patterns:  []string{"/account", "/accounts", "/profile", "/profiles", "/login", "/signin", "/signup", "/sign-up", "/register", "/auth"},
			category:  "account",
			reason:    "Account/login page detected",
			indicator: "account-page",
		},
		{
			patterns:  []string{"/checkout", "/cart", "/carts", "/basket", "/baskets", "/payment", "/payments"},
			category:  "commerce",
			reason:    "Shopping/payment page detected",
			indicator: "commerce-page",
		},
		{
			patterns:  []string{"/unsubscribe", "/opt-out", "/optout", "/opt_out"},
			category:  "unsubscribe",
			reason:    "Unsubscribe page detected",
			indicator: "unsubscribe-page",
		},
		{
			patterns:  []string{"/about", "/about-us", "/aboutus", "/who-we-are", "/our-story", "/our-team", "/team"},
			category:  "about",
			reason:    "About/team page detected",
			indicator: "about-page",
		},
		{
			patterns:  []string{"/contact", "/contact-us", "/contactus", "/get-in-touch", "/reach-us", "/support"},
			category:  "contact",
			reason:    "Contact page detected",
			indicator: "contact-page",
		},
	}

	for _, pattern := range undesirablePatterns {
		for _, p := range pattern.patterns {
			if strings.Contains(urlLower, p) || strings.Contains(titleLower, p) {
				categories := []string{pattern.category, "low-quality", "utility-page"}
				maliciousIndicators := []string{pattern.indicator}
				return true, 0.1, pattern.reason, categories, maliciousIndicators
			}
		}
	}

	// No early pattern detected
	return false, 0.0, "", nil, nil
}

// filterLowQualityLinks filters out low-quality URLs from a list using pattern matching
// Returns the filtered list of URLs and the count of filtered links
func filterLowQualityLinks(urls []string) ([]string, int) {
	if len(urls) == 0 {
		return urls, 0
	}

	filtered := make([]string, 0, len(urls))
	filteredCount := 0

	// Define patterns to filter out
	lowQualityPatterns := []string{
		// Subscription and commerce
		"/subscribe", "/subscription", "/subscriptions", "/pricing", "/plan", "/plans", "/premium", "/upgrade",
		"/checkout", "/cart", "/carts", "/basket", "/baskets", "/payment", "/payments",

		// Account and authentication
		"/account", "/accounts", "/profile", "/profiles", "/login", "/signin", "/signup", "/sign-up", "/register", "/auth",
		"/setting", "/settings", "/preference", "/preferences", "/config", "/configuration", "/configurations",

		// Unsubscribe and opt-out
		"/unsubscribe", "/opt-out", "/optout", "/opt_out",

		// About and contact pages
		"/about", "/about-us", "/aboutus", "/who-we-are", "/our-story", "/our-team", "/team",
		"/contact", "/contact-us", "/contactus", "/get-in-touch", "/reach-us", "/support",

		// Navigation and utility
		"/privacy", "/terms", "/cookie", "/legal", "/disclaimer", "/faq", "/help",
		"/sitemap", "/search", "/rss", "/feed", "/newsletter", "/jobs", "/careers",
		"/press", "/media-kit", "/advertise", "/advertising",

		// Social media and sharing
		"/share", "/tweet", "/facebook", "/twitter", "/linkedin", "/pinterest",

		// Fragments and anchors (often just navigation)
		"#comments", "#respond", "#reply", "#share", "#footer", "#header", "#nav",
	}

	// Media file extensions
	mediaExtensions := []string{
		".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma", ".opus", ".aiff",
		".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpeg", ".mpg",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".rar", ".tar", ".gz",
	}

	// Streaming platforms to filter out
	streamingPlatforms := []string{
		"youtube.com", "youtu.be", "vimeo.com", "dailymotion.com",
		"twitch.tv", "soundcloud.com", "spotify.com", "music.apple.com",
		"tidal.com", "deezer.com", "pandora.com", "music.youtube.com",
	}

	for _, url := range urls {
		urlLower := strings.ToLower(url)
		shouldFilter := false

		// Check for media files
		for _, ext := range mediaExtensions {
			if strings.HasSuffix(urlLower, ext) || strings.Contains(urlLower, ext+"?") {
				shouldFilter = true
				break
			}
		}

		if !shouldFilter {
			// Check for streaming platforms
			for _, platform := range streamingPlatforms {
				if strings.Contains(urlLower, platform) {
					shouldFilter = true
					break
				}
			}
		}

		if !shouldFilter {
			// Check for low-quality URL patterns
			for _, pattern := range lowQualityPatterns {
				if strings.Contains(urlLower, pattern) {
					shouldFilter = true
					break
				}
			}
		}

		if !shouldFilter {
			// Check if URL is a category/section page
			if isCategoryPage(url) {
				shouldFilter = true
			}
		}

		if !shouldFilter {
			filtered = append(filtered, url)
		} else {
			filteredCount++
		}
	}

	return filtered, filteredCount
}

// deduplicateLinks removes duplicate URLs from a list while preserving order
func deduplicateLinks(urls []string) []string {
	if len(urls) == 0 {
		return urls
	}

	seen := make(map[string]bool, len(urls))
	deduplicated := make([]string, 0, len(urls))

	for _, url := range urls {
		if !seen[url] {
			seen[url] = true
			deduplicated = append(deduplicated, url)
		}
	}

	return deduplicated
}

// isCategoryPage detects if a URL is likely a category/section landing page
// These pages are useful for link extraction but shouldn't be in the final results
func isCategoryPage(targetURL string) bool {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return false
	}

	path := strings.Trim(parsedURL.Path, "/")

	// Empty path or just the domain (homepage) - not a category
	if path == "" {
		return false
	}

	// Split path into segments
	segments := strings.Split(path, "/")

	// Common category/section indicators in the path
	categoryIndicators := []string{
		"section", "sections", "category", "categories", "topic", "topics",
		"tag", "tags", "archive", "archives", "index",
	}

	// Check if any segment contains category indicators (strip file extensions first)
	for _, segment := range segments {
		segmentLower := strings.ToLower(segment)

		// Strip common file extensions
		segmentLower = strings.TrimSuffix(segmentLower, ".html")
		segmentLower = strings.TrimSuffix(segmentLower, ".htm")
		segmentLower = strings.TrimSuffix(segmentLower, ".php")
		segmentLower = strings.TrimSuffix(segmentLower, ".asp")
		segmentLower = strings.TrimSuffix(segmentLower, ".aspx")

		for _, indicator := range categoryIndicators {
			if segmentLower == indicator || strings.HasPrefix(segmentLower, indicator+"-") || strings.HasPrefix(segmentLower, indicator+"_") {
				return true
			}
		}
	}

	// Common news section/category names
	newsSections := []string{
		// General news sections
		"news", "world", "national", "local", "us", "uk", "international", "global",
		"politics", "political", "government", "policy",
		"business", "finance", "economy", "markets", "money",
		"technology", "tech", "science", "innovation",
		"technology", "tech", "science", "innovation",
		"health", "medical", "wellness", "healthcare",
		"sports", "sport", "football", "basketball", "baseball", "soccer",
		"entertainment", "culture", "arts", "music", "movies", "film", "tv", "television",
		"lifestyle", "life", "living", "fashion", "food", "travel", "style",
		"opinion", "opinions", "editorial", "editorials", "commentary", "columnists",
		"investigations", "analysis", "features", "special-reports",
		"environment", "climate", "weather", "energy",
		"education", "schools", "university", "college",
		"crime", "law", "justice", "courts",
		"religion", "faith", "beliefs",
		"obituaries", "obits", "deaths",

		// Regional/location-based sections
		"asia", "europe", "africa", "americas", "middle-east", "middleeast",
		"asia-pacific", "latin-america", "north-america", "south-america",
		"england", "scotland", "wales", "northern-ireland",
		"us-canada", "latin-america", "middle-east-asia",

		// Media-specific sections
		"video", "videos", "podcasts", "audio", "multimedia", "gallery", "galleries",
		"photos", "pictures", "images",

		// Time-based sections
		"today", "latest", "breaking", "live", "now", "updates",
	}

	// Check if URL looks like an article (has numeric IDs or article patterns)
	hasArticlePattern := false
	for _, segment := range segments {
		segmentLower := strings.ToLower(segment)

		// Check for "articles" segment (common in modern news sites)
		if segmentLower == "articles" || segmentLower == "article" || segmentLower == "story" {
			hasArticlePattern = true
			break
		}

		// Check for segments with 8+ digit numbers (common article IDs)
		// e.g., "world-middle-east-12345678"
		if len(segmentLower) >= 8 {
			digitCount := 0
			for _, char := range segmentLower {
				if char >= '0' && char <= '9' {
					digitCount++
				}
			}
			if digitCount >= 8 {
				hasArticlePattern = true
				break
			}
		}
	}

	// If it has article patterns, it's not a category page
	if hasArticlePattern {
		return false
	}

	// Check all segments against known section names
	// If URL has 1-4 path segments and consists primarily of section names, it's likely a category page
	if len(segments) <= 4 {
		sectionMatchCount := 0
		for _, segment := range segments {
			segmentLower := strings.ToLower(segment)

			// Strip file extensions before checking
			segmentLower = strings.TrimSuffix(segmentLower, ".html")
			segmentLower = strings.TrimSuffix(segmentLower, ".htm")
			segmentLower = strings.TrimSuffix(segmentLower, ".php")
			segmentLower = strings.TrimSuffix(segmentLower, ".asp")
			segmentLower = strings.TrimSuffix(segmentLower, ".aspx")

			matched := false
			for _, section := range newsSections {
				// Exact match or starts with section name
				if segmentLower == section || strings.HasPrefix(segmentLower, section+"-") || strings.HasPrefix(segmentLower, section+"_") {
					matched = true
					break
				}
				// Also check if segment starts with the section name (for compound names like "sciencetech")
				if len(section) >= 4 && strings.HasPrefix(segmentLower, section) {
					matched = true
					break
				}
			}
			if matched {
				sectionMatchCount++
			}
		}

		// If most segments are section names, it's a category page
		// For 1-2 segments: all must match
		// For 3 segments: at least 2 must match
		// For 4 segments: at least 3 must match (to avoid false positives with article titles)
		if len(segments) <= 2 && sectionMatchCount == len(segments) {
			return true
		}
		if len(segments) == 3 && sectionMatchCount >= 2 {
			return true
		}
		if len(segments) == 4 && sectionMatchCount >= 3 {
			return true
		}
	}

	// Check for patterns like "/section/name" or "/category/name" with no further depth
	if len(segments) == 2 {
		firstSegment := strings.ToLower(segments[0])
		for _, indicator := range categoryIndicators {
			if firstSegment == indicator {
				return true
			}
		}
	}

	// Check for year-based archive pages (e.g., /2024/, /2024/01/)
	if len(segments) >= 1 && len(segments) <= 3 {
		// Check if first segment is a 4-digit year
		if len(segments[0]) == 4 {
			_, err := strconv.Atoi(segments[0])
			if err == nil {
				// If it's just /YYYY/ or /YYYY/MM/ or /YYYY/MM/DD/, likely an archive
				if len(segments) <= 2 {
					return true
				}
				// /YYYY/MM/DD/ is still a category if all are numbers
				if len(segments) == 3 {
					allNumbers := true
					for _, seg := range segments {
						if _, err := strconv.Atoi(seg); err != nil {
							allNumbers = false
							break
						}
					}
					if allNumbers {
						return true
					}
				}
			}
		}
	}

	return false
}

// isAudioVideoURL checks if a URL points to audio/video content or streaming platforms
func isAudioVideoURL(targetURL string) bool {
	urlLower := strings.ToLower(targetURL)

	// Check for audio/video file extensions
	audioVideoExtensions := []string{
		// Audio
		".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma", ".opus", ".aiff",
		// Video
		".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpeg", ".mpg",
	}

	for _, ext := range audioVideoExtensions {
		if strings.HasSuffix(urlLower, ext) || strings.Contains(urlLower, ext+"?") {
			return true
		}
	}

	// Check for streaming platforms
	streamingPlatforms := []string{
		"youtube.com", "youtu.be", "vimeo.com", "dailymotion.com",
		"twitch.tv", "soundcloud.com", "spotify.com", "music.apple.com",
		"tidal.com", "deezer.com", "pandora.com", "music.youtube.com",
	}

	for _, platform := range streamingPlatforms {
		if strings.Contains(urlLower, platform) {
			return true
		}
	}

	return false
}

// isImageURL checks if a URL points directly to an image file
func isImageURL(targetURL string) bool {
	// Parse URL to extract components
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return false
	}

	// Check for image file extensions in path
	imageExtensions := []string{
		".jpg", ".jpeg", ".png", ".gif", ".bmp",
		".webp", ".svg", ".ico", ".tiff", ".tif",
	}

	// Check path before query parameters
	pathLower := strings.ToLower(parsedURL.Path)
	for _, ext := range imageExtensions {
		if strings.HasSuffix(pathLower, ext) {
			return true
		}
	}

	// Check for image format in query parameters (e.g., fm=jpg, format=png, ext=jpg)
	queryParams := parsedURL.Query()
	formatParams := []string{"fm", "format", "ext", "f"}
	for _, param := range formatParams {
		if value := queryParams.Get(param); value != "" {
			valueLower := strings.ToLower(value)
			for _, ext := range imageExtensions {
				// Remove leading dot for comparison with query param values
				extWithoutDot := strings.TrimPrefix(ext, ".")
				if valueLower == extWithoutDot || strings.HasPrefix(valueLower, extWithoutDot) {
					return true
				}
			}
		}
	}

	// Check for known image hosting domains
	imageHosts := []string{
		"images.unsplash.com",
		"i.imgur.com",
		"cdn.pixabay.com",
		"images.pexels.com",
		"source.unsplash.com",
	}

	hostLower := strings.ToLower(parsedURL.Host)
	for _, imageHost := range imageHosts {
		if hostLower == imageHost || strings.HasSuffix(hostLower, "."+imageHost) {
			return true
		}
	}

	return false
}

// scoreContentFallback provides rule-based content scoring when Ollama is unavailable
// This function is only called after checkForLowQualityPatterns() has already passed,
// so we don't need to re-check for audio/video, subscription pages, etc.
func scoreContentFallback(targetURL, title, content string) (score float64, reason string, categories []string, maliciousIndicators []string) {
	score = 0.5 // Start with neutral score
	categories = []string{}
	maliciousIndicators = []string{}
	reasons := []string{}

	urlLower := strings.ToLower(targetURL)
	titleLower := strings.ToLower(title)
	contentLower := strings.ToLower(content)

	// Check for blocked content types (social media, gambling, adult, drugs, etc.)
	blockedDomains := map[string]string{
		"facebook.com":    "social-media",
		"twitter.com":     "social-media",
		"x.com":           "social-media",
		"instagram.com":   "social-media",
		"tiktok.com":      "social-media",
		"reddit.com":      "forum",
		"linkedin.com":    "social-media",
		"pinterest.com":   "social-media",
		"snapchat.com":    "social-media",
		"bet":             "gambling",
		"casino":          "gambling",
		"poker":           "gambling",
		"betting":         "gambling",
		"xxx":             "adult-content",
		"porn":            "adult-content",
		"adult":           "adult-content",
		"cannabis":        "drugs",
		"weed":            "drugs",
		"ebay.com":        "marketplace",
		"amazon.com":      "marketplace",
		"craigslist.org":  "marketplace",
	}

	for domain, category := range blockedDomains {
		if strings.Contains(urlLower, domain) {
			score = 0.1
			categories = append(categories, category, "low-quality")
			reasons = append(reasons, "Blocked content type detected: "+category)
			maliciousIndicators = append(maliciousIndicators, category)
			reason = strings.Join(reasons, "; ")
			return
		}
	}

	// Content length checks
	contentLength := len(content)
	wordCount := len(strings.Fields(content))

	if contentLength < 100 {
		score -= 0.3
		reasons = append(reasons, "Very short content")
		categories = append(categories, "low-quality")
	} else if contentLength < 500 {
		score -= 0.1
		reasons = append(reasons, "Short content")
	} else if contentLength > 1000 {
		score += 0.2
		reasons = append(reasons, "Substantial content")
		categories = append(categories, "informational")
	}

	if wordCount < 20 {
		score -= 0.2
		categories = append(categories, "minimal-content")
	}

	// Check for bullet point lists and sparse content
	lines := strings.Split(content, "\n")
	nonEmptyLines := 0
	bulletLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			nonEmptyLines++
			// Check if line starts with bullet point markers
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "•") ||
				strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "◦") ||
				(len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && (trimmed[1] == '.' || trimmed[1] == ')')) {
				bulletLines++
			}
		}
	}

	// If more than 60% of non-empty lines are bullet points, it's likely a sparse list
	if nonEmptyLines > 5 && bulletLines > 0 {
		bulletRatio := float64(bulletLines) / float64(nonEmptyLines)
		if bulletRatio > 0.6 {
			score -= 0.3
			reasons = append(reasons, "Content is primarily bullet points/list items")
			categories = append(categories, "sparse-content", "low-quality")
		} else if bulletRatio > 0.4 {
			score -= 0.15
			reasons = append(reasons, "Content contains many bullet points")
		}
	}

	// Check for excessive whitespace/newlines (sparse content)
	// If average line length is very short, content is likely sparse
	if nonEmptyLines > 10 {
		avgLineLength := float64(contentLength) / float64(nonEmptyLines)
		if avgLineLength < 30 {
			score -= 0.2
			reasons = append(reasons, "Content has very short lines (sparse layout)")
			categories = append(categories, "sparse-content")
		}
	}

	// Check for spam indicators
	if strings.Count(contentLower, "click here") > 2 ||
		strings.Count(contentLower, "buy now") > 2 ||
		strings.Count(contentLower, "limited offer") > 1 {
		score -= 0.3
		reasons = append(reasons, "Spam indicators detected")
		categories = append(categories, "spam")
		maliciousIndicators = append(maliciousIndicators, "spam_keywords")
	}

	// Check for excessive punctuation (!!!, ???, etc.)
	exclamationCount := strings.Count(content, "!")
	if exclamationCount > wordCount/10 && exclamationCount > 5 {
		score -= 0.2
		reasons = append(reasons, "Excessive punctuation")
	}

	// Check for quality indicators in URL
	qualityDomains := []string{".edu", ".gov", ".org", "wikipedia", "arxiv", "github", "stackoverflow"}
	for _, domain := range qualityDomains {
		if strings.Contains(urlLower, domain) {
			score += 0.3
			reasons = append(reasons, "Quality domain detected")
			categories = append(categories, "reference", "trusted_source")
			break
		}
	}

	// Check for technical/educational content indicators
	technicalKeywords := []string{"documentation", "tutorial", "guide", "research", "study", "analysis", "technical"}
	for _, keyword := range technicalKeywords {
		if strings.Contains(titleLower, keyword) || strings.Contains(contentLower, keyword) {
			score += 0.1
			categories = append(categories, "technical", "educational")
			break
		}
	}

	// Ensure score is within bounds
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	// Build reason string
	if len(reasons) == 0 {
		reason = "Rule-based assessment (Ollama unavailable)"
	} else {
		reason = "Rule-based: " + strings.Join(reasons, "; ")
	}

	// Ensure categories is not nil
	if len(categories) == 0 {
		if score >= 0.6 {
			categories = []string{"informational"}
		} else {
			categories = []string{"general"}
		}
	}

	// Ensure maliciousIndicators is not nil
	if maliciousIndicators == nil {
		maliciousIndicators = []string{}
	}

	// Normalize all categories
	for i, category := range categories {
		categories[i] = normalizeTag(category)
	}

	// Normalize all malicious indicators
	for i, indicator := range maliciousIndicators {
		maliciousIndicators[i] = normalizeTag(indicator)
	}

	return score, reason, categories, maliciousIndicators
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
