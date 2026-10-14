package sourcefacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Listing is a Mac App Store listing as Apple's lookup API reports it for one storefront. It
// describes the App Store edition, which can differ from the download Homebrew installs.
type Listing struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	BundleID  string   `json:"bundleId"`
	Price     float64  `json:"price"`
	Currency  string   `json:"currency"`
	Region    string   `json:"region"`
	Languages []string `json:"languages"`
	Seller    string   `json:"seller,omitempty"`
}

// MaxLookupIDs keeps one lookup URL short; Apple accepts a comma-separated bundleId list.
const MaxLookupIDs = 20

type AppStore struct {
	Client    *http.Client
	UserAgent string
	// Base defaults to https://itunes.apple.com and Country to "us".
	Base, Country string
}

// Lookup returns Mac listings keyed by lower-case bundle identifier. iPhone and iPad listings
// that share an identifier are ignored.
func (a *AppStore) Lookup(ctx context.Context, ids []string) (map[string]Listing, error) {
	out := map[string]Listing{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > MaxLookupIDs {
		return nil, fmt.Errorf("at most %d bundle identifiers per lookup", MaxLookupIDs)
	}
	base, country := a.Base, a.Country
	if base == "" {
		base = "https://itunes.apple.com"
	}
	if country == "" {
		country = "us"
	}
	query := url.Values{"bundleId": {strings.Join(ids, ",")}, "country": {country}, "entity": {"macSoftware"}, "limit": {"200"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/lookup?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", a.UserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := a.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("app store lookup unavailable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return nil, ErrRateLimited
	default:
		return nil, fmt.Errorf("app store lookup status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return nil, fmt.Errorf("read app store lookup: %w", err)
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("app store lookup response too large")
	}
	var body struct {
		Results []struct {
			Kind      string   `json:"kind"`
			BundleID  string   `json:"bundleId"`
			Name      string   `json:"trackName"`
			URL       string   `json:"trackViewUrl"`
			Price     *float64 `json:"price"`
			Currency  string   `json:"currency"`
			Languages []string `json:"languageCodesISO2A"`
			Seller    string   `json:"sellerName"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode app store lookup: %w", err)
	}
	for _, result := range body.Results {
		link := listingURL(result.URL)
		if result.Kind != "mac-software" || result.BundleID == "" || result.Name == "" || result.Price == nil || *result.Price < 0 || result.Currency == "" || link == "" {
			continue
		}
		languages := []string{}
		for _, code := range result.Languages {
			code = strings.ToUpper(strings.TrimSpace(code))
			if len(code) == 2 && !slices.Contains(languages, code) {
				languages = append(languages, code)
			}
		}
		out[strings.ToLower(result.BundleID)] = Listing{Name: result.Name, URL: link, BundleID: result.BundleID, Price: *result.Price,
			Currency: strings.ToUpper(result.Currency), Region: strings.ToUpper(country), Languages: languages, Seller: result.Seller}
	}
	return out, nil
}

// listingURL keeps apps.apple.com links and drops Apple's affiliate marker.
func listingURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "apps.apple.com" {
		return ""
	}
	query := u.Query()
	query.Del("uo")
	u.RawQuery = query.Encode()
	return u.String()
}

// Match picks the first identifier, in the order the definition ranks them, whose listing also
// carries one of the app's names.
func Match(ids []string, names []string, listings map[string]Listing) (Listing, bool) {
	for _, id := range ids {
		if listing, ok := listings[strings.ToLower(id)]; ok && NameMatches(names, listing.Name) {
			return listing, true
		}
	}
	return Listing{}, false
}
