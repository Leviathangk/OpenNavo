package domain

import (
	"strings"
	"testing"
)

// A submission that names a country belongs on the matching map tab.
func TestRegionForCountryUsesTheCountryTab(t *testing.T) {
	// The padded code is deliberate: submissions are free text and the country is trimmed and
	// upper-cased before it becomes a tab.
	cases := []struct {
		code string
		want string
	}{
		{"US", "americas"},
		{"us", "americas"},
		{" GB ", "europe"},
		{"JP", "apac"},
		{"IL", "apac"},
		{"ZZ", "other"},
		{"", "other"},
	}
	for _, test := range cases {
		if got := RegionForCountry(test.code); got != test.want {
			t.Errorf("RegionForCountry(%q) = %q, want %q", test.code, got, test.want)
		}
	}
}

func TestCountryPointsCoverTheMapAndStayInBounds(t *testing.T) {
	if len(countryLocations) < 200 {
		t.Fatalf("expected broad country coverage, got %d codes", len(countryLocations))
	}
	for code, location := range countryLocations {
		if len(code) != 2 || code != strings.ToUpper(code) {
			t.Errorf("country code %q is not normalized", code)
		}
		if location.Lat < -90 || location.Lat > 90 || location.Lng < -180 || location.Lng > 180 {
			t.Errorf("%s has an invalid point: %v,%v", code, location.Lat, location.Lng)
		}
		if location.Region != "americas" && location.Region != "europe" && location.Region != "apac" && location.Region != "other" {
			t.Errorf("%s has an invalid region %q", code, location.Region)
		}
	}
	for _, code := range []string{"US", "GB", "CN", "JP", "IN", "BR", "ZA", "NG", "AU", "TW", "HK"} {
		if _, ok := CountryPoint(code); !ok {
			t.Errorf("expected a point for %s", code)
		}
	}
	if _, ok := CountryPoint("ZZ"); ok {
		t.Error("unsupported country ZZ must not receive a point")
	}
}

func TestSupportedCountryCodesMatchTheMap(t *testing.T) {
	codes := SupportedCountryCodes()
	if len(codes) != len(countryLocations) {
		t.Fatalf("SupportedCountryCodes returned %d codes, want %d", len(codes), len(countryLocations))
	}
	for index := 1; index < len(codes); index++ {
		if codes[index-1] >= codes[index] {
			t.Fatalf("country codes are not sorted: %q before %q", codes[index-1], codes[index])
		}
	}
	for _, code := range codes {
		if _, ok := CountryPoint(code); !ok {
			t.Errorf("SupportedCountryCodes returned %q without a point", code)
		}
	}
}
