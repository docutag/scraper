package scraper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zombar/scraper/models"
	"github.com/zombar/scraper/ollama"
	"golang.org/x/net/html"
)

// Config contains scraper configuration
type Config struct {
	HTTPTimeout         time.Duration
	OllamaBaseURL       string
	OllamaModel         string
	EnableImageAnalysis bool          // Enable AI-powered image analysis
	MaxImageSizeBytes   int64         // Maximum image size to download (bytes)
	ImageTimeout        time.Duration // Timeout for downloading individual images
	LinkScoreThreshold  float64       // Minimum score for link to be recommended (0.0-1.0)
}

// DefaultConfig returns default scraper configuration
func DefaultConfig() Config {
	return Config{
		HTTPTimeout:         30 * time.Second,
		OllamaBaseURL:       ollama.DefaultBaseURL,
		OllamaModel:         ollama.DefaultModel,
		EnableImageAnalysis: true,              // Enable image analysis by default
		MaxImageSizeBytes:   10 * 1024 * 1024,  // 10MB max image size
		ImageTimeout:        15 * time.Second,  // 15s timeout per image
		LinkScoreThreshold:  0.5,               // Default threshold for link scoring
	}
}

// DB interface defines the database operations needed by the scraper
type DB interface {
	GetImageByURL(url string) (*models.ImageInfo, error)
}

// Scraper handles web scraping operations
type Scraper struct {
	config         Config
	httpClient     *http.Client
	ollamaClient   *ollama.Client
	ollamaSemaphore chan struct{} // Semaphore to limit concurrent Ollama requests
	db             DB            // Database for checking existing images
}

// New creates a new Scraper instance
// db parameter can be nil if image deduplication is not needed
func New(config Config, db DB) *Scraper {
	// Limit concurrent Ollama requests to 3 to prevent overload during batch operations
	maxConcurrentOllamaRequests := 3

	return &Scraper{
		config: config,
		httpClient: &http.Client{
			Timeout: config.HTTPTimeout,
		},
		ollamaClient:    ollama.NewClient(config.OllamaBaseURL, config.OllamaModel),
		ollamaSemaphore: make(chan struct{}, maxConcurrentOllamaRequests),
		db:              db,
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

	// Fetch the page
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Scraper/1.0)")

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
			log.Printf("Ollama content extraction failed for %s, using raw text: %v", targetURL, err)
			warnings = append(warnings, "AI content extraction unavailable, using raw text")
		} else {
			content = extractedContent
		}
	} else {
		log.Printf("Context cancelled while waiting for Ollama slot for content extraction: %v", err)
		warnings = append(warnings, "Content extraction timed out, using raw text")
	}

	// Extract images
	images := extractImages(doc, parsedURL)

	// Process images (download and analyze if enabled)
	images, existingImageRefs, imageWarnings := s.processImages(ctx, images)
	warnings = append(warnings, imageWarnings...)

	// Extract links with Ollama sanitization
	links := s.extractLinksWithOllama(ctx, doc, parsedURL, title, content)

	// Extract metadata
	metadata := extractMetadata(doc)

	// Add existing image references to metadata
	if len(existingImageRefs) > 0 {
		metadata.ExistingImageRefs = existingImageRefs
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
			log.Printf("Ollama scoring failed for %s, using rule-based fallback: %v", targetURL, err)
			score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, content)
			aiUsed = false
			warnings = append(warnings, "AI scoring unavailable, using rule-based scoring")
		} else {
			aiUsed = true
		}
	} else {
		// Context cancelled, use rule-based fallback
		log.Printf("Context cancelled while waiting for Ollama slot for scoring: %v", err)
		score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, content)
		aiUsed = false
		warnings = append(warnings, "Scoring timed out, using rule-based scoring")
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

	// Create scraped data
	data := &models.ScrapedData{
		ID:             uuid.New().String(),
		URL:            targetURL,
		Title:          title,
		Content:        content,
		Images:         images,
		Links:          links,
		FetchedAt:      time.Now(),
		CreatedAt:      time.Now(),
		ProcessingTime: time.Since(start).Seconds(),
		Cached:         false,
		Metadata:       metadata,
		Score:          linkScore,
		Warnings:       warnings,
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
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Scraper/1.0)")

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
			log.Printf("Ollama content extraction failed for %s, using raw text: %v", targetURL, err)
		} else {
			content = extractedContent
		}
	} else {
		log.Printf("Context cancelled while waiting for Ollama slot for content extraction: %v", err)
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
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				buf.WriteString(text)
				buf.WriteString(" ")
			}
		}
		// Skip script and style tags
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.TrimSpace(buf.String())
}

