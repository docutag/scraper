package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/docutag/platform/pkg/metrics"
	"github.com/docutag/platform/pkg/tracing"
	"github.com/docutag/scraper"
	"github.com/docutag/scraper/api"
	"github.com/docutag/scraper/db"
)

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	// Setup structured logging with JSON output
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("scraper service initializing", "version", "1.0.0")

	// Initialize tracing
	tp, err := tracing.InitTracer("docutab-scraper")
	if err != nil {
		logger.Warn("failed to initialize tracer, continuing without tracing", "error", err)
	} else {
		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				logger.Error("error shutting down tracer", "error", err)
			}
		}()
		logger.Info("tracing initialized successfully")
	}

	// Default values
	defaultPort := getEnv("PORT", "8080")
	defaultStoragePath := getEnv("STORAGE_BASE_PATH", "./storage")
	defaultOllamaURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	defaultOllamaModel := getEnv("OLLAMA_MODEL", "gpt-oss:20b")
	defaultOllamaVisionModel := getEnv("OLLAMA_VISION_MODEL", defaultOllamaModel) // Default to same as text model if not specified
	defaultLinkScoreThreshold := getEnv("LINK_SCORE_THRESHOLD", "0.5")
	defaultMaxImages := getEnv("MAX_IMAGES", "20")

	// Parse link score threshold
	linkScoreThreshold, err := strconv.ParseFloat(defaultLinkScoreThreshold, 64)
	if err != nil {
		logger.Warn("invalid LINK_SCORE_THRESHOLD value, using default",
			"provided", defaultLinkScoreThreshold,
			"default", 0.5,
			"error", err,
		)
		linkScoreThreshold = 0.5
	}

	// Parse max images limit
	maxImages, err := strconv.Atoi(defaultMaxImages)
	if err != nil {
		logger.Warn("invalid MAX_IMAGES value, using default",
			"provided", defaultMaxImages,
			"default", 20,
			"error", err,
		)
		maxImages = 20
	}
	if maxImages < 0 {
		logger.Warn("MAX_IMAGES cannot be negative, using default", "provided", maxImages, "default", 20)
		maxImages = 20
	}

	// Command-line flags (override environment variables)
	port := flag.String("port", defaultPort, "Server port")
	ollamaURL := flag.String("ollama-url", defaultOllamaURL, "Ollama base URL")
	ollamaModel := flag.String("ollama-model", defaultOllamaModel, "Ollama model to use for text generation")
	ollamaVisionModel := flag.String("ollama-vision-model", defaultOllamaVisionModel, "Ollama model to use for vision tasks")
	scoreThreshold := flag.Float64("link-score-threshold", linkScoreThreshold, "Minimum score for link recommendation (0.0-1.0)")
	disableCORS := flag.Bool("disable-cors", false, "Disable CORS")
	disableImageAnalysis := flag.Bool("disable-image-analysis", false, "Disable AI-powered image analysis")
	flag.Parse()

	// PostgreSQL database configuration (required)
	dbHost := getEnv("DB_HOST", "")
	if dbHost == "" {
		logger.Error("DB_HOST environment variable is required")
		os.Exit(1)
	}

	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "docutab")
	dbPassword := getEnv("DB_PASSWORD", "docutab_dev_pass")
	dbName := getEnv("DB_NAME", "docutab")

	dbConfig := db.Config{
		DSN: fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", dbHost, dbPort, dbUser, dbPassword, dbName),
	}
	logger.Info("using PostgreSQL database", "host", dbHost, "port", dbPort, "database", dbName)

	// Create server configuration
	config := api.Config{
		Addr:     ":" + *port,
		DBConfig: dbConfig,
		ScraperConfig: scraper.Config{
			HTTPTimeout:         30 * time.Second,
			OllamaBaseURL:       *ollamaURL,
			OllamaModel:         *ollamaModel,
			OllamaVisionModel:   *ollamaVisionModel,
			EnableImageAnalysis: !*disableImageAnalysis,
			MaxImageSizeBytes:   10 * 1024 * 1024, // 10MB
			ImageTimeout:        15 * time.Second,
			LinkScoreThreshold:  *scoreThreshold,
			StoragePath:         defaultStoragePath,
			MaxImages:           maxImages, // Maximum images to download per scrape
		},
		CORSEnabled: !*disableCORS,
	}

	// Create server
	server, err := api.NewServer(config)
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	// Initialize database metrics
	dbMetrics := metrics.NewDatabaseMetrics("scraper")
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			dbMetrics.UpdateDBStats(server.DB().DB())
		}
	}()
	logger.Info("database metrics initialized")

	// Initialize image metrics updater
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			server.UpdateImageMetrics()
		}
	}()
	logger.Info("image metrics initialized")

	// Start server in a goroutine
	go func() {
		logger.Info("scraper service starting",
			"port", *port,
			"database_host", dbHost,
			"database_name", dbName,
			"storage_path", defaultStoragePath,
			"ollama_url", *ollamaURL,
			"ollama_model", *ollamaModel,
			"ollama_vision_model", *ollamaVisionModel,
			"link_score_threshold", *scoreThreshold,
			"max_images", maxImages,
			"image_analysis_enabled", !*disableImageAnalysis,
		)

		if err := server.Start(); err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Graceful shutdown
	logger.Info("shutting down gracefully")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}
