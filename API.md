# Scraper API Reference

REST API documentation for the web scraper service.

## Base URL

```
http://localhost:8080
```

## Endpoints

### Health Check

Check server status and get database statistics.

**Request:**
```http
GET /health
```

**Response:**
```json
{
  "status": "healthy",
  "count": 42,
  "time": "2024-01-15T14:23:45Z"
}
```

---

### Scrape Single URL

Scrape a single URL. Returns cached result if previously scraped.

**Request:**
```http
POST /api/scrape
Content-Type: application/json

{
  "url": "https://example.com",
  "force": false
}
```

**Parameters:**
- `url` (string, required) - URL to scrape
- `force` (boolean, optional) - Bypass cache and re-scrape (default: false)

**Response:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "url": "https://example.com",
  "title": "Example Domain",
  "content": "AI-cleaned main content...",
  "images": [
    {
      "url": "https://example.com/image.jpg",
      "alt_text": "Example image",
      "summary": "AI-generated description...",
      "tags": ["example", "illustration"]
    }
  ],
  "links": [
    "https://example.com/about",
    "https://example.com/contact"
  ],
  "fetched_at": "2024-01-15T14:23:45Z",
  "created_at": "2024-01-15T14:23:45Z",
  "processing_time_seconds": 8.34,
  "cached": false,
  "metadata": {
    "description": "Example domain description",
    "keywords": ["example", "domain"],
    "author": "Example Author",
    "published_date": "2024-01-15"
  },
  "score": {
    "url": "https://example.com",
    "score": 0.85,
    "reason": "High-quality informative content suitable for knowledge database",
    "categories": ["informational", "reference"],
    "is_recommended": true,
    "malicious_indicators": [],
    "ai_used": true
  }
}
```

**Note:** The `score` field contains quality assessment of the scraped content (0.0-1.0 scale). Uses AI-powered scoring when Ollama is available, otherwise falls back to rule-based heuristics. Always present unless service fails.

**Example:**
```bash
curl -X POST http://localhost:8080/api/scrape \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com"}'
```

---

### Batch Scrape

Scrape multiple URLs concurrently (maximum 50 per request).

**Request:**
```http
POST /api/scrape/batch
Content-Type: application/json

{
  "urls": [
    "https://example.com",
    "https://example.org"
  ],
  "force": false
}
```

**Parameters:**
- `urls` (array of strings, required) - URLs to scrape (max 50)
- `force` (boolean, optional) - Bypass cache for all URLs (default: false)

**Response:**
```json
{
  "results": [
    {
      "url": "https://example.com",
      "success": true,
      "data": { ... },
      "cached": true
    },
    {
      "url": "https://example.org",
      "success": true,
      "data": { ... },
      "cached": false
    },
    {
      "url": "https://invalid-url",
      "success": false,
      "error": "failed to fetch page",
      "cached": false
    }
  ],
  "summary": {
    "total": 3,
    "success": 2,
    "failed": 1,
    "cached": 1,
    "scraped": 1
  }
}
```

**Example:**
```bash
curl -X POST http://localhost:8080/api/scrape/batch \
  -H "Content-Type: application/json" \
  -d '{
    "urls": ["https://example.com", "https://example.org"]
  }'
```

---

### Get by ID

Retrieve scraped data by UUID.

**Request:**
```http
GET /api/data/{id}
```

**Response:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "url": "https://example.com",
  "title": "Page Title",
  ...
}
```

**Error Response (404):**
```json
{
  "error": "data not found"
}
```

**Example:**
```bash
curl http://localhost:8080/api/data/550e8400-e29b-41d4-a716-446655440000
```

---

### Delete by ID

Delete scraped data by UUID.

**Request:**
```http
DELETE /api/data/{id}
```

**Response:**
```json
{
  "message": "data deleted successfully"
}
```

**Error Response (404):**
```json
{
  "error": "data not found"
}
```

**Example:**
```bash
curl -X DELETE http://localhost:8080/api/data/550e8400-e29b-41d4-a716-446655440000
```

---

### Get Image by ID

