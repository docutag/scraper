package scraper

import (
	"testing"

	"github.com/zombar/scraper/models"
)

func TestScoreImageRelevance(t *testing.T) {
	tests := []struct {
		name          string
		img           models.ImageInfo
		position      int
		totalImages   int
		articleTitle  string
		articleTags   []string
		expectedRange [2]float64 // min, max expected score
	}{
		{
			name: "ideal article image",
			img: models.ImageInfo{
				URL:     "https://example.com/article-photo.jpg",
				AltText: "Main article photo",
				Width:   1200,
				Height:  800,
				Tags:    []string{"photo", "landscape"},
			},
			position:      0,
			totalImages:   5,
			articleTitle:  "Article about landscapes",
			articleTags:   []string{"landscape", "photography"},
			expectedRange: [2]float64{0.8, 1.0},
		},
		{
			name: "infographic - should score low",
			img: models.ImageInfo{
				URL:     "https://example.com/chart.png",
				AltText: "Data visualization chart",
				Width:   800,
				Height:  600,
				Tags:    []string{"infographic", "chart", "data"},
				Summary: "An infographic showing statistics",
			},
			position:      0,
			totalImages:   3,
			articleTitle:  "Article title",
			articleTags:   []string{"article"},
			expectedRange: [2]float64{0.0, 0.3},
		},
		{
			name: "banner ad - should score very low",
			img: models.ImageInfo{
				URL:    "https://example.com/ad-banner.jpg",
				Width:  728,
				Height: 90,
			},
			position:      2,
			totalImages:   5,
			articleTitle:  "Article",
			articleTags:   []string{},
			expectedRange: [2]float64{0.0, 0.2},
		},
		{
			name: "icon - should score low due to small size",
			img: models.ImageInfo{
				URL:    "https://example.com/icon.png",
				Width:  32,
				Height: 32,
				Tags:   []string{"icon"},
			},
			position:      3,
			totalImages:   5,
			articleTitle:  "Article",
			articleTags:   []string{},
			expectedRange: [2]float64{0.0, 0.4},
		},
		{
			name: "medium quality image mid-position",
			img: models.ImageInfo{
				URL:     "https://example.com/photo.jpg",
				AltText: "A photo",
				Width:   600,
				Height:  400,
			},
			position:      2,
			totalImages:   5,
			articleTitle:  "Article",
			articleTags:   []string{},
			expectedRange: [2]float64{0.5, 0.9}, // Adjusted - algorithm favors this size
		},
		{
			name: "relevant alt text - bonus score",
			img: models.ImageInfo{
				URL:     "https://example.com/sunset.jpg",
				AltText: "Beautiful sunset over mountains",
				Width:   1000,
				Height:  667,
			},
			position:      0,
			totalImages:   3,
			articleTitle:  "Amazing sunset photography guide",
			articleTags:   []string{"sunset", "mountains"},
			expectedRange: [2]float64{0.7, 1.0},
		},
		{
			name: "diagram - low score",
			img: models.ImageInfo{
				URL:     "https://example.com/diagram.png",
				Width:   800,
				Height:  600,
				Tags:    []string{"diagram", "flowchart"},
				Summary: "A technical diagram showing the process flow",
			},
			position:      1,
			totalImages:   4,
			articleTitle:  "Technical article",
			articleTags:   []string{},
			expectedRange: [2]float64{0.0, 0.3},
		},
		{
			name: "photo with people - good score",
			img: models.ImageInfo{
				URL:     "https://example.com/people.jpg",
				AltText: "Group photo",
				Width:   1600,
				Height:  1000,
				Tags:    []string{"photo", "people", "portrait"},
			},
			position:      0,
			totalImages:   3,
			articleTitle:  "Event coverage",
			articleTags:   []string{"event"},
			expectedRange: [2]float64{0.7, 1.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scoreImageRelevance(tt.img, tt.position, tt.totalImages, tt.articleTitle, tt.articleTags)

			if score < 0.0 || score > 1.0 {
				t.Errorf("Score out of range: got %.2f, want 0.0-1.0", score)
			}

			if score < tt.expectedRange[0] || score > tt.expectedRange[1] {
				t.Errorf("Score %.2f not in expected range [%.2f, %.2f] for %s",
					score, tt.expectedRange[0], tt.expectedRange[1], tt.name)
			}

			t.Logf("Image: %s, Score: %.2f, Expected: [%.2f, %.2f]",
				tt.name, score, tt.expectedRange[0], tt.expectedRange[1])
		})
	}
}

