package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zombar/scraper"
	"github.com/zombar/scraper/db"
	"github.com/zombar/scraper/models"
)

// Server represents the API server
type Server struct {
	db          *db.DB
	scraper     *scraper.Scraper
	addr        string
	server      *http.Server
	mux         *http.ServeMux
	corsEnabled bool
}

// Config contains server configuration
type Config struct {
	Addr          string
	DBConfig      db.Config
	ScraperConfig scraper.Config
	CORSEnabled   bool
}

// DefaultConfig returns default server configuration
func DefaultConfig() Config {
	return Config{
		Addr:          ":8080",
		DBConfig:      db.DefaultConfig(),
		ScraperConfig: scraper.DefaultConfig(),
		CORSEnabled:   true,
	}
}

// NewServer creates a new API server
func NewServer(config Config) (*Server, error) {
	// Initialize database
	database, err := db.New(config.DBConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Initialize scraper
	scraperInstance := scraper.New(config.ScraperConfig)

	s := &Server{
		db:          database,
		scraper:     scraperInstance,
		addr:        config.Addr,
		mux:         http.NewServeMux(),
		corsEnabled: config.CORSEnabled,
	}

	// Register routes
	s.registerRoutes()

	// Create HTTP server
	s.server = &http.Server{
		Addr:         config.Addr,
		Handler:      s.middleware(s.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 15 * time.Minute, // Allow time for long-running scrapes
		IdleTimeout:  120 * time.Second,
	}

	return s, nil
}

// registerRoutes sets up all API routes
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/api/scrape", s.handleScrape)
	s.mux.HandleFunc("/api/scrape/batch", s.handleBatchScrape)
	s.mux.HandleFunc("/api/extract-links", s.handleExtractLinks)
	s.mux.HandleFunc("/api/score", s.handleScore)
	s.mux.HandleFunc("/api/data/", s.handleData) // Handles /api/data/{id}
	s.mux.HandleFunc("/api/data", s.handleList)
	s.mux.HandleFunc("/api/images/search", s.handleImageSearch)
	s.mux.HandleFunc("/api/images/", s.handleImage) // Handles /api/images/{id}
}

// Start starts the API server
func (s *Server) Start() error {
	log.Printf("Starting API server on %s", s.addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down API server...")
	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}
	return s.db.Close()
}

// middleware applies common middleware to all routes
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		if s.corsEnabled {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		// Logging
		start := time.Now()
		log.Printf("%s %s", r.Method, r.URL.Path)

		next.ServeHTTP(w, r)

		log.Printf("%s %s - completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// handleHealth returns server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	count, err := s.db.Count()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get count")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status": "healthy",
		"count":  count,
		"time":   time.Now(),
	})
}

// ScrapeRequest represents a scrape request
type ScrapeRequest struct {
	URL   string `json:"url"`
	Force bool   `json:"force"` // Force re-scrape even if exists
}

// handleScrape handles single URL scraping
func (s *Server) handleScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.URL == "" {
		respondError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Check if URL already exists (unless force is true)
	if !req.Force {
		existing, err := s.db.GetByURL(req.URL)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "database error")
			return
		}
		if existing != nil {
			// Mark as cached
			existing.Cached = true
			respondJSON(w, http.StatusOK, existing)
			return
		}
	}

	// Scrape the URL
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	result, err := s.scraper.Scrape(ctx, req.URL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("scraping failed: %v", err))
		return
	}

	// Save to database
	if err := s.db.SaveScrapedData(result); err != nil {
		log.Printf("Failed to save data: %v", err)
		// Still return the result even if save fails
	}

	respondJSON(w, http.StatusOK, result)
}

// ExtractLinksRequest represents an extract links request
type ExtractLinksRequest struct {
	URL string `json:"url"`
}

// ExtractLinksResponse represents an extract links response
type ExtractLinksResponse struct {
	URL   string   `json:"url"`
	Links []string `json:"links"`
	Count int      `json:"count"`
}

// handleExtractLinks handles link extraction and sanitization
func (s *Server) handleExtractLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ExtractLinksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.URL == "" {
		respondError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Extract and sanitize links
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	links, err := s.scraper.ExtractLinks(ctx, req.URL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("link extraction failed: %v", err))
		return
	}

	response := ExtractLinksResponse{
		URL:   req.URL,
		Links: links,
		Count: len(links),
	}

	respondJSON(w, http.StatusOK, response)
}

// handleScore handles content scoring requests
func (s *Server) handleScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req models.ScoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.URL == "" {
		respondError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Score the content
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	score, err := s.scraper.ScoreLinkContent(ctx, req.URL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("scoring failed: %v", err))
		return
	}

	response := models.ScoreResponse{
		URL:   req.URL,
		Score: *score,
	}

	respondJSON(w, http.StatusOK, response)
}

// BatchScrapeRequest represents a batch scrape request
type BatchScrapeRequest struct {
	URLs  []string `json:"urls"`
	Force bool     `json:"force"`
}

// BatchScrapeResponse represents a batch scrape response
type BatchScrapeResponse struct {
	Results []BatchResult `json:"results"`
	Summary BatchSummary  `json:"summary"`
}

