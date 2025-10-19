package models

import (
	"encoding/json"
	"testing"
	"time"
)

// TestImageInfoJSONSerialization verifies that tombstone_datetime is properly serialized to JSON
func TestImageInfoJSONSerialization(t *testing.T) {
	now := time.Now().UTC()

	// Test with tombstone_datetime set
	imageWithTombstone := &ImageInfo{
		ID:                "test-img",
		URL:               "https://example.com/test.jpg",
		AltText:           "Test image",
		Tags:              []string{"test"},
		TombstoneDatetime: &now,
	}

	jsonBytes, err := json.Marshal(imageWithTombstone)
	if err != nil {
		t.Fatalf("Failed to marshal image with tombstone: %v", err)
	}

	jsonStr := string(jsonBytes)
	t.Logf("JSON with tombstone: %s", jsonStr)

	// Verify tombstone_datetime is in the JSON
	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if _, exists := unmarshaled["tombstone_datetime"]; !exists {
		t.Error("tombstone_datetime field is missing from JSON")
	}

	// Test without tombstone_datetime (should be omitted due to omitempty)
	imageWithoutTombstone := &ImageInfo{
		ID:      "test-img-2",
		URL:     "https://example.com/test2.jpg",
		AltText: "Test image 2",
		Tags:    []string{"test"},
	}

	jsonBytes2, err := json.Marshal(imageWithoutTombstone)
	if err != nil {
		t.Fatalf("Failed to marshal image without tombstone: %v", err)
	}

	jsonStr2 := string(jsonBytes2)
	t.Logf("JSON without tombstone: %s", jsonStr2)

	var unmarshaled2 map[string]interface{}
	if err := json.Unmarshal(jsonBytes2, &unmarshaled2); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if _, exists := unmarshaled2["tombstone_datetime"]; exists {
		t.Error("tombstone_datetime field should be omitted when nil")
	}
}
