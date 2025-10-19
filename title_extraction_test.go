package scraper

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name     string
		htmlDoc  string
		expected string
	}{
		{
			name: "og:title takes precedence over title tag",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<meta property="og:title" content="Actual Article Title" />
	<title>British Broadcasting Corporation</title>
</head>
<body></body>
</html>`,
			expected: "Actual Article Title",
		},
		{
			name: "twitter:title takes precedence over title tag",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<meta name="twitter:title" content="Twitter Article Title" />
	<title>Generic Site Name</title>
</head>
<body></body>
</html>`,
			expected: "Twitter Article Title",
		},
		{
			name: "og:title takes precedence over twitter:title",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<meta property="og:title" content="OG Title" />
	<meta name="twitter:title" content="Twitter Title" />
	<title>Site Name</title>
</head>
<body></body>
</html>`,
			expected: "OG Title",
		},
		{
			name: "h1 fallback when no meta tags",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<title>Site Name</title>
</head>
<body>
	<h1>Article Heading</h1>
	<p>Content here</p>
</body>
</html>`,
			expected: "Article Heading",
		},
		{
			name: "title tag as final fallback",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<title>Page Title</title>
</head>
<body>
	<p>Content without h1</p>
</body>
</html>`,
			expected: "Page Title",
		},
		{
			name: "empty og:title falls back to twitter:title",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<meta property="og:title" content="" />
	<meta name="twitter:title" content="Twitter Fallback" />
	<title>Site Title</title>
</head>
<body></body>
</html>`,
			expected: "Twitter Fallback",
		},
		{
			name: "h1 with nested elements",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<title>Site Name</title>
</head>
<body>
	<h1>Article <span>Title</span> Here</h1>
</body>
</html>`,
			expected: "Article Title Here",
		},
		{
			name: "BBC-like structure",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<meta property="og:title" content="UK weather: Snow and ice warnings issued as cold snap continues - BBC News" />
	<meta name="twitter:title" content="UK weather: Snow and ice warnings issued as cold snap continues" />
	<title>British Broadcasting Corporation</title>
</head>
<body>
	<h1>UK weather: Snow and ice warnings issued</h1>
</body>
</html>`,
			expected: "UK weather: Snow and ice warnings issued as cold snap continues - BBC News",
		},
		{
			name: "whitespace trimming",
			htmlDoc: `<!DOCTYPE html>
<html>
<head>
	<meta property="og:title" content="  Trimmed Title  " />
	<title>Site</title>
</head>
<body></body>
</html>`,
			expected: "Trimmed Title",
		},
		{
			name: "empty document",
			htmlDoc: `<!DOCTYPE html><html><head></head><body></body></html>`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tt.htmlDoc))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			result := extractTitle(doc)
			if result != tt.expected {
				t.Errorf("extractTitle() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
