# Scraper Service

[![Go Report Card](https://goreportcard.com/badge/github.com/docutag/platform)](https://goreportcard.com/report/github.com/docutag/platform)
[![Go Version](https://img.shields.io/github/go-mod/go-version/docutag/platform)](go.mod)

A Go-based web scraping service that extracts content, images, and metadata from web pages using Ollama AI models. Available as both a command-line tool and a REST API server with persistent storage.

## Features

- AI-powered content extraction using Ollama
- Image analysis with vision models
- Link and metadata extraction
- SQLite storage with caching
- Batch URL processing
- REST API with CORS support
- UUID-based resource identification

## Requirements

- Go 1.24 or higher
- [Ollama](https://ollama.ai) running locally
- GCC (for SQLite CGO compilation)

### Ollama Models

```bash
# Install required models
ollama pull llama3.2         # Text model
ollama pull llama3.2-vision  # Vision model for image analysis
```

## Installation

```bash
# Build CLI tool
go build -o scraper-bin

# Build API server
go build -o scraper-api ./cmd/api

# Using Make
make build        # CLI only
make build-api    # API server only
make build-both   # Both CLI and API
```

## Usage

### Command-Line Tool

```bash
# Basic usage
./scraper-bin -url "https://example.com" -pretty

# Custom configuration
./scraper-bin -url "https://example.com" \
  -timeout 60s \
  -ollama-url "http://localhost:11434" \
  -ollama-model "llama3.2"

# Save output to file
./scraper-bin -url "https://example.com" -pretty > output.json

# Using Make
make run URL=https://example.com
```

### API Server

```bash
# Start server (default port 8080)
./scraper-api

# Custom configuration
./scraper-api -addr :3000 -db ./data/scraper.db

# Using Make
make run-api
make run-api PORT=3000 DB=./data/scraper.db
```

### Command-Line Options

**CLI Tool:**
- `-url` (required) - URL to scrape
- `-timeout` - Request timeout (default: 120s)
- `-ollama-url` - Ollama base URL (default: http://localhost:11434)
- `-ollama-model` - Ollama model (default: llama3.2)
- `-pretty` - Pretty print JSON output

**API Server:**
- `-addr` - Server address (default: :8080)
- `-db` - Database file path (default: scraper.db)
- `-ollama-url` - Ollama base URL (default: http://localhost:11434)
- `-ollama-model` - Ollama model (default: llama3.2)
- `-disable-cors` - Disable CORS support

## Output Format

The scraper returns structured JSON data:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "url": "https://example.com",
  "title": "Page Title",
  "content": "AI-cleaned main content...",
  "images": [
    {
      "url": "https://example.com/image.jpg",
      "alt_text": "Image description",
      "summary": "AI-generated description",
      "tags": ["tag1", "tag2"]
    }
  ],
  "links": ["https://example.com/page1"],
  "fetched_at": "2024-01-01T12:00:00Z",
  "created_at": "2024-01-01T12:00:00Z",
  "processing_time_seconds": 3.45,
  "cached": false,
  "metadata": {
    "description": "Page meta description",
    "keywords": ["keyword1", "keyword2"],
    "author": "Author Name",
    "published_date": "2024-01-01"
  }
}
```

## Architecture

### Package Structure

- **models/** - Data structures and types
- **ollama/** - Ollama API client implementation
- **scraper/** - Core scraping logic
- **db/** - Database layer with migrations
- **api/** - REST API server implementation
- **cmd/** - Application entry points

### Processing Pipeline

1. Fetch HTML content from target URL
2. Parse HTML structure
3. Extract title, text, images, links, and metadata
4. Clean content using Ollama AI
5. Analyze images with Ollama vision
6. Return structured JSON data

### Error Handling

The scraper handles various error conditions:
- Invalid URLs
- Network timeouts
- HTTP errors (404, 500, etc.)
- Ollama connection issues
- Malformed HTML
- Image download failures

Image processing errors are isolated and do not fail the entire operation. If AI content extraction fails, the scraper falls back to raw text extraction.

## Development

### Make Commands

```bash
make help           # Show all available commands
make build          # Build CLI application
make build-api      # Build API server
make build-both     # Build both
make test           # Run tests
make test-coverage  # Generate coverage report
make coverage-html  # Generate HTML coverage
make clean          # Remove build artifacts
make fmt            # Format code
make vet            # Run go vet
make check          # Format, vet, and test
make build-cross    # Cross-compile for multiple platforms
```

### Running Tests

```bash
# Run all tests
make test

# Generate coverage report
make test-coverage

# Generate HTML coverage report
make coverage-html

# Using Go directly
go test ./...
go test -v ./...
go test -cover ./...
```

### Database

The API server uses SQLite with automatic migrations. The database schema includes:

- `scraped_data` - Stores scraped content with UUID-based IDs
- `schema_migrations` - Tracks applied database migrations

URLs are deduplicated using a unique constraint. Cached results are returned for previously scraped URLs unless the `force` parameter is used.

### Switching to PostgreSQL

To use PostgreSQL instead of SQLite:

1. Add PostgreSQL driver:
   ```bash
   go get github.com/lib/pq
   ```

2. Update `db/db.go` import:
   ```go
   import _ "github.com/lib/pq"
   ```

3. Update SQL syntax in migrations (AUTOINCREMENT → SERIAL, DATETIME → TIMESTAMP)

4. Use PostgreSQL connection string:
   ```bash
   ./scraper-api -db "postgres://user:pass@localhost/scraper?sslmode=disable"
   ```

## Performance Considerations

- Default HTTP timeout: 120 seconds
- Concurrent batch processing (up to 50 URLs)
- Database connection pooling (25 max open, 5 idle)
- Image processing is sequential (can be parallelized)
- Processing time depends on Ollama hardware and model size

## API Documentation

See [API.md](API.md) for complete API reference including:
- Endpoint specifications
- Request/response formats
- Error handling
- Code examples
- Integration patterns

## License

This project is licensed under the MIT License - see the [LICENSE](../../LICENSE) file for details.
