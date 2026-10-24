package management

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/service/seoingest"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func (s *Service) seoProvider(ctx context.Context, name string, in Input) (any, error) {
	if op := domain.CurrentOperation(ctx); op != nil && (op.AgentClientID != 0 || !op.Allowed("system:seo:edit")) {
		return nil, fail(domain.CodeForbidden)
	}
	if in.Key != "gsc" && in.Key != "bing" && in.Key != "audit" {
		return nil, domain.Validation()
	}
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		var row struct {
			Source                string
			Enabled               bool
			CredentialsCiphertext string
		}
		r := tx.WithContext(ctx).Raw("SELECT source,enabled,credentials_ciphertext FROM seo_provider_settings WHERE source=? FOR UPDATE", in.Key).Scan(&row)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return fail(domain.CodeNotFound)
		}
		view := func() (map[string]any, error) {
			return s.seoProviderView(ctx, tx, row.Source, row.Enabled, row.CredentialsCiphertext)
		}
		before, err := view()
		if err != nil {
			return err
		}
		if name == "GetSeoProvider" {
			result = before
			return nil
		}
		if v, ok := in.Body["enabled"]; ok {
			enabled, ok := v.(bool)
			if !ok {
				return domain.Validation()
			}
			row.Enabled = enabled
		}
		if boolean(in.Body, "clearCredentials") {
			row.CredentialsCiphertext = ""
		}
		if raw, ok := in.Body["credentials"]; ok {
			if boolean(in.Body, "clearCredentials") {
				return domain.Validation()
			}
			encoded, err := json.Marshal(raw)
			// JSON escaping can use six bytes per character. The decoded fields
			// below enforce the same character limits as the OpenAPI contract.
			if err != nil || len(encoded) > 256<<10 {
				return domain.Rejected("provider_credentials_invalid")
			}
			var creds seoingest.Credentials
			decoder := json.NewDecoder(strings.NewReader(string(encoded)))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&creds) != nil || !validProviderCredentials(in.Key, creds, s.Config.WebBaseURL) {
				return domain.Rejected("provider_credentials_invalid")
			}
			row.CredentialsCiphertext, err = s.Config.EncryptSEOSecret("provider:"+in.Key, string(encoded))
			if err != nil {
				return err
			}
		}
		if row.Enabled && row.CredentialsCiphertext == "" {
			return domain.Validation()
		}
		if err := tx.Exec("UPDATE seo_provider_settings SET enabled=?,credentials_ciphertext=?,updated_at=clock_timestamp() WHERE source=?", row.Enabled, row.CredentialsCiphertext, in.Key).Error; err != nil {
			return err
		}
		result, err = view()
		if err != nil {
			return err
		}
		return store.Audit(ctx, tx, in.Actor, name, "seo_provider_settings", in.Key, before, result)
	})
	return result, err
}

func (s *Service) seoProviderView(ctx context.Context, tx *gorm.DB, source string, enabled bool, ciphertext string) (map[string]any, error) {
	var credentialType any
	if ciphertext != "" {
		if raw, err := s.Config.DecryptSEOSecret("provider:"+source, ciphertext); err == nil {
			var c seoingest.Credentials
			if json.Unmarshal([]byte(raw), &c) == nil {
				switch {
				case source == "gsc" && c.ServiceAccountJSON != "" && c.ClientID == "" && c.ClientSecret == "" && c.RefreshToken == "":
					credentialType = "service_account"
				case source == "gsc" && c.ServiceAccountJSON == "" && c.ClientID != "" && c.ClientSecret != "" && c.RefreshToken != "":
					credentialType = "oauth"
				case source == "bing" && c.APIKey != "":
					credentialType = "api_key"
				case source == "audit" && c.OriginURL != "":
					credentialType = "origin"
				}
			}
		}
	}
	sources := map[string][]string{"gsc": {"gsc_page", "gsc_query", "inspection"}, "bing": {"bing"}, "audit": {"audit", "edge_probe"}}[source]
	var rows []struct{ Data string }
	if err := tx.WithContext(ctx).Raw(`SELECT jsonb_build_object('source',source,'completeThrough',complete_through,'freshThrough',fresh_through,'updatedAt',updated_at)::text AS data
 FROM seo_data_watermarks WHERE source IN ? ORDER BY array_position(ARRAY['gsc_page','gsc_query','inspection','bing','audit','edge_probe'],source)`, sources).Scan(&rows).Error; err != nil {
		return nil, err
	}
	watermarks := make([]any, 0, len(rows))
	for _, row := range rows {
		var value map[string]any
		if err := json.Unmarshal([]byte(row.Data), &value); err != nil {
			return nil, err
		}
		watermarks = append(watermarks, value)
	}
	return map[string]any{"source": source, "enabled": enabled, "configured": ciphertext != "", "credentialType": credentialType, "watermarks": watermarks}, nil
}

func validProviderCredentials(source string, c seoingest.Credentials, web string) bool {
	for _, value := range []string{c.Property, c.ClientID, c.ClientSecret, c.RefreshToken, c.APIKey, c.OriginURL} {
		if utf8.RuneCountInString(value) > 4096 {
			return false
		}
	}
	property := func() bool {
		if strings.HasPrefix(c.Property, "sc-domain:") {
			host := strings.TrimPrefix(c.Property, "sc-domain:")
			return host != "" && !strings.ContainsAny(host, "/ ?#@: ")
		}
		u, e := url.Parse(c.Property)
		return e == nil && u.Scheme == "https" && fetch.ValidateURL(u) == nil
	}
	switch source {
	case "gsc":
		if !property() || c.APIKey != "" || c.OriginURL != "" {
			return false
		}
		if c.ServiceAccountJSON != "" {
			return c.ClientID == "" && c.ClientSecret == "" && c.RefreshToken == "" && seoingest.ValidServiceAccountJSON(c.ServiceAccountJSON)
		}
		return c.ClientID != "" && c.ClientSecret != "" && c.RefreshToken != ""
	case "bing":
		return property() && !strings.HasPrefix(c.Property, "sc-domain:") && c.APIKey != "" && c.ClientID == "" && c.ClientSecret == "" && c.RefreshToken == "" && c.OriginURL == "" && c.ServiceAccountJSON == ""
	case "audit":
		u, e := url.Parse(c.OriginURL)
		w, we := url.Parse(web)
		return e == nil && we == nil && u.Scheme == "https" && fetch.ValidateURL(u) == nil && u.Host != w.Host && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") && c.APIKey == "" && c.ClientID == "" && c.ClientSecret == "" && c.RefreshToken == "" && c.Property == "" && c.ServiceAccountJSON == ""
	}
	return false
}
