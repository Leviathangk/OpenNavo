// Package articles holds the rules for articles (WP14) that do not need the database: input
// validation, the Markdown safety check shared by save, submit and publish, reading time, and the
// content hash that decides when dateModified moves.
package articles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxSources  = 20
	MaxPackages = 50
	MaxImages   = 30
	MaxAppCards = 20
	MaxBody     = 100000

	// FeedPath is the web path of each language's RSS feed, refreshed with every public change.
	FeedPath = "/articles/feed.xml"
)

var (
	Types  = []string{"recommendation", "review", "tutorial", "roundup", "news"}
	Status = []string{"draft", "review", "published", "archived"}

	slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// ::app[token] on a line of its own embeds the app's current catalog card.
	appDirective = regexp.MustCompile(`(?m)^::app\[([a-z0-9][a-z0-9@+._-]*)\]\s*$`)
	fence        = regexp.MustCompile("(?ms)^(```|~~~).*?^(```|~~~)\\s*$")
	inlineCode   = regexp.MustCompile("`[^`\n]+`")
	rawHTML      = regexp.MustCompile(`<[A-Za-z!/?][^>]*>`)
	image        = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	link         = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	autolink     = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9+.-]*:[^>\s]*)>`)
	cjk          = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]`)
)

// ErrInvalid marks input the API rejects as a validation error.
var ErrInvalid = errors.New("invalid article")

// Problem codes reported for content that cannot be submitted or published.
const (
	ProblemTitle      = "title"
	ProblemSummary    = "summary"
	ProblemBody       = "body"
	ProblemRawHTML    = "raw_html"
	ProblemLink       = "unsafe_link"
	ProblemImageAlt   = "image_alt"
	ProblemImageHost  = "image_host"
	ProblemImages     = "too_many_images"
	ProblemAppCards   = "too_many_app_cards"
	ProblemUnknownApp = "unknown_app"
	ProblemSources    = "sources"
	ProblemTestNote   = "test_note"
	ProblemCoverAlt   = "cover_alt"
)

// Source is one cited reference.
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ValidSlug accepts lowercase words joined by hyphens, as used in URLs.
func ValidSlug(slug string, limit int) bool {
	return len(slug) <= limit && slugPattern.MatchString(slug)
}

// ValidType reports a known article type.
func ValidType(kind string) bool { return slices.Contains(Types, kind) }

// Text trims a single-line field; empty becomes nil.
func Text(value *string, limit int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if text == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(text) > limit || strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) {
		return nil, ErrInvalid
	}
	return &text, nil
}

// Body normalizes line endings and enforces the size limit; empty becomes nil.
func Body(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(strings.ReplaceAll(*value, "\r\n", "\n"))
	if text == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(text) > MaxBody || strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return nil, ErrInvalid
	}
	return &text, nil
}

// HTTPS accepts absolute https URLs without credentials.
func HTTPS(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// Sources validates cited references: https URLs, titles up to 200 characters, no duplicates.
func Sources(in []Source) ([]Source, error) {
	if len(in) > MaxSources {
		return nil, ErrInvalid
	}
	out := make([]Source, 0, len(in))
	for _, source := range in {
		title, err := Text(&source.Title, 200)
		if err != nil || title == nil || !HTTPS(strings.TrimSpace(source.URL)) {
			return nil, ErrInvalid
		}
		item := Source{Title: *title, URL: strings.TrimSpace(source.URL)}
		if slices.ContainsFunc(out, func(seen Source) bool { return seen.URL == item.URL }) {
			return nil, ErrInvalid
		}
		out = append(out, item)
	}
	return out, nil
}

// Packages validates the related app list: unique positive IDs, at most MaxPackages.
func Packages(ids []int64) ([]int64, error) {
	if len(ids) > MaxPackages {
		return nil, ErrInvalid
	}
	for index, id := range ids {
		if id < 1 || slices.Contains(ids[:index], id) {
			return nil, ErrInvalid
		}
	}
	if ids == nil {
		return []int64{}, nil
	}
	return ids, nil
}

// Scan is what the Markdown body references.
type Scan struct {
	Images   []Image
	AppCards []string
	Problems []string
}

// Image is one body image.
type Image struct {
	URL, Alt string
}

func withoutCode(body string) string {
	return inlineCode.ReplaceAllString(fence.ReplaceAllString(body, ""), "")
}

func safeLink(raw string) bool {
	if strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
		return u.Host != "" && u.User == nil
	case "mailto":
		return true
	}
	return false
}

