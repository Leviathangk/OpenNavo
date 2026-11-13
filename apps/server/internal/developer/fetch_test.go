package developer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseWebsiteHTMLPrefersOpenGraphAndJSONLD(t *testing.T) {
	source := `<!doctype html><html><head>
		<title>Acme — Tools for Mac</title>
		<meta property="og:title" content="Acme">
		<meta property="og:description" content="A native Mac app.">
		<meta property="og:site_name" content="Acme Labs">
		<meta property="og:image" content="https://acme.example/icon.png">
		<script type="application/ld+json">{"@type":"SoftwareApplication","name":"Acme","description":"Native Mac app.","softwareVersion":"2.1","applicationCategory":"DeveloperApplication","author":{"name":"Acme Labs"}}</script>
	</head></html>`
	meta := parseWebsiteHTML(source, "https://acme.example/")
	if meta.Name != "Acme" || meta.Developer != "Acme Labs" || meta.Version != "2.1" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if meta.IconURL != "https://acme.example/icon.png" {
		t.Fatalf("icon: %q", meta.IconURL)
	}
}

func TestParseWebsiteHTMLFallsBackToTitle(t *testing.T) {
	meta := parseWebsiteHTML(`<html><head><title>Plain site</title><meta name="description" content="Hello"></head></html>`, "https://plain.example/")
	if meta.Name != "Plain site" || meta.Summary != "Hello" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestValidEmail(t *testing.T) {
	for _, value := range []string{"dev@example.com", "a.b+tag@sub.example.co"} {
		if !ValidEmail(value) {
			t.Fatalf("expected valid: %s", value)
		}
	}
	for _, value := range []string{"", "dev", "dev@", "@example.com", "dev@example", strings.Repeat("a", 250) + "@example.com"} {
		if ValidEmail(value) {
			t.Fatalf("expected invalid: %s", value)
		}
	}
}

func TestValidatePublicURLRejectsPrivateHosts(t *testing.T) {
	for _, value := range []string{
		"http://127.0.0.1/",
		"http://localhost/",
		"http://[::1]/",
		"file:///etc/passwd",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[fd00::1]/",
		"http://printer.internal/",
		"ftp://example.com/app",
	} {
		if err := validatePublicURL(value); err == nil {
			t.Fatalf("expected rejection: %s", value)
		}
	}
}

func TestTruncateRunesKeepsValidUTF8(t *testing.T) {
	value := strings.Repeat("终", 300)
	short := truncateRunes(value, 240)
	if !utf8.ValidString(short) {
		t.Fatal("truncated text is not valid UTF-8")
	}
	if got := len([]rune(strings.TrimSuffix(short, "…"))); got != 240 {
		t.Fatalf("kept %d runes, want 240", got)
	}
	if !strings.HasSuffix(short, "…") {
		t.Fatal("expected an ellipsis")
	}
}