Retrieve a specific image by its UUID.

**Request:**
```http
GET /api/images/{id}
```

**Response:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "url": "https://example.com/image.jpg",
  "alt_text": "Example image",
  "summary": "AI-generated 4-5 sentence description of the image...",
  "tags": ["example", "illustration", "diagram"],
  "base64_data": "iVBORw0KGgoAAAANSUhEUgAAAAEA..."
}
```

**Error Response (404):**
```json
{
  "error": "image not found"
}
```

**Example:**
```bash
curl http://localhost:8080/api/images/550e8400-e29b-41d4-a716-446655440000
```

---

### Search Images by Tags

Search for images using fuzzy tag matching (case-insensitive substring matching).

**Request:**
```http
POST /api/images/search
Content-Type: application/json

{
  "tags": ["cat", "animal"]
}
```

**Parameters:**
- `tags` (array of strings, required) - Tags to search for (fuzzy matching)

**Response:**
```json
{
  "images": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "url": "https://example.com/cat.jpg",
      "alt_text": "A cat photo",
      "summary": "Image shows a domestic cat...",
      "tags": ["cat", "animal", "pet"],
      "base64_data": "iVBORw0KGgoAAAANSUhEUgAAAAEA..."
    },
    {
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "url": "https://example.com/wildlife.jpg",
      "alt_text": "Wildlife scene",
      "summary": "Image depicts various animals in nature...",
      "tags": ["animals", "wildlife", "nature"],
      "base64_data": "iVBORw0KGgoAAAANSUhEUgAAAAEA..."
    }
  ],
  "count": 2
}
```

**Fuzzy Matching:** Searches are case-insensitive and match substrings. For example:
- Searching for "cat" will match images with tags: "cat", "cats", "wildcat", "scatter"
- Searching for "anim" will match images with tags: "animal", "animation", "animals"

**Example:**
```bash
curl -X POST http://localhost:8080/api/images/search \
  -H "Content-Type: application/json" \
  -d '{"tags": ["cat", "dog"]}'
```

---

### Get Images by Scrape ID

Retrieve all images associated with a specific scrape operation.

**Request:**
```http
GET /api/scrapes/{id}/images
```

**Parameters:**
- `id` (string, required) - Scrape ID (UUID from scraped data)

**Response:**
```json
{
  "images": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "url": "https://example.com/image1.jpg",
      "alt_text": "First image",
      "summary": "Image shows a diagram explaining...",
      "tags": ["diagram", "technical"],
      "base64_data": "iVBORw0KGgoAAAANSUhEUgAAAAEA..."
    },
    {
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "url": "https://example.com/image2.png",
      "alt_text": "Second image",
      "summary": "Graph depicting performance metrics...",
      "tags": ["chart", "data"],
      "base64_data": "iVBORw0KGgoAAAANSUhEUgAAAAEA..."
    }
  ],
  "count": 2
}
```

**Empty Response:**
```json
{
  "images": [],
  "count": 0
}
```

**Error Response (400):**
```json
{
  "error": "scrape id is required"
}
```

**Example:**
```bash
curl http://localhost:8080/api/scrapes/550e8400-e29b-41d4-a716-446655440000/images
```

**Use Case:** Retrieve all images from a specific scraping operation. The scrape ID is the `id` field returned when scraping a URL.

---

### Delete Image

Permanently delete an image from the database.

**Request:**
```http
DELETE /api/images/{id}
```

**Parameters:**
- `id` (string, required) - Image UUID

**Response:**
```json
{
  "message": "image deleted successfully"
}
```

**Error Response (404):**
```json
{
  "error": "no image found with id: {id}"
}
```

**Example:**
```bash
curl -X DELETE http://localhost:8080/api/images/550e8400-e29b-41d4-a716-446655440000
```

**Notes:**
- This operation is permanent and cannot be undone
- Image must exist in the database

---

### Tombstone Image

Mark an image as scheduled for deletion by adding `tombstone_datetime` to its record. This is a soft delete that can be undone.

**Request:**
```http
PUT /api/images/{id}/tombstone
```

**Parameters:**
- `id` (string, required) - Image UUID

**Response:**
```json
{
  "message": "image tombstoned successfully"
}
```

**Error Response (404):**
```json
{
  "error": "no image found with id: {id}"
}
```

**Example:**
```bash
curl -X PUT http://localhost:8080/api/images/550e8400-e29b-41d4-a716-446655440000/tombstone
```

**Notes:**
- Adds `tombstone_datetime` field to image record
- Image remains in database and can be retrieved
- Can be undone using the untombstone endpoint
- Image will include `tombstone_datetime` in API responses

---

### Untombstone Image

Remove tombstone status from an image by deleting the `tombstone_datetime` field.

**Request:**
```http
DELETE /api/images/{id}/tombstone
```

**Parameters:**
- `id` (string, required) - Image UUID

**Response:**
```json
{
  "message": "image untombstoned successfully"
}
```

**Error Response (404):**
```json
{
  "error": "no image found with id: {id}"
}
```

**Example:**
```bash
curl -X DELETE http://localhost:8080/api/images/550e8400-e29b-41d4-a716-446655440000/tombstone
```

**Use Case:** Restore an image that was marked for deletion. The image returns to normal status.

---

### List All Data

List all scraped data with pagination.

**Request:**
```http
GET /api/data?limit=20&offset=0
```

**Query Parameters:**
- `limit` (integer, optional) - Results per page (default: 20, max: 100)
- `offset` (integer, optional) - Number of results to skip (default: 0)

**Response:**
```json
{
  "data": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "url": "https://example.com",
      ...
    }
  ],
  "total": 150,
  "limit": 20,
  "offset": 0
}
```

**Example:**
```bash
# First page
curl "http://localhost:8080/api/data?limit=20&offset=0"

