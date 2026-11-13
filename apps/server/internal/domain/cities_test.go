package domain

import "testing"

// A submission that names only a country still belongs on the matching map tab.
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

// Country-only submissions and city submissions must agree, so one country never
// spreads across two tabs.
func TestRegionForCountryMatchesCityRegions(t *testing.T) {
	for country, entries := range cities {
		region := RegionForCountry(country)
		if region == "other" {
			t.Errorf("country %q has cities but no region", country)
			continue
		}
		for key, entry := range entries {
			if entry.region != region {
				t.Errorf("%s/%s is %q but the country is %q", country, key, entry.region, region)
			}
		}
	}
}
