package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
)

// SEO crawler classes that admins may tune. Search engines and user-requested
// fetchers are deliberately absent: they always use the default robots group.
const (
	CrawlerModeAllow = "allow"
	CrawlerModeLimit = "limit"
	CrawlerModeBlock = "block"
)

type SEOCrawlerRule struct {
	Mode         string   `json:"mode"`
	DelaySeconds int      `json:"delaySeconds"`
	Bots         []string `json:"bots"`
}

type SEOSettings struct {
	Identity struct {
		Name           string   `json:"name"`
		AlternateNames []string `json:"alternateNames"`
		LogoURL        string   `json:"logoUrl"`
		SameAs         []string `json:"sameAs"`
	} `json:"identity"`
	Verification struct {
		Google string `json:"google"`
		Bing   string `json:"bing"`
	} `json:"verification"`
	Analytics struct {
		UsageEnabled     bool     `json:"usageEnabled"`
		GA4Enabled       bool     `json:"ga4Enabled"`
		GA4MeasurementID string   `json:"ga4MeasurementId"`
		ConsentRegions   []string `json:"consentRegions"`
	} `json:"analytics"`
	IndexNow struct {
		Enabled bool `json:"enabled"`
	} `json:"indexNow"`
	Cloudflare struct {
		ZoneID       string `json:"zoneId"`
		PurgeEnabled bool   `json:"purgeEnabled"`
	} `json:"cloudflare"`
	EdgeCache struct {
		Enabled bool `json:"enabled"`
	} `json:"edgeCache"`
	Crawlers struct {
		Training SEOCrawlerRule `json:"training"`
		SEOTools SEOCrawlerRule `json:"seoTools"`
	} `json:"crawlers"`
	Pages struct {
		TitleTemplate string `json:"titleTemplate"`
		IndexPolicy   struct {
			Mode  string `json:"mode"`
			Scope string `json:"scope"`
			// Since is set by the server when enforcement first covers new apps (RFC 3339).
			Since string `json:"since"`
		} `json:"indexPolicy"`
		SitemapMinInstalls    int      `json:"sitemapMinInstalls"`
		SitemapNonEnglishTopN int      `json:"sitemapNonEnglishTopN"`
		VersionsSitemap       bool     `json:"versionsSitemap"`
		FontsSitemap          bool     `json:"fontsSitemap"`
		TranslationMode       string   `json:"translationMode"`
		TranslationLocales    []string `json:"translationLocales"`
		VersionsMinNotesChars int      `json:"versionsMinNotesChars"`
		OpenLocales           []string `json:"openLocales"`
		IntentPagesEnabled    bool     `json:"intentPagesEnabled"`
		DetailsRedirect       bool     `json:"detailsRedirect"`
	} `json:"pages"`
}

// ConsentRegionsDefault lists EU/EEA members plus the UK and Switzerland, where
// analytics cookies wait for consent.
var ConsentRegionsDefault = []string{"AT", "BE", "BG", "HR", "CY", "CZ", "DK", "EE", "FI", "FR", "DE", "GR", "HU", "IE", "IT", "LV", "LT", "LU", "MT", "NL", "PL", "PT", "RO", "SK", "SI", "ES", "SE", "IS", "LI", "NO", "GB", "CH"}

// DefaultSEOSettings reproduces the behaviour that existed before the settings page, with the
// training crawlers that fetch at scale limited (10 s crawl delay, no payloads or dependency tabs;
// the same list as packages/shared), SEO-tool crawlers blocked, new title templates, index policy
// observed but not enforced. Migration 00112 added these bots to stored lists.
func DefaultSEOSettings() SEOSettings {
	var s SEOSettings
	s.Identity.Name = "OpenNavo"
	s.Identity.AlternateNames = []string{}
	s.Identity.SameAs = []string{"https://github.com/opennavo/OpenNavo", "https://x.com/opennavo"}
	s.Analytics.UsageEnabled = true
	s.Analytics.ConsentRegions = append([]string{}, ConsentRegionsDefault...)
	s.Crawlers.Training = SEOCrawlerRule{Mode: CrawlerModeLimit, DelaySeconds: 10, Bots: []string{"ClaudeBot", "GPTBot", "Amazonbot", "Bytespider", "CCBot", "Meta-ExternalAgent", "anthropic-ai", "MistralAI-Training"}}
	s.Crawlers.SEOTools = SEOCrawlerRule{Mode: CrawlerModeBlock, DelaySeconds: 10, Bots: []string{"MJ12bot", "SerpstatBot"}}
	for _, locale := range i18n.Locales {
		s.Pages.OpenLocales = append(s.Pages.OpenLocales, string(locale))
	}
	s.Pages.TranslationMode = "shadow"
	s.Pages.TranslationLocales = []string{}
	s.Pages.VersionsMinNotesChars = 300
	s.Pages.SitemapMinInstalls = seo.DefaultSitemapMinInstalls
	s.Pages.SitemapNonEnglishTopN = seo.DefaultSitemapNonEnglishTopN
	s.Pages.TitleTemplate = "new"
	s.Pages.IndexPolicy.Mode = "shadow"
	s.Pages.IndexPolicy.Scope = "new"
	return s
}

type SEOSettingsRow struct {
	Settings                  SEOSettings
	IndexNowKeyCiphertext     string `json:"-"`
	CloudflareTokenCiphertext string `json:"-"`
	UpdatedAt                 time.Time
	Revision                  int64
}

// DecodeSEOSettings overlays stored JSON on the defaults, so fields added later
// keep their default until an admin saves them.
func DecodeSEOSettings(raw []byte) (SEOSettings, error) {
	settings := DefaultSEOSettings()
	if len(raw) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return DefaultSEOSettings(), err
	}
	return settings, nil
}

func (s *Store) SEOSettings(ctx context.Context) (SEOSettingsRow, error) {
	var row struct {
		Settings                  string
		IndexnowKeyCiphertext     string
		CloudflareTokenCiphertext string
		UpdatedAt                 time.Time
		Revision                  int64
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT settings::text AS settings,indexnow_key_ciphertext,cloudflare_token_ciphertext,updated_at,revision FROM seo_settings WHERE id=true").Scan(&row).Error; err != nil {
		return SEOSettingsRow{}, err
	}
	settings, err := DecodeSEOSettings([]byte(row.Settings))
	if err != nil {
		return SEOSettingsRow{}, err
	}
	return SEOSettingsRow{Settings: settings, IndexNowKeyCiphertext: row.IndexnowKeyCiphertext, CloudflareTokenCiphertext: row.CloudflareTokenCiphertext, UpdatedAt: row.UpdatedAt, Revision: row.Revision}, nil
}

// SEOSecret decrypts the IndexNow key ("indexnow") or Cloudflare token ("cloudflare").
// It reads per call and never caches credentials. An empty result means not configured.
func (s *Store) SEOSecret(ctx context.Context, cfg config.Config, field string) (string, error) {
	row, err := s.SEOSettings(ctx)
	if err != nil {
		return "", err
	}
	var ciphertext string
	switch field {
	case "indexnow":
		ciphertext = row.IndexNowKeyCiphertext
	case "cloudflare":
		ciphertext = row.CloudflareTokenCiphertext
	default:
		return "", errors.New("unknown SEO credential")
	}
	if ciphertext == "" {
		return "", nil
	}
	return cfg.DecryptSEOSecret(field, ciphertext)
}