# Second page
curl "http://localhost:8080/api/data?limit=20&offset=20"
```

---

### Score Link Content

Score a URL to determine if it should be ingested into the database. Uses AI to assess content quality and identify potentially malicious, spam, or inappropriate content.

**Request:**
```http
POST /api/score
Content-Type: application/json

{
  "url": "https://example.com/article"
}
```

**Parameters:**
- `url` (string, required) - URL to score

**Response:**
```json
{
  "url": "https://example.com/article",
  "score": {
    "url": "https://example.com/article",
    "score": 0.85,
    "reason": "High quality technical article with educational content",
    "categories": ["technical", "education"],
    "is_recommended": true,
    "malicious_indicators": [],
    "ai_used": true
  }
}
```

**Score Field Description:**
- `score` (float) - Quality score from 0.0 to 1.0, where:
  - 1.0 = High quality, substantive content
  - 0.5-0.9 = Moderate quality content
  - 0.0-0.4 = Low quality or inappropriate content
- `reason` (string) - Explanation for the assigned score
- `categories` (array) - Detected content categories (e.g., "news", "social_media", "gambling", "spam")
- `is_recommended` (boolean) - Whether the link meets the quality threshold for ingestion
- `malicious_indicators` (array) - Any detected suspicious patterns (e.g., "phishing", "malware", "scam")
- `ai_used` (boolean) - Whether AI (Ollama) was used for scoring (`true`) or rule-based fallback (`false`)

**Rejected Content Types:**
- Social media platforms
- Gambling websites
- Adult content / pornography
- Drug marketplaces
- Forums and chatrooms (except high-quality technical forums)
- General marketplaces
- Spam and clickbait
- Malicious websites

**Accepted Content Types:**
- News articles and journalism
- Educational content
- Research papers
- Technical documentation
- Blog posts with substantive content
- Government and official resources

**Example:**
```bash
curl -X POST http://localhost:8080/api/score \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com/article"}'
```

**Low Score Example Response:**
```json
{
  "url": "https://facebook.com",
  "score": {
    "url": "https://facebook.com",
    "score": 0.1,
    "reason": "Rule-based: Social media platform - not suitable for content ingestion",
    "categories": ["social_media"],
    "is_recommended": false,
    "malicious_indicators": [],
    "ai_used": false
  }
}
```

**Malicious Content Example:**
```json
{
  "url": "https://suspicious-site.com",
  "score": {
    "url": "https://suspicious-site.com",
    "score": 0.05,
    "reason": "Suspected phishing site with misleading content",
    "categories": ["malicious", "spam"],
    "is_recommended": false,
    "malicious_indicators": ["phishing", "suspicious_url"],
    "ai_used": true
  }
}
```

---

### Extract Links

Extract and sanitize links from a URL using pattern-based and AI filtering. Automatically filters out low-quality links (subscription pages, social media, media files) and category/section pages (e.g., `/arts`, `/world/asia`).

**Request:**
```http
POST /api/extract-links
Content-Type: application/json

