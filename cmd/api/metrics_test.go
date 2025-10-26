package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsEndpoint(t *testing.T) {
	// Create a request to the metrics endpoint
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	// Use the prometheus handler directly
	handler := promhttp.Handler()
	handler.ServeHTTP(w, req)

	// Check the response
	resp := w.Result()

	// Verify status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Expected content-type to contain 'text/plain', got '%s'", contentType)
	}

	// Verify response contains Prometheus metrics
	body := w.Body.String()
	
	// Check for standard Go runtime metrics
	expectedMetrics := []string{
		"go_goroutines",
		"go_threads",
		"go_info",
		"promhttp_metric_handler",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected metrics to contain '%s'", metric)
		}
	}
}
