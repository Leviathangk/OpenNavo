package management

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockedCrawlers are search engines and user-requested fetchers. They always use
// the default robots group, so the settings page cannot limit or block them.
var lockedCrawlers = []string{
	"googlebot", "googlebot-image", "googlebot-news", "googlebot-video", "storebot-google", "google-inspectiontool", "adsbot-google",
	"bingbot", "bingpreview", "msnbot", "oai-searchbot", "chatgpt-user", "claude-searchbot", "claude-user",
	"perplexitybot", "perplexity-user", "duckduckbot", "duckassistbot", "applebot", "yandexbot", "baiduspider",
	"mistralai-user", "yeti", "*",
}

var (
	botNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
	verificationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	measurementPattern  = regexp.MustCompile(`^G-[A-Z0-9]{4,16}$`)
	regionPattern       = regexp.MustCompile(`^[A-Z]{2}$`)
	zonePattern         = regexp.MustCompile(`^[a-f0-9]{32}$`)
	indexNowKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)
)

func seoView(row store.SEOSettingsRow) map[string]any {
	return map[string]any{
		"settings":                  row.Settings,
		"indexNowKeyConfigured":     row.IndexNowKeyCiphertext != "",
		"cloudflareTokenConfigured": row.CloudflareTokenCiphertext != "",
		"updatedAt":                 row.UpdatedAt.UTC().Format(time.RFC3339),
		"revision":                  row.Revision,
	}
}

// seoResponse adds read-only context for the settings page; audit entries use seoView alone.
func (s *Service) seoResponse(ctx context.Context, st *store.Store, row store.SEOSettingsRow) (map[string]any, error) {
	view := seoView(row)
	view["siteUrl"] = strings.TrimRight(s.Config.WebBaseURL, "/")
	feed, err := st.URLNotifyState(ctx)
	if err != nil {
		return nil, err
	}
	var lastRun any
	if feed.LastRunAt != nil {
		lastRun = feed.LastRunAt.UTC().Format(time.RFC3339)
	}
	view["changeFeed"] = map[string]any{
		"lastRunAt": lastRun, "lastUrls": feed.LastURLs, "lastIndexnowStatus": feed.LastIndexnowStatus,
		"lastPurgeStatus": feed.LastPurgeStatus, "lastError": feed.LastError,
	}
	return view, nil
}

