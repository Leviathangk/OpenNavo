package seoingest

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/fetch"
	"github.com/opennavo/opennavo/server/internal/store"
)

// These documents exist only inside authenticated encryption at rest. None is an API response.
type Credentials struct {
	Property           string `json:"property"`
	ClientID           string `json:"clientId"`
	ClientSecret       string `json:"clientSecret"`
	RefreshToken       string `json:"refreshToken"`
	APIKey             string `json:"apiKey"`
	OriginURL          string `json:"originUrl"`
	ServiceAccountJSON string `json:"serviceAccountJson"`
}

const googleTokenURL = "https://oauth2.googleapis.com/token" // #nosec G101 -- Google's public token endpoint, not a credential.
const webmasterScope = "https://www.googleapis.com/auth/webmasters.readonly"

type serviceAccount struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
}

// Parse only the fields used for authentication; downloaded Google key files
// include other metadata. Never return parser errors containing submitted data.
func parseServiceAccount(raw string) (serviceAccount, *rsa.PrivateKey, error) {
	var account serviceAccount
	if utf8.RuneCountInString(raw) > 16384 || json.Unmarshal([]byte(raw), &account) != nil || account.Type != "service_account" {
		return serviceAccount{}, nil, ErrUpstream
	}
	address, err := mail.ParseAddress(account.ClientEmail)
	if err != nil || address.Address != account.ClientEmail {
		return serviceAccount{}, nil, ErrUpstream
	}
	if account.TokenURI == "" {
		account.TokenURI = googleTokenURL
	}
	// The assertion must only be sent to Google's token service, never to a URL
	// supplied by an unrelated key file. Tests replace the transport, not this boundary.
	if account.TokenURI != googleTokenURL && account.TokenURI != "https://accounts.google.com/o/oauth2/token" {
		return serviceAccount{}, nil, ErrUpstream
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(account.PrivateKey))
	if err != nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return serviceAccount{}, nil, ErrUpstream
	}
	return account, key, nil
}

func ValidServiceAccountJSON(raw string) bool {
	_, _, err := parseServiceAccount(raw)
	return err == nil
}

func googleProvider(ctx context.Context, c Credentials, client *http.Client, now time.Time) (Google, error) {
	if c.Property == "" || c.APIKey != "" || c.OriginURL != "" {
		return nil, ErrUpstream
	}
	tokenURL := googleTokenURL
	var body url.Values
	if c.ServiceAccountJSON != "" {
		if c.ClientID != "" || c.ClientSecret != "" || c.RefreshToken != "" {
			return nil, ErrUpstream
		}
		account, key, err := parseServiceAccount(c.ServiceAccountJSON)
		if err != nil {
			return nil, err
		}
		tokenURL = account.TokenURI
		assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": account.ClientEmail, "scope": webmasterScope, "aud": tokenURL,
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		})
		if account.PrivateKeyID != "" {
			assertion.Header["kid"] = account.PrivateKeyID
		}
		signed, err := assertion.SignedString(key)
		if err != nil {
			return nil, ErrUpstream
		}
		body = url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signed}}
	} else {
		if c.ClientID == "" || c.ClientSecret == "" || c.RefreshToken == "" {
			return nil, ErrUpstream
		}
		body = url.Values{"client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "refresh_token": {c.RefreshToken}, "grant_type": {"refresh_token"}}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, ErrUpstream
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := readJSON(client, req, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, ErrUpstream
	}
	return &googleHTTP{client: client, property: c.Property, token: token.AccessToken}, nil
}

func New(st *store.Store, cfg config.Config) *Service {
	s := &Service{DB: st.DB, WebURL: cfg.WebBaseURL}
	load := func(ctx context.Context, source string) (Credentials, error) {
		var row struct {
			Enabled               bool
			CredentialsCiphertext string
		}
		if err := st.DB.WithContext(ctx).Table("seo_provider_settings").Where("source=?", source).Take(&row).Error; err != nil {
			return Credentials{}, err
		}
		if !row.Enabled || row.CredentialsCiphertext == "" {
			return Credentials{}, ErrDisabled
		}
		raw, err := cfg.DecryptSEOSecret("provider:"+source, row.CredentialsCiphertext)
		if err != nil {
			return Credentials{}, err
		}
		var c Credentials
		if json.Unmarshal([]byte(raw), &c) != nil {
			return c, errors.New("invalid encrypted SEO provider settings")
		}
		return c, nil
	}
	s.Google = func(ctx context.Context) (Google, error) {
		c, err := load(ctx, "gsc")
		if err != nil {
			return nil, err
		}
		return googleProvider(ctx, c, apiClient(), s.now())
	}
	s.Bing = func(ctx context.Context) (Bing, error) {
		c, err := load(ctx, "bing")
		if err != nil {
			return nil, err
		}
		if c.Property == "" || c.APIKey == "" {
			return nil, ErrUpstream
		}
		return &bingHTTP{client: apiClient(), property: c.Property, key: c.APIKey}, nil
	}
	s.Audit = func(ctx context.Context) (Auditor, error) {
		c, err := load(ctx, "audit")
		if err != nil {
			return nil, err
		}
		origin, err := url.Parse(c.OriginURL)
		if err != nil || fetch.ValidateURL(origin) != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
			return nil, errors.New("invalid SEO audit origin")
		}
		public, err := url.Parse(cfg.WebBaseURL)
		if err != nil || origin.Host == public.Host {
			return nil, errors.New("SEO audit origin must be distinct from edge")
		}
		return &httpAuditor{client: fetch.NewClient(15 * time.Second), origin: origin, public: public}, nil
	}
	return s
}

