package store

import (
	"encoding/json"
	"testing"
)

func TestDeveloperPayloadTokenBase(t *testing.T) {
	cases := map[string]string{
		"Ghostty":            "ghostty",
		"Visual Studio Code": "visual-studio-code",
		"Things 3":           "things-3",
		"  Café  App  ":      "caf-app",
		"":                   "app",
		"!!!":                "app",
	}
	for name, want := range cases {
		payload := DeveloperPayload{Name: name}
		if got := payload.tokenBase(); got != want {
			t.Errorf("tokenBase(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDeveloperPayloadGithubRepo(t *testing.T) {
	cases := []struct {
		name   string
		values DeveloperPayload
		want   string
	}{
		{"repo url", DeveloperPayload{RepoURL: "https://github.com/ghostty-org/ghostty"}, "ghostty-org/ghostty"},
		{"repo url with suffix", DeveloperPayload{RepoURL: "https://github.com/owner/repo.git"}, "owner/repo"},
		{"source fallback", DeveloperPayload{SourceURL: "https://github.com/owner/repo/tree/main"}, "owner/repo"},
		{"not github", DeveloperPayload{RepoURL: "https://example.com/owner/repo"}, ""},
		{"owner only", DeveloperPayload{RepoURL: "https://github.com/owner"}, ""},
	}
	for _, tc := range cases {
		repo := tc.values.GithubRepo()
		got := ""
		if repo != nil {
			got = *repo
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseDeveloperPayloadNormalizes(t *testing.T) {
	raw := json.RawMessage(`{"name":"  Ghostty  ","summary":" Fast ","countryCode":"us","city":"  San Francisco ","version":"1.2.0","remote":true}`)
	payload := ParseDeveloperPayload(raw)
	if payload.Name != "Ghostty" || payload.Summary != "Fast" || payload.CountryCode != "US" || payload.City != "San Francisco" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if !payload.HasLocation() || !payload.Remote || payload.Version != "1.2.0" {
		t.Fatalf("unexpected flags: %+v", payload)
	}
	empty := ParseDeveloperPayload(nil)
	if empty.HasLocation() || empty.Name != "" {
		t.Fatalf("nil payload should be empty: %+v", empty)
	}
}