func validHTTPS(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func validLabel(value string, limit int) bool {
	if value == "" || len([]rune(value)) > limit {
		return false
	}
	for _, char := range value {
		if char < ' ' || char == 0x7f {
			return false
		}
	}
	return true
}

func validCrawlerRule(rule *store.SEOCrawlerRule) bool {
	if !slices.Contains([]string{store.CrawlerModeAllow, store.CrawlerModeLimit, store.CrawlerModeBlock}, rule.Mode) {
		return false
	}
	if rule.DelaySeconds < 1 || rule.DelaySeconds > 60 || len(rule.Bots) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, bot := range rule.Bots {
		key := strings.ToLower(bot)
		if !botNamePattern.MatchString(bot) || slices.Contains(lockedCrawlers, key) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

// validateSEOSettings checks the non-secret settings document. Credential presence is
// checked separately because it depends on stored ciphertexts.
func validateSEOSettings(s *store.SEOSettings) bool {
	if !validLabel(s.Identity.Name, 60) || len(s.Identity.AlternateNames) > 5 || len(s.Identity.SameAs) > 10 {
		return false
	}
	for _, name := range s.Identity.AlternateNames {
		if !validLabel(name, 80) {
			return false
		}
	}
	if s.Identity.LogoURL != "" && !validHTTPS(s.Identity.LogoURL, 500) {
		return false
	}
	for _, link := range s.Identity.SameAs {
		if !validHTTPS(link, 300) {
			return false
		}
	}
	for _, code := range []string{s.Verification.Google, s.Verification.Bing} {
		if code != "" && !verificationPattern.MatchString(code) {
			return false
		}
	}
	if s.Analytics.GA4MeasurementID != "" && !measurementPattern.MatchString(s.Analytics.GA4MeasurementID) {
		return false
	}
	if s.Analytics.GA4Enabled && s.Analytics.GA4MeasurementID == "" {
		return false
	}
	if len(s.Analytics.ConsentRegions) > 60 {
		return false
	}
	seen := map[string]bool{}
	for _, region := range s.Analytics.ConsentRegions {
		if !regionPattern.MatchString(region) || seen[region] {
			return false
		}
		seen[region] = true
	}
	if s.Cloudflare.ZoneID != "" && !zonePattern.MatchString(s.Cloudflare.ZoneID) {
		return false
	}
	if !validCrawlerRule(&s.Crawlers.Training) || !validCrawlerRule(&s.Crawlers.SEOTools) {
		return false
	}
	training, tools := map[string]bool{}, map[string]bool{}
	for _, bot := range s.Crawlers.Training.Bots {
		training[strings.ToLower(bot)] = true
	}
	for _, bot := range s.Crawlers.SEOTools.Bots {
		tools[strings.ToLower(bot)] = true
		if training[strings.ToLower(bot)] {
			return false
		}
	}
	if !slices.Contains([]string{"new", "legacy"}, s.Pages.TitleTemplate) ||
		!slices.Contains([]string{"off", "shadow", "enforce"}, s.Pages.IndexPolicy.Mode) ||
		!slices.Contains([]string{"new", "all"}, s.Pages.IndexPolicy.Scope) {
		return false
	}
	return true
}

func printableSecret(value string, limit int) bool {
	if len(value) > limit {
		return false
	}
	for _, char := range value {
		if char < '!' || char > '~' {
			return false
		}
	}
	return true
}

type seoUpdate struct {
	Settings             *store.SEOSettings `json:"settings"`
	IndexNowKey          *string            `json:"indexNowKey"`
	ClearIndexNowKey     bool               `json:"clearIndexNowKey"`
	CloudflareToken      *string            `json:"cloudflareToken"`
	ClearCloudflareToken bool               `json:"clearCloudflareToken"`
}

func decodeSEOUpdate(body map[string]any) (seoUpdate, error) {
	var in seoUpdate
	data, err := json.Marshal(body)
	if err != nil {
		return in, domain.Validation()
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil || in.Settings == nil {
		return in, domain.Validation()
	}
	if !validateSEOSettings(in.Settings) {
		return in, domain.Validation()
	}
	if in.IndexNowKey != nil && *in.IndexNowKey != "" && (in.ClearIndexNowKey || !indexNowKeyPattern.MatchString(*in.IndexNowKey)) {
		return in, domain.Validation()
	}
	if in.CloudflareToken != nil && *in.CloudflareToken != "" && (in.ClearCloudflareToken || !printableSecret(*in.CloudflareToken, 200)) {
		return in, domain.Validation()
	}
	return in, nil
}

func (s *Service) seoSettings(ctx context.Context, op string, in Input) (any, error) {
	if op == "GetSeoSettings" {
		row, err := s.Store.SEOSettings(ctx)
		if err != nil {
			return nil, err
		}
		return s.seoResponse(ctx, s.Store, row)
	}
	update, err := decodeSEOUpdate(in.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		var before struct {
			Settings                  string
			IndexnowKeyCiphertext     string
			CloudflareTokenCiphertext string
			UpdatedAt                 time.Time
			Revision                  int64
		}
		if err := tx.WithContext(ctx).Table("seo_settings").Select("settings::text AS settings,indexnow_key_ciphertext,cloudflare_token_ciphertext,updated_at,revision").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=true").Take(&before).Error; err != nil {
			return err
		}
		previous, err := store.DecodeSEOSettings([]byte(before.Settings))
		if err != nil {
			return err
		}
		beforeRow := store.SEOSettingsRow{Settings: previous, IndexNowKeyCiphertext: before.IndexnowKeyCiphertext, CloudflareTokenCiphertext: before.CloudflareTokenCiphertext, UpdatedAt: before.UpdatedAt, Revision: before.Revision}
		after := beforeRow
		after.Settings = *update.Settings
		// The server owns since: it marks when enforcement first covered new apps.
		policy := &after.Settings.Pages.IndexPolicy
		policy.Since = previous.Pages.IndexPolicy.Since
		if policy.Mode == "enforce" && policy.Scope == "new" && policy.Since == "" {
			policy.Since = s.now().UTC().Format(time.RFC3339)
		}
		if update.ClearIndexNowKey {
			after.IndexNowKeyCiphertext = ""
		}
		if update.IndexNowKey != nil && *update.IndexNowKey != "" {
			if after.IndexNowKeyCiphertext, err = s.Config.EncryptSEOSecret("indexnow", *update.IndexNowKey); err != nil {
				return err
			}
		}
		if update.ClearCloudflareToken {
			after.CloudflareTokenCiphertext = ""
		}
		if update.CloudflareToken != nil && *update.CloudflareToken != "" {
			if after.CloudflareTokenCiphertext, err = s.Config.EncryptSEOSecret("cloudflare", *update.CloudflareToken); err != nil {
				return err
			}
		}
		// Switches that act on credentials need the credential to exist.
		if after.Settings.IndexNow.Enabled && after.IndexNowKeyCiphertext == "" {
			return domain.Validation()
		}
		if after.Settings.Cloudflare.PurgeEnabled && (after.CloudflareTokenCiphertext == "" || after.Settings.Cloudflare.ZoneID == "") {
			return domain.Validation()
		}
		document, err := json.Marshal(after.Settings)
		if err != nil {
			return err
		}
		after.UpdatedAt, after.Revision = s.now(), beforeRow.Revision+1
		if err := tx.WithContext(ctx).Table("seo_settings").Where("id=true").Updates(map[string]any{
			"settings":                    gorm.Expr("?::jsonb", string(document)),
			"indexnow_key_ciphertext":     after.IndexNowKeyCiphertext,
			"cloudflare_token_ciphertext": after.CloudflareTokenCiphertext,
			"updated_at":                  after.UpdatedAt,
			"revision":                    after.Revision,
		}).Error; err != nil {
			return err
		}
		if result, err = s.seoResponse(ctx, &store.Store{DB: tx, Redis: s.Store.Redis}, after); err != nil {
			return err
		}
		// Cached app details carry the index decision; refresh them when the policy changes.
		if previous.Pages.IndexPolicy != after.Settings.Pages.IndexPolicy {
			// The decision is also embedded in SSR HTML; feed the existing cache-purge worker.
			if err := tx.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason)
                SELECT id,kind,token,'upsert','content' FROM packages WHERE kind='cask' AND removed_at IS NULL`).Error; err != nil {
				return err
			}
			if err := store.QueueCacheInvalidation(ctx, tx, "seo-policy", cache.PackageDetailPrefix+"*"); err != nil {
				return err
			}
		}
		// Audit records configured flags, never credentials.
		return store.Audit(ctx, tx, in.Actor, "UpdateSeoSettings", "seo_settings", "true", seoView(beforeRow), seoView(after))
	})
	return result, store.MapAdminError(err)
}
