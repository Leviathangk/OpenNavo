package management

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

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
		view := func() any {
			return map[string]any{"source": row.Source, "enabled": row.Enabled, "configured": row.CredentialsCiphertext != ""}
		}
		before := view()
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
			if err != nil || len(encoded) > 16384 {
				return domain.Validation()
			}
			var creds seoingest.Credentials
			decoder := json.NewDecoder(strings.NewReader(string(encoded)))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&creds) != nil || !validProviderCredentials(in.Key, creds, s.Config.WebBaseURL) {
				return domain.Validation()
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
		result = view()
		return store.Audit(ctx, tx, in.Actor, name, "seo_provider_settings", in.Key, before, result)
	})
	return result, err
}
func validProviderCredentials(source string, c seoingest.Credentials, web string) bool {
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
		return property() && c.ClientID != "" && c.ClientSecret != "" && c.RefreshToken != "" && c.APIKey == "" && c.OriginURL == ""
	case "bing":
		return property() && !strings.HasPrefix(c.Property, "sc-domain:") && c.APIKey != "" && c.ClientID == "" && c.ClientSecret == "" && c.RefreshToken == "" && c.OriginURL == ""
	case "audit":
		u, e := url.Parse(c.OriginURL)
		w, we := url.Parse(web)
		return e == nil && we == nil && u.Scheme == "https" && fetch.ValidateURL(u) == nil && u.Host != w.Host && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") && c.APIKey == "" && c.ClientID == "" && c.ClientSecret == "" && c.RefreshToken == "" && c.Property == ""
	}
	return false
}
