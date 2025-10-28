package scraper

import (
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TestHTTPClientUsesOtelTransport verifies the scraper's HTTP client
// is instrumented with otelhttp.Transport for trace propagation
func TestHTTPClientUsesOtelTransport(t *testing.T) {
	// Create a scraper instance
	scraper := New(Config{
		HTTPTimeout: 30,
	}, nil, nil)

	// Verify the HTTP client's transport is wrapped with otelhttp
	_, ok := scraper.httpClient.Transport.(*otelhttp.Transport)
	if !ok {
		t.Error("❌ Scraper HTTP client does not use otelhttp.Transport for trace propagation")
		t.Error("   This will cause traces to 'go dead' when the scraper fetches external URLs")
	} else {
		t.Log("✅ Scraper HTTP client correctly uses otelhttp.Transport")
		t.Log("   Trace context will be propagated when fetching external URLs")
	}
}
