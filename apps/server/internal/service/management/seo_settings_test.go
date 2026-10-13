package management

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func seoBody(t *testing.T, change func(*store.SEOSettings), extra map[string]any) map[string]any {
	t.Helper()
	settings := store.DefaultSEOSettings()
	if change != nil {
		change(&settings)
	}
	data, err := json.Marshal(settings)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(data, &document))
	body := map[string]any{"settings": document}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func TestSEOSettingsValidation(t *testing.T) {
	valid := []map[string]any{
		seoBody(t, nil, nil),
		seoBody(t, func(s *store.SEOSettings) {
			s.Identity.AlternateNames = []string{"OpenNavo App Store", "开源导航"}
			s.Identity.LogoURL = "https://opennavo.example/brand/logo-512.png"
			s.Verification.Google = "fixture-google_code"
			s.Analytics.GA4Enabled, s.Analytics.GA4MeasurementID = true, "G-FIXTURE1"
			s.Analytics.ConsentRegions = []string{}
			s.Cloudflare.ZoneID = strings.Repeat("a1", 16)
			s.Crawlers.Training = store.SEOCrawlerRule{Mode: store.CrawlerModeBlock, DelaySeconds: 60, Bots: []string{"GPTBot", "CCBot", "Google-Extended"}}
			s.Crawlers.SEOTools = store.SEOCrawlerRule{Mode: store.CrawlerModeAllow, DelaySeconds: 1, Bots: []string{}}
			s.Pages.TitleTemplate, s.Pages.IndexPolicy.Mode, s.Pages.IndexPolicy.Scope = "legacy", "enforce", "all"
		}, nil),
		seoBody(t, nil, map[string]any{"indexNowKey": "fixture-indexnow-key"}),
		seoBody(t, nil, map[string]any{"indexNowKey": "", "clearIndexNowKey": true, "cloudflareToken": "", "clearCloudflareToken": true}),
		seoBody(t, nil, map[string]any{"cloudflareToken": "fixture_cloudflare-token.1"}),
	}
	for _, body := range valid {
		_, err := decodeSEOUpdate(body)
		require.NoError(t, err, body)
	}
	invalid := []map[string]any{
		{}, {"settings": nil}, {"settings": "text"},
		seoBody(t, func(s *store.SEOSettings) { s.Identity.Name = "" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.Name = strings.Repeat("名", 61) }, nil),
		seoBody(t, nil, map[string]any{"unknown": true}),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.AlternateNames = []string{""} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.AlternateNames = []string{"line\nbreak"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.AlternateNames = []string{"a", "b", "c", "d", "e", "f"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.LogoURL = "http://opennavo.example/logo.png" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.LogoURL = "https://user@opennavo.example/logo.png" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Identity.SameAs = []string{"javascript:alert(1)"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Verification.Bing = "<meta>" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Analytics.GA4Enabled = true }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Analytics.GA4MeasurementID = "UA-1234-1" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Analytics.ConsentRegions = []string{"DE", "DE"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Analytics.ConsentRegions = []string{"de"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Cloudflare.ZoneID = "not-a-zone" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.Training.Mode = "throttle" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.Training.DelaySeconds = 0 }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.SEOTools.DelaySeconds = 61 }, nil),
		// Search engines and user-requested fetchers stay locked to the default group.
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.Training.Bots = []string{"Googlebot"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.SEOTools.Bots = []string{"OAI-SearchBot"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.Training.Bots = []string{"*"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.Training.Bots = []string{"GPTBot", "gptbot"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.Training.Bots = []string{"Bad Bot"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Crawlers.SEOTools.Bots = []string{"claudebot"} }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Pages.TitleTemplate = "custom" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Pages.IndexPolicy.Mode = "on" }, nil),
		seoBody(t, func(s *store.SEOSettings) { s.Pages.IndexPolicy.Scope = "some" }, nil),
		seoBody(t, nil, map[string]any{"indexNowKey": "short"}),
		seoBody(t, nil, map[string]any{"indexNowKey": "has space key"}),
		seoBody(t, nil, map[string]any{"indexNowKey": "fixture-indexnow-key", "clearIndexNowKey": true}),
		seoBody(t, nil, map[string]any{"cloudflareToken": "line\nbreak"}),
		seoBody(t, nil, map[string]any{"cloudflareToken": strings.Repeat("x", 201)}),
		seoBody(t, nil, map[string]any{"cloudflareToken": "fixture-token", "clearCloudflareToken": true}),
		seoBody(t, nil, map[string]any{"clearIndexNowKey": "true"}),
	}
	for _, body := range invalid {
		_, err := decodeSEOUpdate(body)
		require.Error(t, err, body)
	}
}

func TestSEOSettingsDecodeKeepsDefaultsForMissingFields(t *testing.T) {
	settings, err := store.DecodeSEOSettings([]byte(`{"analytics":{"ga4Enabled":true,"ga4MeasurementId":"G-FIXTURE1"}}`))
	require.NoError(t, err)
	require.True(t, settings.Analytics.GA4Enabled)
	require.Equal(t, store.ConsentRegionsDefault, settings.Analytics.ConsentRegions)
	require.Equal(t, store.CrawlerModeLimit, settings.Crawlers.Training.Mode)
	require.Equal(t, "shadow", settings.Pages.IndexPolicy.Mode)
	require.True(t, validateSEOSettings(&settings))
	defaults := store.DefaultSEOSettings()
	require.True(t, validateSEOSettings(&defaults))
}