{
  "url": "https://example.com"
}
```

**Parameters:**
- `url` (string, required) - URL to extract links from

**Filtering:**
The endpoint automatically filters out:
- Low-quality links (subscriptions, login pages, social media, media files)
- Category/section landing pages:
  - Single sections: `/arts`, `/business`, `/science`
  - Multi-level sections: `/news/world/asia`, `/news/world/europe`
  - Compound names: `/sciencetech/index.html`
  - Section indicators: `/section/politics`, `/category/tech`
- Archive pages: `/2024`, `/2024/01`, `/2024/01/15`
- Tag/topic pages: `/tag/ai`, `/topic/climate`
- Preserves article URLs with numeric IDs (e.g., `/news/world-middle-east-12345678`)

**Response:**
```json
{
  "url": "https://example.com",
  "links": [
    "https://example.com/article-1",
    "https://example.com/article-2"
  ],
  "count": 2
}
```

**Example:**
```bash
curl -X POST http://localhost:8080/api/extract-links \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com"}'
```

---

## Data Types

### ScrapedData

Main output structure containing all scraped content.

```go
type ScrapedData struct {
    ID              string        `json:"id"`
    URL             string        `json:"url"`
    Title           string        `json:"title"`
    Content         string        `json:"content"`
    Images          []ImageInfo   `json:"images"`
    Links           []string      `json:"links"`
    FetchedAt       time.Time     `json:"fetched_at"`
    CreatedAt       time.Time     `json:"created_at"`
    ProcessingTime  float64       `json:"processing_time_seconds"`
    Cached          bool          `json:"cached"`
    Metadata        PageMetadata  `json:"metadata"`
}
```

**Fields:**
- `id` - Unique UUID identifier
- `url` - Scraped URL
- `title` - Page title from `<title>` tag
- `content` - AI-cleaned main content
- `images` - Array of image information
- `links` - All extracted hyperlinks
- `fetched_at` - When content was originally fetched
- `created_at` - When record was created in database
- `processing_time_seconds` - Total processing time
- `cached` - Whether result was served from cache
- `metadata` - Additional page metadata

### ImageInfo

Information about an extracted image.

```go
type ImageInfo struct {
    ID                string     `json:"id,omitempty"`
    URL               string     `json:"url"`
    AltText           string     `json:"alt_text"`
    Summary           string     `json:"summary"`
    Tags              []string   `json:"tags"`
    Base64Data        string     `json:"base64_data,omitempty"`
    TombstoneDatetime *time.Time `json:"tombstone_datetime,omitempty"`
}
```

**Fields:**
- `id` - Unique UUID identifier for the image
- `url` - Absolute image URL
- `alt_text` - Alt text from `<img>` tag
- `summary` - AI-generated 4-5 sentence description
- `tags` - AI-generated tags for categorization
- `base64_data` - Base64-encoded image data (omitted in list responses for performance)
- `tombstone_datetime` - When the image was marked for deletion (omitted if not tombstoned)

### PageMetadata

Metadata extracted from HTML meta tags.

```go
type PageMetadata struct {
    Description   string   `json:"description,omitempty"`
    Keywords      []string `json:"keywords,omitempty"`
    Author        string   `json:"author,omitempty"`
    PublishedDate string   `json:"published_date,omitempty"`
}
```

### LinkScore

Quality assessment and scoring for a URL.

```go
type LinkScore struct {
    URL                 string   `json:"url"`
    Score               float64  `json:"score"`              // 0.0 to 1.0
    Reason              string   `json:"reason"`
    Categories          []string `json:"categories"`
    IsRecommended       bool     `json:"is_recommended"`
    MaliciousIndicators []string `json:"malicious_indicators,omitempty"`
    AIUsed              bool     `json:"ai_used"`
}
```

**Fields:**
- `url` - The URL that was scored
- `score` - Quality score from 0.0 (lowest) to 1.0 (highest)
- `reason` - Explanation for the assigned score
- `categories` - Content categories detected (e.g., "news", "technical", "social_media", "spam")
- `is_recommended` - Whether the URL meets the quality threshold for ingestion
- `malicious_indicators` - Any suspicious patterns detected (e.g., "phishing", "malware")
- `ai_used` - Whether AI (Ollama) was used for scoring (`true`) or rule-based fallback (`false`)

---

## Error Responses

All errors return JSON with an `error` field:

```json
{
  "error": "descriptive error message"
}
```

**HTTP Status Codes:**
- `200 OK` - Success
- `400 Bad Request` - Invalid request parameters
- `404 Not Found` - Resource not found
- `405 Method Not Allowed` - Wrong HTTP method
- `500 Internal Server Error` - Server error

---

## Integration Examples

### JavaScript/TypeScript

```typescript
// Scrape single URL
async function scrapeURL(url: string): Promise<ScrapedData> {
  const response = await fetch('http://localhost:8080/api/scrape', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url })
  });
  return response.json();
}