// ScanBody lists images and app cards and reports raw HTML, unsafe links, images without alt text
// and too many embeds. Code is excluded: commands and snippets are shown, never interpreted.
func ScanBody(body string) Scan {
	scan := Scan{Images: []Image{}, AppCards: []string{}, Problems: []string{}}
	add := func(problem string) {
		if !slices.Contains(scan.Problems, problem) {
			scan.Problems = append(scan.Problems, problem)
		}
	}
	text := withoutCode(body)
	if rawHTML.MatchString(autolink.ReplaceAllString(text, "")) {
		add(ProblemRawHTML)
	}
	for _, match := range image.FindAllStringSubmatch(text, -1) {
		alt := strings.TrimSpace(match[1])
		scan.Images = append(scan.Images, Image{URL: match[2], Alt: alt})
		if alt == "" {
			add(ProblemImageAlt)
		}
		if !HTTPS(match[2]) {
			add(ProblemImageHost)
		}
	}
	for _, match := range link.FindAllStringSubmatch(image.ReplaceAllString(text, ""), -1) {
		if !safeLink(match[1]) {
			add(ProblemLink)
		}
	}
	for _, match := range autolink.FindAllStringSubmatch(text, -1) {
		if !safeLink(match[1]) {
			add(ProblemLink)
		}
	}
	for _, match := range appDirective.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(scan.AppCards, match[1]) {
			scan.AppCards = append(scan.AppCards, match[1])
		}
	}
	if len(scan.Images) > MaxImages {
		add(ProblemImages)
	}
	if len(appDirective.FindAllStringIndex(text, -1)) > MaxAppCards {
		add(ProblemAppCards)
	}
	return scan
}

// ReadingMinutes estimates reading time: about 400 CJK characters or 220 words a minute.
func ReadingMinutes(body string) int {
	text := withoutCode(body)
	chars := len(cjk.FindAllStringIndex(text, -1))
	words := len(strings.FieldsFunc(cjk.ReplaceAllString(text, " "), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) && r != '\'' && r != '-'
	}))
	minutes := (chars*60/400 + words*60/220 + 59) / 60
	return max(1, minutes)
}

// Content is what a revision shows readers; its hash decides when dateModified moves.
type Content struct {
	Type         string   `json:"type"`
	AuthorID     *int64   `json:"authorId"`
	CoverAssetID *int64   `json:"coverAssetId"`
	CoverAlt     *string  `json:"coverAlt"`
	Title        string   `json:"title"`
	Summary      *string  `json:"summary"`
	Body         *string  `json:"body"`
	TestNote     *string  `json:"testNote"`
	Packages     []int64  `json:"packages"`
	Sources      []Source `json:"sources"`
}

// Hash of the substantive content. SEO fields are left out: rewording a search snippet is not an
// update readers should see as a new date.
func (c Content) Hash() string {
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Readiness lists what must be fixed before review or publication: a title, a summary for the
// description, a body without problems, cited sources for compilations and a test record for
// reviews (no test means writing it as a compilation with sources instead).
func Readiness(c Content, scan Scan) []string {
	out := []string{}
	if strings.TrimSpace(c.Title) == "" {
		out = append(out, ProblemTitle)
	}
	if c.Summary == nil {
		out = append(out, ProblemSummary)
	}
	if c.Body == nil {
		out = append(out, ProblemBody)
	}
	out = append(out, scan.Problems...)
	if (c.Type == "recommendation" || c.Type == "roundup" || c.Type == "news") && len(c.Sources) == 0 {
		out = append(out, ProblemSources)
	}
	if c.Type == "review" && c.TestNote == nil {
		out = append(out, ProblemTestNote)
	}
	if c.CoverAssetID != nil && c.CoverAlt == nil {
		out = append(out, ProblemCoverAlt)
	}
	return out
}