// extractImages extracts image information from the HTML
func extractImages(n *html.Node, baseURL *url.URL) []models.ImageInfo {
	var images []models.ImageInfo
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			var src, alt string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "src":
					src = attr.Val
				case "alt":
					alt = attr.Val
				}
			}
			if src != "" {
				// Resolve relative URLs
				if imgURL, err := resolveURL(baseURL, src); err == nil {
					images = append(images, models.ImageInfo{
						URL:     imgURL,
						AltText: alt,
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
	log.Printf("Link extraction: found %d links, filtered out %d low-quality links, %d remaining",
		len(allLinks), filteredCount, len(filteredLinks))

	// If we have no links remaining, return early
	if len(filteredLinks) == 0 {
		return filteredLinks
	}

	// Skip AI only if:
	// 1. We have very few links (≤3) after filtering, AND
	// 2. We filtered out at least 50% of the original links (meaning our patterns worked well)
	// This ensures we still use AI when needed for intelligent filtering
	if len(filteredLinks) <= 3 && filteredCount > 0 && float64(filteredCount)/float64(len(allLinks)) >= 0.5 {
		log.Printf("Skipping AI for link extraction: %d clean links after filtering %d/%d links",
			len(filteredLinks), filteredCount, len(allLinks))
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
			log.Printf("Ollama link sanitization failed, returning filtered links: %v", err)
		} else {
			// Parse JSON response
			var parsedLinks []string
			if err := json.Unmarshal([]byte(response), &parsedLinks); err != nil {
				// If parsing fails, fall back to returning filtered links
				log.Printf("Failed to parse Ollama link response, returning filtered links: %v", err)
			} else if parsedLinks != nil {
				sanitizedLinks = parsedLinks
				log.Printf("AI refined links from %d to %d", len(filteredLinks), len(sanitizedLinks))
			}
		}
	} else {
		log.Printf("Context cancelled while waiting for Ollama slot for link sanitization: %v", err)
	}

	// Ensure we never return nil
	if sanitizedLinks == nil {
		sanitizedLinks = []string{}
	}

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
func (s *Scraper) downloadImage(ctx context.Context, imageURL string) ([]byte, error) {
	// Create request with timeout context
	ctx, cancel := context.WithTimeout(ctx, s.config.ImageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Scraper/1.0)")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	// Check content length if available
	if resp.ContentLength > s.config.MaxImageSizeBytes {
		return nil, fmt.Errorf("image too large: %d bytes (max: %d)", resp.ContentLength, s.config.MaxImageSizeBytes)
	}

	// Read with size limit
	limitedReader := io.LimitReader(resp.Body, s.config.MaxImageSizeBytes+1)
	imageData, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	// Check if we exceeded the limit
	if int64(len(imageData)) > s.config.MaxImageSizeBytes {
		return nil, fmt.Errorf("image too large: exceeds %d bytes", s.config.MaxImageSizeBytes)
	}

	return imageData, nil
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

// processImages downloads and analyzes images if image analysis is enabled
// Uses parallel processing with worker pool for better performance
// Returns processed images, existing image references, and any warnings encountered
func (s *Scraper) processImages(ctx context.Context, images []models.ImageInfo) ([]models.ImageInfo, []models.ExistingImageRef, []string) {
	warnings := []string{}
	existingRefs := []models.ExistingImageRef{}

	if !s.config.EnableImageAnalysis {
		log.Printf("Image analysis disabled, returning %d images without analysis", len(images))
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
			log.Printf("Skipping junk image: %s", img.URL)
			skippedCount++
		} else {
			filteredImages = append(filteredImages, img)
		}
	}

	if skippedCount > 0 {
		log.Printf("Filtered out %d junk images (placeholder/temp/UI components)", skippedCount)
		warnings = append(warnings, fmt.Sprintf("Skipped %d placeholder/temp/UI component images", skippedCount))
	}

	// If all images were filtered out, return early
	if len(filteredImages) == 0 {
		log.Printf("All %d images were filtered out as junk", len(images))
		return []models.ImageInfo{}, existingRefs, warnings
	}

	// Use filtered images for processing
	images = filteredImages

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

	// Collect results in order
	processedImages := make([]models.ImageInfo, 0, len(images))
	failedAnalysis := 0
	for result := range results {
		if result.existingRef != nil {
			// Image already exists in database, add reference
			existingRefs = append(existingRefs, *result.existingRef)
			log.Printf("Skipped downloading existing image %s (ID: %s)", result.existingRef.ImageURL, result.existingRef.ImageID)
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
		log.Printf("Found %d existing images (not re-downloaded)", len(existingRefs))
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
			log.Printf("Error checking for existing image %s: %v", img.URL, err)
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
	imageData, err := s.downloadImage(ctx, img.URL)
	if err != nil {
		log.Printf("Failed to download image %s: %v", img.URL, err)
		return img, nil, "download_failed"
	}

	log.Printf("Downloaded image %s (%d bytes)", img.URL, len(imageData))

	// Store base64 encoded image data
	img.Base64Data = base64.StdEncoding.EncodeToString(imageData)

	// Analyze the image with Ollama (with semaphore protection)
	if err := s.acquireOllamaSlot(ctx); err == nil {
		summary, tags, err := s.ollamaClient.AnalyzeImage(ctx, imageData, img.AltText)
		s.releaseOllamaSlot()
		if err != nil {
			log.Printf("Failed to analyze image %s: %v", img.URL, err)
			return img, nil, "analysis_failed"
		}

		// Update image info with analysis results
		img.Summary = summary
		img.Tags = tags
		log.Printf("Successfully analyzed image %s (summary: %d chars, tags: %d)",
			img.URL, len(summary), len(tags))
		return img, nil, ""
	}

	log.Printf("Context cancelled while waiting for Ollama slot for image %s: %v", img.URL, err)
	return img, nil, "analysis_timeout"
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

	// Fetch the page
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Scraper/1.0)")

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
			log.Printf("Ollama scoring failed, using rule-based fallback: %v", err)
			score, reason, categories, maliciousIndicators = scoreContentFallback(targetURL, title, textContent)
			aiUsed = false
		} else {
			aiUsed = true
		}
	} else {
		// Context cancelled, use rule-based fallback
		log.Printf("Context cancelled while waiting for Ollama slot for scoring: %v", err)
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
			filtered = append(filtered, url)
		} else {
			filteredCount++
		}
	}

	return filtered, filteredCount
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