// Batch scrape
async function scrapeBatch(urls: string[]): Promise<BatchResponse> {
  const response = await fetch('http://localhost:8080/api/scrape/batch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ urls })
  });
  return response.json();
}

// Get by ID
async function getByID(id: string): Promise<ScrapedData> {
  const response = await fetch(`http://localhost:8080/api/data/${id}`);
  return response.json();
}

// List all with pagination
async function listAll(limit = 20, offset = 0): Promise<ListResponse> {
  const response = await fetch(
    `http://localhost:8080/api/data?limit=${limit}&offset=${offset}`
  );
  return response.json();
}

// Check cache freshness
async function checkCacheFreshness(url: string): Promise<void> {
  const data = await scrapeURL(url);
  const ageInHours = (Date.now() - new Date(data.created_at).getTime()) / (1000 * 60 * 60);

  if (data.cached && ageInHours > 24) {
    console.log('Data is stale, re-scraping...');
    const freshData = await fetch('http://localhost:8080/api/scrape', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url, force: true })
    }).then(res => res.json());
  }
}
```

### Python

```python
import requests

# Scrape single URL
def scrape_url(url: str, force: bool = False) -> dict:
    response = requests.post(
        'http://localhost:8080/api/scrape',
        json={'url': url, 'force': force}
    )
    return response.json()

# Batch scrape
def scrape_batch(urls: list[str], force: bool = False) -> dict:
    response = requests.post(
        'http://localhost:8080/api/scrape/batch',
        json={'urls': urls, 'force': force}
    )
    return response.json()

# Get by ID
def get_by_id(id: str) -> dict:
    response = requests.get(f'http://localhost:8080/api/data/{id}')
    return response.json()

# Delete by ID
def delete_by_id(id: str) -> dict:
    response = requests.delete(f'http://localhost:8080/api/data/{id}')
    return response.json()

# List with pagination
def list_all(limit: int = 20, offset: int = 0) -> dict:
    response = requests.get(
        f'http://localhost:8080/api/data?limit={limit}&offset={offset}'
    )
    return response.json()
```

### cURL

```bash
# Health check
curl http://localhost:8080/health

# Scrape URL
curl -X POST http://localhost:8080/api/scrape \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com"}'

# Force re-scrape
curl -X POST http://localhost:8080/api/scrape \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com", "force": true}'

# Batch scrape
curl -X POST http://localhost:8080/api/scrape/batch \
  -H "Content-Type: application/json" \
  -d '{"urls": ["https://example.com", "https://example.org"]}'