// BatchResult represents a single result in a batch
type BatchResult struct {
	URL      string              `json:"url"`
	Success  bool                `json:"success"`
	Data     *models.ScrapedData `json:"data,omitempty"`
	Error    string              `json:"error,omitempty"`
	Cached   bool                `json:"cached"`
	Warnings []string            `json:"warnings,omitempty"` // Non-fatal issues during processing
}

// BatchSummary provides summary statistics
type BatchSummary struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Cached  int `json:"cached"`
	Scraped int `json:"scraped"`
}

// handleBatchScrape handles batch URL scraping
func (s *Server) handleBatchScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req BatchScrapeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.URLs) == 0 {
		respondError(w, http.StatusBadRequest, "urls array is required")
		return
	}

	if len(req.URLs) > 50 {
		respondError(w, http.StatusBadRequest, "maximum 50 URLs per batch")
		return
	}

	// Process URLs concurrently
	results := make([]BatchResult, len(req.URLs))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, url := range req.URLs {
		wg.Add(1)
		go func(index int, targetURL string) {
			defer wg.Done()

			result := s.processSingleURL(r.Context(), targetURL, req.Force)

			mu.Lock()
			results[index] = result
			mu.Unlock()
		}(i, url)
	}

	wg.Wait()

	// Calculate summary
	summary := BatchSummary{Total: len(results)}
	for _, r := range results {
		if r.Success {
			summary.Success++
			if r.Cached {
				summary.Cached++
			} else {
				summary.Scraped++
			}
		} else {
			summary.Failed++
		}
	}

	response := BatchScrapeResponse{
		Results: results,
		Summary: summary,
	}

	respondJSON(w, http.StatusOK, response)
}

// processSingleURL processes a single URL for batch scraping
func (s *Server) processSingleURL(ctx context.Context, url string, force bool) BatchResult {
	// Check cache first
	if !force {
		existing, err := s.db.GetByURL(url)
		if err == nil && existing != nil {
			// Mark as cached in the response
			existing.Cached = true
			return BatchResult{
				URL:     url,
				Success: true,
				Data:    existing,
				Cached:  true,
			}
		}
	}

	// Scrape the URL
	scrapeCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	result, err := s.scraper.Scrape(scrapeCtx, url)
	if err != nil {
		return BatchResult{
			URL:     url,
			Success: false,
			Error:   err.Error(),
		}
	}

	// Save to database
	if err := s.db.SaveScrapedData(result); err != nil {
		log.Printf("Failed to save data for %s: %v", url, err)
	}

	return BatchResult{
		URL:      url,
		Success:  true,
		Data:     result,
		Cached:   false,
		Warnings: result.Warnings,
	}
}

// handleData handles GET (by ID) and DELETE operations
func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/data/")
	if path == "" {
		respondError(w, http.StatusBadRequest, "id is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGetByID(w, r, path)
	case http.MethodDelete:
		s.handleDeleteByID(w, r, path)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleGetByID retrieves data by ID
func (s *Server) handleGetByID(w http.ResponseWriter, r *http.Request, id string) {
	data, err := s.db.GetByID(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database error")
		return
	}

	if data == nil {
		respondError(w, http.StatusNotFound, "data not found")
		return
	}

	// Mark as cached since it's from database
	data.Cached = true
	respondJSON(w, http.StatusOK, data)
}

// handleDeleteByID deletes data by ID
func (s *Server) handleDeleteByID(w http.ResponseWriter, r *http.Request, id string) {
	err := s.db.DeleteByID(id)
	if err != nil {
		if strings.Contains(err.Error(), "no data found") {
			respondError(w, http.StatusNotFound, "data not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to delete data")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "data deleted successfully",
	})
}

// handleList lists all scraped data with pagination
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse pagination parameters
	limit := 20
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		fmt.Sscanf(offsetStr, "%d", &offset)
	}

	// Enforce reasonable limits
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	data, err := s.db.List(limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database error")
		return
	}

	// Mark all as cached since they're from database
	for _, item := range data {
		item.Cached = true
	}

	count, _ := s.db.Count()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":   data,
		"total":  count,
		"limit":  limit,
		"offset": offset,
	})
}

// respondJSON sends a JSON response
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// respondError sends an error response
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{
		"error": message,
	})
}

// handleImage handles GET operations for individual images
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/images/")
	if path == "" {
		respondError(w, http.StatusBadRequest, "id is required")
		return
	}

	image, err := s.db.GetImageByID(path)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database error")
		return
	}

	if image == nil {
		respondError(w, http.StatusNotFound, "image not found")
		return
	}

	respondJSON(w, http.StatusOK, image)
}

// ImageSearchRequest represents a search request for images by tags
type ImageSearchRequest struct {
	Tags []string `json:"tags"`
}

// ImageSearchResponse represents the response for image search
type ImageSearchResponse struct {
	Images []*models.ImageInfo `json:"images"`
	Count  int                 `json:"count"`
}

// handleImageSearch handles POST requests to search images by tags
func (s *Server) handleImageSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ImageSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Tags) == 0 {
		respondError(w, http.StatusBadRequest, "tags array is required and must not be empty")
		return
	}

	images, err := s.db.SearchImagesByTags(req.Tags)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database error")
		return
	}

	response := ImageSearchResponse{
		Images: images,
		Count:  len(images),
	}

	respondJSON(w, http.StatusOK, response)
}
