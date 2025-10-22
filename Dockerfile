# Multi-stage build for optimal image size
FROM golang:1.24-alpine AS builder

# Install build dependencies (minimal)
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary (pure Go with modernc.org/sqlite)
RUN GOOS=linux go build -a -ldflags="-w -s" -o scraper-api ./cmd/api

# Final stage
FROM alpine:3.20

# Install minimal runtime dependencies
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 scraper && \
    adduser -D -u 1000 -G scraper scraper

# Create necessary directories
RUN mkdir -p /app/data /app/storage && \
    chown -R scraper:scraper /app

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /build/scraper-api .

# Copy timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Switch to non-root user
USER scraper

# Create volumes for persistent data
VOLUME /app/data
VOLUME /app/storage

# Expose API port
EXPOSE 8080

# Default to running the API server
CMD ["./scraper-api", "-addr", ":8080", "-db", "/app/data/scraper.db"]
