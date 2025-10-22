package slug

import (
	"testing"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "basic ascii",
			input:    "Hello World",
			expected: "hello-world",
		},
		{
			name:     "with punctuation",
			input:    "Hello, World!",
			expected: "hello-world",
		},
		{
			name:     "with multiple spaces",
			input:    "Hello   World   Test",
			expected: "hello-world-test",
		},
		{
			name:     "with unicode characters",
			input:    "Café München",
			expected: "cafe-munchen",
		},
		{
			name:     "with special characters",
			input:    "Hello@#$%World",
			expected: "helloworld",
		},
		{
			name:     "with leading/trailing spaces",
			input:    "  Hello World  ",
			expected: "hello-world",
		},
		{
			name:     "with hyphens",
			input:    "Hello-World-Test",
			expected: "hello-world-test",
		},
		{
			name:     "with underscores",
			input:    "Hello_World_Test",
			expected: "hello-world-test",
		},
		{
			name:     "very long string",
			input:    "This is a very long title that should be truncated to one hundred characters maximum for SEO purposes and URL readability",
			expected: "this-is-a-very-long-title-that-should-be-truncated-to-one-hundred-characters-maximum-for-seo-purpose",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only special characters",
			input:    "@#$%^&*()",
			expected: "",
		},
		{
			name:     "cyrillic characters",
			input:    "Привет Мир",
			expected: "", // Cyrillic chars are removed, not transliterated
		},
		{
			name:     "mixed case with numbers",
			input:    "Article 123 Test",
			expected: "article-123-test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Generate(tt.input)
			if result != tt.expected {
				t.Errorf("Generate(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGenerateWithFallback(t *testing.T) {
	tests := []struct {
		name     string
		primary  string
		fallback string
		expected string
	}{
		{
			name:     "use primary when valid",
			primary:  "Test Article",
			fallback: "https://example.com/article",
			expected: "test-article",
		},
		{
			name:     "use fallback when primary empty",
			primary:  "",
			fallback: "https://example.com/article",
			expected: "httpsexamplecomarticle", // Special chars removed
		},
		{
			name:     "use fallback when primary only special chars",
			primary:  "@#$%",
			fallback: "fallback-value",
			expected: "fallback-value",
		},
		{
			name:     "both empty returns empty",
			primary:  "",
			fallback: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateWithFallback(tt.primary, tt.fallback)
			if result != tt.expected {
				t.Errorf("GenerateWithFallback(%q, %q) = %q, want %q", tt.primary, tt.fallback, result, tt.expected)
			}
		})
	}
}

func TestFromImageInfo(t *testing.T) {
	tests := []struct {
		name     string
		altText  string
		url      string
		expected string
	}{
		{
			name:     "use alt text when available",
			altText:  "Beautiful Sunset",
			url:      "https://example.com/images/sunset.jpg",
			expected: "beautiful-sunset",
		},
		{
			name:     "use url when alt text empty",
			altText:  "",
			url:      "https://example.com/images/sunset.jpg",
			expected: "sunset", // FromImageURL extracts filename only
		},
		{
			name:     "use url when alt text only special chars",
			altText:  "!!!",
			url:      "https://example.com/images/photo.png",
			expected: "photo", // FromImageURL extracts filename only
		},
		{
			name:     "both empty",
			altText:  "",
			url:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FromImageInfo(tt.altText, tt.url)
			if result != tt.expected {
				t.Errorf("FromImageInfo(%q, %q) = %q, want %q", tt.altText, tt.url, result, tt.expected)
			}
		})
	}
}

func TestSlugUniqueness(t *testing.T) {
	// Test that similar inputs produce different slugs when needed
	inputs := []string{
		"Test Article",
		"Test Article 1",
		"Test Article 2",
	}

	slugs := make(map[string]bool)
	for _, input := range inputs {
		slug := Generate(input)
		if slugs[slug] {
			t.Errorf("Duplicate slug generated: %q for input %q", slug, input)
		}
		slugs[slug] = true
	}
}

func TestSlugLength(t *testing.T) {
	// Test that slugs are never longer than 100 characters
	longInput := "This is an extremely long title that goes on and on and should definitely be truncated because it exceeds the maximum allowed length for a URL slug which is one hundred characters"

	result := Generate(longInput)
	if len(result) > 100 {
		t.Errorf("Slug length %d exceeds maximum of 100 characters", len(result))
	}
}
