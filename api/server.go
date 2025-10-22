package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/zombar/scraper"
	"github.com/zombar/scraper/db"
	"github.com/zombar/scraper/models"
	"github.com/zombar/scraper/storage"
)

// Server represents the API server
type Server struct {
	db          *db.DB
	scraper     *scraper.Scraper
	storage     *storage.Storage
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

	// Initialize filesystem storage
	storageConfig := storage.Config{
		BasePath: config.ScraperConfig.StoragePath,
	}
	storageInstance, err := storage.New(storageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	// Initialize scraper with database and storage
	scraperInstance := scraper.New(config.ScraperConfig, database, storageInstance)

	s := &Server{
		db:          database,
		scraper:     scraperInstance,
		storage:     storageInstance,
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
	s.mux.HandleFunc("/api/extract-links", s.handleExtractLinks)
	s.mux.HandleFunc("/api/score", s.handleScore)
	s.mux.HandleFunc("/api/data/", s.handleData) // Handles /api/data/{id}
	s.mux.HandleFunc("/api/data", s.handleList)
	s.mux.HandleFunc("/api/images/search", s.handleImageSearch)
	s.mux.HandleFunc("/api/images/", s.handleImage) // Handles /api/images/{id} and /api/images/{id}/file
	s.mux.HandleFunc("/api/scrapes/", s.handleScrapeImages) // Handles /api/scrapes/{id}/images and /api/scrapes/{id}/content
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

		// Logging (skip health checks to reduce noise)
		start := time.Now()
		if r.URL.Path != "/health" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}

		next.ServeHTTP(w, r)

		if r.URL.Path != "/health" {
			log.Printf("%s %s - completed in %v", r.Method, r.URL.Path, time.Since(start))
		}
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

// handleImage handles GET, DELETE, and tombstone operations for individual images
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	// Extract path from URL
	path := strings.TrimPrefix(r.URL.Path, "/api/images/")
	if path == "" {
		respondError(w, http.StatusBadRequest, "id is required")
		return
	}

	// Check if this is a file serving request
	if strings.HasSuffix(path, "/file") {
		id := strings.TrimSuffix(path, "/file")
		if r.Method == http.MethodGet {
			s.handleServeImageFile(w, r, id)
		} else {
			respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Check if this is a tombstone operation
	if strings.HasSuffix(path, "/tombstone") {
		id := strings.TrimSuffix(path, "/tombstone")
		switch r.Method {
		case http.MethodPut:
			s.handleTombstoneImage(w, r, id)
		case http.MethodDelete:
			s.handleUntombstoneImage(w, r, id)
		default:
			respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Regular GET/DELETE operations
	switch r.Method {
	case http.MethodGet:
		s.handleGetImage(w, r, path)
	case http.MethodDelete:
		s.handleDeleteImage(w, r, path)
	default:
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleGetImage retrieves an image by ID
func (s *Server) handleGetImage(w http.ResponseWriter, r *http.Request, id string) {
	image, err := s.db.GetImageByID(id)
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

// handleDeleteImage deletes an image by ID
func (s *Server) handleDeleteImage(w http.ResponseWriter, r *http.Request, id string) {
	err := s.db.DeleteImageByID(id)
	if err != nil {
		if strings.Contains(err.Error(), "no image found") || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "image not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to delete image")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "image deleted successfully",
	})
}

// handleTombstoneImage tombstones an image by ID
func (s *Server) handleTombstoneImage(w http.ResponseWriter, r *http.Request, id string) {
	err := s.db.TombstoneImageByID(id)
	if err != nil {
		if strings.Contains(err.Error(), "no image found") || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "image not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to tombstone image")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "image tombstoned successfully",
	})
}

// handleUntombstoneImage removes tombstone from an image by ID
func (s *Server) handleUntombstoneImage(w http.ResponseWriter, r *http.Request, id string) {
	err := s.db.UntombstoneImageByID(id)
	if err != nil {
		if strings.Contains(err.Error(), "no image found") || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "image not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to untombstone image")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "image untombstoned successfully",
	})
}

// handleServeImageFile serves an image file from filesystem storage
func (s *Server) handleServeImageFile(w http.ResponseWriter, r *http.Request, id string) {
	// Get image metadata from database
	image, err := s.db.GetImageByID(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database error")
		return
	}

	if image == nil {
		respondError(w, http.StatusNotFound, "image not found")
		return
	}

	// Check if image has a file path
	if image.FilePath == "" {
		// Fallback to base64 data if file path not available
		if image.Base64Data == "" {
			respondError(w, http.StatusNotFound, "image file not available")
			return
		}
		// Serve base64 data as fallback (legacy support)
		respondError(w, http.StatusNotFound, "image file not available in filesystem storage")
		return
	}

	// Read image from storage
	imageData, err := s.storage.ReadImage(image.FilePath)
	if err != nil {
		log.Printf("Failed to read image file %s: %v", image.FilePath, err)
		respondError(w, http.StatusInternalServerError, "failed to read image file")
		return
	}

	// Set content type header
	if image.ContentType != "" {
		w.Header().Set("Content-Type", image.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	// Set content length header
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(imageData)))

	// Set cache control headers (cache for 1 year since images don't change)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	// Write image data
	w.WriteHeader(http.StatusOK)
	w.Write(imageData)
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

// handleScrapeImages handles GET requests to retrieve images for a specific scrape ID
func (s *Server) handleScrapeImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract path from URL
	path := strings.TrimPrefix(r.URL.Path, "/api/scrapes/")

	// Check if this is a content serving request
	if strings.HasSuffix(path, "/content") {
		id := strings.TrimSuffix(path, "/content")
		s.handleServeContent(w, r, id)
		return
	}

	// Extract scrape ID from path - format: /api/scrapes/{id}/images
	path = strings.TrimSuffix(path, "/images")

	if path == "" || path == r.URL.Path {
		respondError(w, http.StatusBadRequest, "scrape id is required")
		return
	}

	images, err := s.db.GetImagesByScrapeID(path)
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
// handleServeContent serves HTML content from filesystem storage
func (s *Server) handleServeContent(w http.ResponseWriter, r *http.Request, id string) {
	// Get scraped data from database
	data, err := s.db.GetByID(id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "database error")
		return
	}

	if data == nil {
		respondError(w, http.StatusNotFound, "scrape not found")
		return
	}

	// Check if content was saved to filesystem
	if data.Slug == "" {
		respondError(w, http.StatusNotFound, "content file not available")
		return
	}

	// Set content type header
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Set cache control headers
	w.Header().Set("Cache-Control", "public, max-age=3600")

	// Create simple HTML response with the content
	html := fmt.Sprintf(`<!DOCTYPE html>
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
</html>`, data.Title, data.Metadata.Description, data.Title, data.Content)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}