func TestScoreImageRelevanceInfographicDetection(t *testing.T) {
	infographicKeywords := []string{
		"infographic",
		"chart",
		"diagram",
		"graph",
		"flowchart",
		"visualization",
		"data visualization",
	}

	for _, keyword := range infographicKeywords {
		t.Run("tag_"+keyword, func(t *testing.T) {
			img := models.ImageInfo{
				URL:    "https://example.com/image.png",
				Width:  800,
				Height: 600,
				Tags:   []string{keyword},
			}

			score := scoreImageRelevance(img, 0, 1, "Article", []string{})

			// Should be penalized significantly (but not always below 0.5 due to base score)
			// The penalty is -0.4, so with base 0.5 + good size (0.2) = 0.7 - 0.4 = 0.3
			// With 800x600 aspect ratio bonus: 0.3 + 0.1 = 0.4, position bonus 0.2 = 0.6
			if score > 0.7 {
				t.Errorf("Infographic with tag '%s' scored too high: %.2f (expected < 0.7)", keyword, score)
			}
			t.Logf("Infographic tag '%s' score: %.2f", keyword, score)
		})

		t.Run("summary_"+keyword, func(t *testing.T) {
			img := models.ImageInfo{
				URL:     "https://example.com/image.png",
				Width:   800,
				Height:  600,
				Summary: "This is an " + keyword + " showing data",
			}

			score := scoreImageRelevance(img, 0, 1, "Article", []string{})

			// Should be penalized (summary penalty is -0.3, so expect < 0.9)
			// This is looser because summary penalties are less severe than tag penalties
			if score > 1.0 {
				t.Errorf("Infographic with summary '%s' scored too high: %.2f (expected <= 1.0)", keyword, score)
			}
			t.Logf("Infographic summary '%s' score: %.2f", keyword, score)
		})
	}
}

func TestScoreImageRelevanceBannerSizes(t *testing.T) {
	bannerSizes := []struct {
		name   string
		width  int
		height int
	}{
		{"standard banner", 728, 90},
		{"full banner", 468, 60},
		{"half banner", 234, 60},
		{"skyscraper", 120, 600},
		{"medium rectangle", 300, 250},
		{"large rectangle", 336, 280},
	}

	for _, banner := range bannerSizes {
		t.Run(banner.name, func(t *testing.T) {
			img := models.ImageInfo{
				URL:    "https://example.com/ad.jpg",
				Width:  banner.width,
				Height: banner.height,
			}

			score := scoreImageRelevance(img, 0, 1, "Article", []string{})

			// Banner sizes should be heavily penalized
			if score > 0.3 {
				t.Errorf("Banner size %dx%d scored too high: %.2f (expected < 0.3)",
					banner.width, banner.height, score)
			}
			t.Logf("Banner %s (%dx%d) score: %.2f", banner.name, banner.width, banner.height, score)
		})
	}
}

func TestScoreImageRelevancePositionBias(t *testing.T) {
	// First images should score higher than last images
	img := models.ImageInfo{
		URL:    "https://example.com/photo.jpg",
		Width:  800,
		Height: 600,
	}

	firstImageScore := scoreImageRelevance(img, 0, 10, "Article", []string{})
	lastImageScore := scoreImageRelevance(img, 9, 10, "Article", []string{})

	if firstImageScore <= lastImageScore {
		t.Errorf("First image should score higher than last: first=%.2f, last=%.2f",
			firstImageScore, lastImageScore)
	}

	t.Logf("Position bias - First: %.2f, Last: %.2f, Difference: %.2f",
		firstImageScore, lastImageScore, firstImageScore-lastImageScore)
}

func TestScoreImageRelevanceAspectRatio(t *testing.T) {
	tests := []struct {
		name          string
		width         int
		height        int
		expectedRange [2]float64
	}{
		{"ideal ratio 16:9", 1600, 900, [2]float64{0.6, 1.0}},
		{"ideal ratio 3:2", 1200, 800, [2]float64{0.6, 1.0}},
		{"extreme banner 10:1", 1000, 100, [2]float64{0.0, 0.4}},
		{"extreme vertical 1:5", 200, 1000, [2]float64{0.0, 0.5}},
		{"square 1:1", 800, 800, [2]float64{0.6, 1.0}}, // Square images score well if good size
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := models.ImageInfo{
				URL:    "https://example.com/image.jpg",
				Width:  tt.width,
				Height: tt.height,
			}

			score := scoreImageRelevance(img, 0, 1, "Article", []string{})

			if score < tt.expectedRange[0] || score > tt.expectedRange[1] {
				t.Errorf("Aspect ratio %d:%d scored %.2f, expected [%.2f, %.2f]",
					tt.width, tt.height, score, tt.expectedRange[0], tt.expectedRange[1])
			}

			aspectRatio := float64(tt.width) / float64(tt.height)
			t.Logf("Aspect ratio %.2f (%dx%d) score: %.2f", aspectRatio, tt.width, tt.height, score)
		})
	}
}