func apiClient() *http.Client {
	c := fetch.NewClient(30 * time.Second)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("SEO API redirect refused") }
	return c
}
func readJSON(client *http.Client, req *http.Request, out any) error {
	response, err := client.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		return ErrUpstream
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUpstream
	}
	const limit = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit {
		return ErrUpstream
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrUpstream
	}
	return nil
}

type googleHTTP struct {
	client          *http.Client
	property, token string
}

func (g *googleHTTP) post(ctx context.Context, target string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return ErrUpstream
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	return readJSON(g.client, req, out)
}
func (g *googleHTTP) Analytics(ctx context.Context, q Query) (SearchResult, error) {
	var out SearchResult
	err := g.post(ctx, "https://www.googleapis.com/webmasters/v3/sites/"+url.PathEscape(g.property)+"/searchAnalytics/query", q, &out)
	return out, err
}
func (g *googleHTTP) Inspect(ctx context.Context, target string) (Inspection, error) {
	var out struct {
		InspectionResult struct {
			IndexStatusResult Inspection `json:"indexStatusResult"`
		} `json:"inspectionResult"`
	}
	err := g.post(ctx, "https://searchconsole.googleapis.com/v1/urlInspection/index:inspect", map[string]string{"inspectionUrl": target, "siteUrl": g.property, "languageCode": "en-US"}, &out)
	return out.InspectionResult.IndexStatusResult, err
}

type bingHTTP struct {
	client        *http.Client
	property, key string
}
type bingStats struct {
	Date                  string  `json:"Date"`
	Query                 string  `json:"Query"`
	Clicks                int64   `json:"Clicks"`
	Impressions           int64   `json:"Impressions"`
	AvgImpressionPosition float64 `json:"AvgImpressionPosition"`
}

func bingDate(raw string) (string, error) {
	if strings.HasPrefix(raw, "/Date(") && strings.HasSuffix(raw, ")/") {
		v := strings.TrimSuffix(strings.TrimPrefix(raw, "/Date("), ")/")
		// The optional offset does not change the UTC millisecond timestamp.
		for i := 1; i < len(v); i++ {
			if v[i] == '+' || v[i] == '-' {
				v = v[:i]
				break
			}
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return "", ErrUpstream
		}
		return day(time.UnixMilli(n).UTC()), nil
	}
	if d, err := time.Parse(time.RFC3339, raw); err == nil {
		return day(d), nil
	}
	if _, err := parseDay(raw); err != nil {
		return "", ErrUpstream
	}
	return raw, nil
}
func (b *bingHTTP) get(ctx context.Context, method string, out any) error {
	u := "https://ssl.bing.com/webmaster/api.svc/json/" + method + "?" + url.Values{"siteUrl": {b.property}, "apikey": {b.key}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ErrUpstream
	}
	return readJSON(b.client, req, out)
}
func (b *bingHTTP) Read(ctx context.Context) (BingResult, error) {
	result := BingResult{Crawl: map[string]map[string]int64{}}
	for _, dim := range []struct{ method, dimension string }{{"GetPageStats", "page"}, {"GetQueryStats", "query"}} {
		var out struct {
			D []bingStats `json:"d"`
		}
		if err := b.get(ctx, dim.method, &out); err != nil {
			return BingResult{}, err
		}
		for _, r := range out.D {
			d, err := bingDate(r.Date)
			if err != nil {
				return BingResult{}, err
			}
			result.Rows = append(result.Rows, BingRow{Day: d, Dimension: dim.dimension, Value: r.Query, Clicks: r.Clicks, Impressions: r.Impressions, Position: r.AvgImpressionPosition})
		}
	}
	var crawl struct {
		D []map[string]json.RawMessage `json:"d"`
	}
	if err := b.get(ctx, "GetCrawlStats", &crawl); err != nil {
		return BingResult{}, err
	}
	for _, r := range crawl.D {
		var raw string
		if json.Unmarshal(r["Date"], &raw) != nil {
			return BingResult{}, ErrUpstream
		}
		d, err := bingDate(raw)
		if err != nil {
			return BingResult{}, err
		}
		counts := map[string]int64{}
		for k, v := range r {
			if k == "Date" || k == "__type" {
				continue
			}
			var n int64
			if json.Unmarshal(v, &n) == nil && n >= 0 {
				counts[k] = n
			}
		}
		result.Crawl[d] = counts
	}
	return result, nil
}
