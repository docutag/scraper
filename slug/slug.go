package slug

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Generate creates a URL-friendly slug from a string
func Generate(s string) string {
	if s == "" {
		return ""
	}

	// Convert to lowercase
	s = strings.ToLower(s)

	// Transliterate unicode to ASCII
	s = transliterate(s)

	// Replace spaces and underscores with hyphens
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove all non-alphanumeric characters except hyphens
	reg := regexp.MustCompile("[^a-z0-9-]+")
	s = reg.ReplaceAllString(s, "")

	// Remove consecutive hyphens
	reg = regexp.MustCompile("-+")
	s = reg.ReplaceAllString(s, "-")

	// Trim hyphens from start and end
	s = strings.Trim(s, "-")

	// Limit length to 100 characters
	if len(s) > 100 {
		s = s[:100]
		// Trim any trailing hyphen after truncation
		s = strings.TrimRight(s, "-")
	}

	return s
}

// GenerateWithFallback generates a slug, falling back to a default if the input produces an empty slug
func GenerateWithFallback(s, fallback string) string {
	slug := Generate(s)
	if slug == "" {
		return Generate(fallback)
	}
	return slug
}

// transliterate converts unicode characters to ASCII equivalents
func transliterate(s string) string {
	// Normalize unicode characters to NFD form (decomposed)
	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

// isMn checks if a rune is a nonspacing mark (accents, diacritics)
func isMn(r rune) bool {
	return unicode.Is(unicode.Mn, r)
}

// MakeUnique appends a number to a slug to make it unique
func MakeUnique(slug string, counter int) string {
	if counter == 0 {
		return slug
	}
	return slug + "-" + string(rune('0'+counter))
}

// FromImageURL generates a slug from an image URL
// Extracts the filename without extension and creates a slug
func FromImageURL(url string) string {
	// Extract filename from URL
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return ""
	}

	filename := parts[len(parts)-1]

	// Remove query parameters
	if idx := strings.Index(filename, "?"); idx != -1 {
		filename = filename[:idx]
	}

	// Remove file extension
	if idx := strings.LastIndex(filename, "."); idx != -1 {
		filename = filename[:idx]
	}

	return Generate(filename)
}

// FromImageInfo generates a slug from image metadata
// Tries alt text first, then falls back to URL
func FromImageInfo(altText, url string) string {
	if altText != "" {
		slug := Generate(altText)
		if slug != "" {
			return slug
		}
	}

	// Fallback to URL-based slug
	return FromImageURL(url)
}