# Get by ID
curl http://localhost:8080/api/data/550e8400-e29b-41d4-a716-446655440000

# List with pagination
curl "http://localhost:8080/api/data?limit=10&offset=0"

# Delete by ID
curl -X DELETE http://localhost:8080/api/data/550e8400-e29b-41d4-a716-446655440000
```

---

## Configuration

### Command-Line Flags

```bash
./scraper-api [flags]
```

- `-port string` - Server port (default: "8080")
- `-db string` - Database file path (default: "scraper.db")
- `-ollama-url string` - Ollama base URL (default: "http://localhost:11434")
- `-ollama-model string` - Ollama model (default: "gpt-oss:20b")
- `-link-score-threshold float` - Minimum score for link recommendation (default: 0.5)
- `-disable-cors` - Disable CORS (enabled by default)
- `-disable-image-analysis` - Disable AI-powered image analysis

### Environment Variables

For production deployments, use environment variables:

```bash
export PORT="8080"
export DB_PATH="scraper.db"
export OLLAMA_URL="http://localhost:11434"
export OLLAMA_MODEL="gpt-oss:20b"
# export OLLAMA_VISION_MODEL="llama3.2-vision:latest"  # Optional: defaults to OLLAMA_MODEL
export LINK_SCORE_THRESHOLD="0.5"
```

**Configuration Options:**
- `PORT` - Server port number
- `DB_PATH` - Path to SQLite database file
- `OLLAMA_URL` - Base URL for Ollama API server
- `OLLAMA_MODEL` - Name of the Ollama model to use for text generation and content analysis
- `OLLAMA_VISION_MODEL` (optional) - Name of the Ollama model to use for image analysis. Must be a vision-capable model like llama3.2-vision, llava, or minicpm-v. Defaults to OLLAMA_MODEL if not specified.
- `LINK_SCORE_THRESHOLD` - Minimum quality score (0.0-1.0) for recommending a link for ingestion (default: 0.5)

---

## Performance

### Batch Processing

- Maximum 50 URLs per batch request
- URLs processed concurrently using goroutines
- Each URL has 2-minute timeout
- Failed URLs don't affect successful ones

### Caching

- URLs deduplicated using database unique constraint
- Cached results returned instantly
- Use `force: true` to bypass cache
- `cached` field indicates cache status
- `created_at` shows original scrape time

### Database

- Connection pool: 25 max open, 5 idle
- Connection lifetime: 5 minutes
- Prepared statements for queries
- Indexes on url and created_at

### Timeouts

- HTTP read timeout: 30 seconds
- HTTP write timeout: 120 seconds
- Idle timeout: 120 seconds
- Scraping timeout: 2 minutes per URL

---

## Database Schema

### scraped_data Table

```sql
CREATE TABLE scraped_data (
    id TEXT PRIMARY KEY,
    url TEXT NOT NULL UNIQUE,
    data TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### images Table

Images are stored separately from scraped data for efficient querying and retrieval.

```sql
CREATE TABLE images (
    id TEXT PRIMARY KEY,
    scrape_id TEXT NOT NULL,
    url TEXT NOT NULL,
    alt_text TEXT,
    summary TEXT,
    tags TEXT,
    base64_data TEXT,
    tombstone_datetime TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (scrape_id) REFERENCES scraped_data(id) ON DELETE CASCADE
);
```

**Note:** The `tags` field stores a JSON array of strings. The `tombstone_datetime` field marks images for deletion when set (soft delete). Images are automatically deleted when their parent scraped data is deleted (cascade delete).

### Indexes

**scraped_data:**
- `idx_scraped_data_url` on `url`
- `idx_scraped_data_created_at` on `created_at`

**images:**
- `idx_images_scrape_id` on `scrape_id`
- `idx_images_created_at` on `created_at`

### Migrations

Migrations are automatically applied on startup using a version-based system tracked in the `schema_migrations` table.

To add new migrations, edit `db/migrations.go` and add to the `migrations` slice.
