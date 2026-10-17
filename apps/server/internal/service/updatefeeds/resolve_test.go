package updatefeeds

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func githubRule() store.UpdateFeed {
	return store.UpdateFeed{UpdateFeedInput: domain.UpdateFeedInput{UpdateFeedKey: domain.UpdateFeedKey{BundleID: "test.example.App", Source: "github"}, FeedURL: "https://api.github.com/repos/example/app/releases/latest", Channel: "stable", TeamID: ptr("TESTTEAM01"), GithubRepo: ptr("example/app"), AssetPattern: ptr(defaultMacPattern)}}
}
func githubBody(digest string) []byte {
	return []byte(`{"tag_name":"v2.1.0","published_at":"2026-10-15T00:00:00Z","body":"Notes","assets":[{"name":"App-mac-arm64.zip","size":8,"browser_download_url":"https://github.com/example/app/releases/download/v2.1.0/App-mac-arm64.zip","digest":"` + digest + `"},{"name":"App-win.zip","size":8,"browser_download_url":"https://github.com/example/app/releases/download/v2.1.0/App-win.zip","digest":"` + digest + `"}]}`)
}
func TestGithubDigestRepositoryAndChannel(t *testing.T) {
	rule := githubRule()
	release, err := parseGitHub(githubBody("sha256:"+strings.Repeat("a", 64)), rule)
	require.NoError(t, err)
	require.Equal(t, "2.1.0", release.Version)
	require.Len(t, release.Files, 1)
	require.Equal(t, "arm64", *release.Files[0].Architecture)
	for _, digest := range []string{"", "sha256:bad", "sha512:" + strings.Repeat("a", 64)} {
		_, err := parseGitHub(githubBody(digest), rule)
		require.Error(t, err)
	}
	_, err = parseGitHub([]byte(strings.ReplaceAll(string(githubBody("sha256:"+strings.Repeat("a", 64))), "github.com/example/app/", "github.com/attacker/app/")), rule)
	require.Error(t, err)
	data := strings.Replace(string(githubBody("sha256:"+strings.Repeat("b", 64))), `"tag_name"`, `"prerelease":true,"tag_name"`, 1)
	_, err = parseGitHub([]byte(data), rule)
	require.Error(t, err)
	rule.Channel = "beta"
	_, err = parseGitHub([]byte("["+data+"]"), rule)
	require.NoError(t, err)
}

type githubReader struct {
	t     *testing.T
	calls int
}

func (g *githubReader) Get(_ context.Context, target string, _ int64, h http.Header) ([]byte, int, http.Header, error) {
	g.calls++
	require.Equal(g.t, "https://api.github.com/repos/example/app/releases/latest", target)
	require.Empty(g.t, h.Get("If-None-Match"))
	require.Empty(g.t, h.Get("Authorization"))
	if g.calls > 1 {
		if g.calls == 2 {
			require.Equal(g.t, "Thu, 15 Oct 2026 00:00:00 GMT", h.Get("If-Modified-Since"))
		}
		return nil, 304, nil, nil
	}
	return githubBody("sha256:" + strings.Repeat("a", 64)), 200, http.Header{"Last-Modified": {"Thu, 15 Oct 2026 00:00:00 GMT"}, "Etag": {"changes-with-download-count"}}, nil
}
func TestGithubUsesOnlyModifiedSinceAndRetainsVerifiedBody(t *testing.T) {
	client := &githubReader{t: t}
	s := &Service{GitHub: client}
	row := githubRule()
	release, modified, err := s.github(context.Background(), row)
	require.NoError(t, err)
	row.Release = release
	row.LastModified = &modified
	row.VerifiedAt = ptr(time.Now())
	row.ETag = ptr("ignored")
	again, _, err := s.github(context.Background(), row)
	require.NoError(t, err)
	require.Equal(t, release, again)
	row.Release = nil
	_, _, err = s.github(context.Background(), row)
	require.Error(t, err)
}
func TestElectronYAMLBoundsRelativeFilesAndHashes(t *testing.T) {
	hash := base64.StdEncoding.EncodeToString(make([]byte, 64))
	body := []byte("version: 2.1.0\nfiles:\n  - url: App-arm64.zip\n    sha512: " + hash + "\n    size: 12\n  - url: App-x64.zip\n    sha512: " + hash + "\n")
	release, err := parseElectron(body, "https://updates.example.org/stable/latest-mac.yml")
	require.NoError(t, err)
	require.Len(t, release.Files, 2)
	require.Equal(t, "https://updates.example.org/stable/App-arm64.zip", release.Files[0].URL)
	require.Equal(t, "x86_64", *release.Files[1].Architecture)
	for _, bad := range []string{strings.ReplaceAll(string(body), hash, "invalid"), string(body) + "---\nversion: 4", string(body) + "alias: &a [1]\nreference: *a", strings.ReplaceAll(string(body), "App-arm64.zip", "http://example.org/App.zip")} {
		_, err := parseElectron([]byte(bad), "https://example.org/latest-mac.yml")
		require.Error(t, err)
	}
}
func TestVendorURLDigestMustComeFromSameObject(t *testing.T) {
	rule := domain.VendorResolver{Mode: "json", VersionKey: "CFBundleShortVersionString", VersionPath: "version", DownloadPath: "files.0.url", ChecksumForURL: true}
	body := []byte(`{"version":"v2.0","files":[{"url":"https://example.org/App-arm64.zip","sha256":"` + strings.Repeat("a", 64) + `"},{"url":"https://example.org/App-x64.zip","sha256":"` + strings.Repeat("b", 64) + `"}]}`)
	r, err := parseVendor(body, "https://example.org/meta", rule, "stable")
	require.NoError(t, err)
	require.Equal(t, "2.0", r.Version)
	require.Equal(t, strings.Repeat("a", 64), *r.Files[0].SHA256)
	_, err = parseVendor([]byte(strings.Replace(string(body), `"sha256"`, `"bad"`, 1)), "https://example.org/meta", rule, "stable")
	require.Error(t, err)
	_, err = parseVendor([]byte(strings.ReplaceAll(string(body), "https://example.org/App-arm64.zip", "http://127.0.0.1/App.zip")), "https://example.org/meta", rule, "stable")
	require.Error(t, err)
}
func TestSeedRulesCarryKnownIdentityAndNoLocalSecrets(t *testing.T) {
	rules, err := SeedRules()
	require.NoError(t, err)
	require.Len(t, rules, 20)
	seen := map[string]bool{}
	for _, r := range rules {
		require.NotNil(t, r.TeamID)
		require.NotNil(t, r.Resolver)
		require.False(t, seen[r.BundleID])
		seen[r.BundleID] = true
		require.NotContains(t, r.FeedURL, "__IDENTITY__")
	}
	require.True(t, seen["com.sogou.inputmethod.sogou"])
	require.True(t, seen["com.bytedance.inputmethod.doubaoime"])
	var identities []seededIdentity
	require.NoError(t, json.Unmarshal(identityData, &identities))
	require.Len(t, identities, 254)
	require.NotContains(t, string(identityData), "lastSeenURL")
}
func TestVendorSparkleAssociatesVersionWithItsEnclosure(t *testing.T) {
	body := `<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>1</s:version><s:shortVersionString>1.0</s:shortVersionString><enclosure url="https://example.org/One.dmg"/></item><item><s:version>2</s:version><s:shortVersionString>2.0</s:shortVersionString><enclosure url="https://example.org/Two.dmg"/></item></channel></rss>`
	r, err := vendorSparkle([]byte(body), "stable")
	require.NoError(t, err)
	require.Equal(t, "2.0", r.Version)
	require.Equal(t, "https://example.org/Two.dmg", r.Files[0].URL)
}

func TestVendorScopedRecordsAvoidOldWindowsOrOtherProductVersions(t *testing.T) {
	rule := domain.VendorResolver{Mode: "json", VersionKey: "CFBundleShortVersionString", ScopePath: "data.manifest.darwin.versions", Match: map[string]string{"arch": "apple", "region": "va"}, VersionPath: "version", DownloadPath: "url"}
	body := []byte(`{"data":{"manifest":{"win32":{"version":"9.9"},"darwin":{"versions":[{"arch":"apple","region":"cn","version":"2","url":"https://example.org/CN.dmg"},{"arch":"apple","region":"va","version":"3","url":"https://example.org/VA.dmg"}]}},"solo":{"version":"100"}}}`)
	r, err := parseVendor(body, "https://example.org/meta", rule, "stable")
	require.NoError(t, err)
	require.Equal(t, "3", r.Version)
	require.Equal(t, "https://example.org/VA.dmg", r.Files[0].URL)
	rule.Match = nil
	_, err = parseVendor(body, "https://example.org/meta", rule, "stable")
	require.Error(t, err)
}

func TestVendorSparkleFiltersChannelBeforeSelectingVersion(t *testing.T) {
	body := []byte(`<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>
 <item><s:version>1</s:version><s:shortVersionString>1.0</s:shortVersionString><enclosure url="https://example.org/Stable.dmg"/></item>
 <item><s:version>1</s:version><s:shortVersionString>2.0</s:shortVersionString><s:channel>beta</s:channel><enclosure url="https://example.org/Beta.dmg"/></item>
 <item><s:version>1</s:version><s:shortVersionString>3.0</s:shortVersionString><s:channel>nightly</s:channel><enclosure url="https://example.org/Nightly.dmg"/></item>
 </channel></rss>`)
	for _, channel := range []string{"", "stable", "beta"} {
		r, err := parseVendor(body, "https://example.org/feed", domain.VendorResolver{Mode: "sparkle"}, channel)
		require.NoError(t, err)
		want := "1.0"
		if channel == "beta" {
			want = "2.0"
		}
		require.Equal(t, want, r.Version)
	}
	stable := []byte(`<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><enclosure s:version="4" s:shortVersionString="4.0" url="https://example.org/Stable.dmg"/></item></channel></rss>`)
	for _, channel := range []string{"stable", "beta"} {
		r, err := vendorSparkle(stable, channel)
		require.NoError(t, err)
		require.Equal(t, "4.0", r.Version)
	}
	_, err := vendorSparkle([]byte(strings.ReplaceAll(string(stable), `<item>`, `<item><s:channel>beta</s:channel>`)), "stable")
	require.Error(t, err)
}

func vendorArchitectureFixture(mode, target, digest string) ([]byte, domain.VendorResolver) {
	rule := domain.VendorResolver{Mode: mode, VersionKey: "CFBundleShortVersionString"}
	var body string
	switch mode {
	case "sparkle":
		body = `<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>200</s:version><s:shortVersionString>2.0</s:shortVersionString><enclosure url="` + target + `"/></item></channel></rss>`
	case "electron":
		body = "version: 2.0\nreleaseNotes: Release notes\nminimumSystemVersion: '12.0'\nfiles:\n  - url: " + target + "\n    sha512: " + base64.StdEncoding.EncodeToString([]byte(digest)) + "\n    size: 12\n"
	case "json":
		rule.VersionPath, rule.DownloadPath, rule.SHA256Path = "version", "url", "sha256"
		body = `{"version":"2.0","url":"` + target + `","sha256":"` + digest + `"}`
	case "text":
		rule.VersionPattern, rule.DownloadPattern, rule.SHA256Pattern = `version=(\S+)`, `url=(\S+)`, `sha256=(\S+)`
		body = "version=2.0\nurl=" + target + "\nsha256=" + digest
	case "redirect":
		rule.VersionPattern = `/([0-9.]+)/`
		body = target
	}
	return []byte(body), rule
}

func TestVendorArchitectureAcrossParsingModes(t *testing.T) {
	for _, mode := range []string{"sparkle", "electron", "json", "text", "redirect"} {
		for _, tc := range []struct {
			name, filename, explicit, want string
		}{
			{"explicit_without_filename_hint", "App.zip", "arm64", "arm64"},
			{"explicit_overrides_filename", "App-x64.zip", "arm64", "arm64"},
			{"explicit_universal", "App-arm64.zip", "universal", "universal"},
			{"inferred_arm64", "App-arm64.zip", "", "arm64"},
			{"inferred_x86_64", "App-x64.zip", "", "x86_64"},
			{"unspecified", "App.zip", "", ""},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				target := "https://example.org/2.0/" + tc.filename
				body, rule := vendorArchitectureFixture(mode, target, strings.Repeat("a", 64))
				original, err := parseVendor(body, target, rule, "stable")
				require.NoError(t, err)
				rule.Architecture = tc.explicit
				release, err := parseVendor(body, target, rule, "stable")
				require.NoError(t, err)
				require.Len(t, release.Files, 1)
				if tc.want == "" {
					require.Nil(t, release.Files[0].Architecture)
				} else {
					require.Equal(t, ptr(tc.want), release.Files[0].Architecture)
				}
				// Architecture configuration must not change URLs, versions, digests,
				// notes, sizes, or other metadata produced by the parser.
				release.Files[0].Architecture = original.Files[0].Architecture
				require.Equal(t, original, release)
			})
		}
	}
}

type vendorMetadataTransport func(*http.Request) (*http.Response, error)

func (f vendorMetadataTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResolveVendorArchitectureVariantsPreserveFilePairing(t *testing.T) {
	for _, mode := range []string{"sparkle", "electron", "json", "text", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			_, rule := vendorArchitectureFixture(mode, "", "")
			rule.Architecture = "universal"
			rule.Variants = []domain.VendorVariant{
				{URL: "https://example.org/2.0/one/App.zip", Architecture: "arm64"},
				{URL: "https://example.org/2.0/two/App.zip", Architecture: "x86_64"},
			}
			calls := 0
			s := &Service{HTTP: &http.Client{Transport: vendorMetadataTransport(func(r *http.Request) (*http.Response, error) {
				require.Less(t, calls, len(rule.Variants))
				require.Equal(t, rule.Variants[calls].URL, r.URL.String())
				hash := strings.Repeat([]string{"a", "b"}[calls], 64)
				body, _ := vendorArchitectureFixture(mode, r.URL.String(), hash)
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			release, err := s.ResolveVendor(context.Background(), domain.UpdateFeedInput{
				UpdateFeedKey: domain.UpdateFeedKey{BundleID: "org.example.App", Source: "vendor"},
				FeedURL:       "https://example.org/feed", TeamID: ptr("TESTTEAM01"), Resolver: &rule,
			})
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Len(t, release.Files, 2)
			require.Equal(t, "2.0", release.Version)
			for i, file := range release.Files {
				require.Equal(t, "App.zip", file.Name)
				require.Equal(t, rule.Variants[i].URL, file.URL)
				require.Equal(t, ptr(rule.Variants[i].Architecture), file.Architecture)
				hash := strings.Repeat([]string{"a", "b"}[i], 64)
				switch mode {
				case "electron":
					require.Equal(t, ptr(base64.StdEncoding.EncodeToString([]byte(hash))), file.SHA512)
				case "json", "text":
					require.Equal(t, ptr(hash), file.SHA256)
				}
			}
		})
	}
}

func TestVendorElectronArchitectureAppliesToEveryFile(t *testing.T) {
	for _, explicit := range []string{"arm64", "universal"} {
		t.Run(explicit, func(t *testing.T) {
			hash := base64.StdEncoding.EncodeToString(make([]byte, 64))
			body := []byte("version: 2.0\nfiles:\n  - url: App.zip\n    sha512: " + hash + "\n  - url: App-x64.dmg\n    sha512: " + hash + "\n")
			release, err := parseVendor(body, "https://example.org/feed", domain.VendorResolver{Mode: "electron", Architecture: explicit}, "stable")
			require.NoError(t, err)
			require.Len(t, release.Files, 2)
			for _, file := range release.Files {
				require.Equal(t, ptr(explicit), file.Architecture)
				require.Equal(t, ptr(hash), file.SHA512)
			}
		})
	}
}

func TestResolveVendorArchitectureDoesNotHideInvalidFeeds(t *testing.T) {
	for _, mode := range []string{"sparkle", "electron"} {
		for _, failure := range []string{"invalid_feed", "invalid_url", "invalid_file", "invalid_digest", "invalid_version_pattern", "second_variant_invalid", "versions_differ"} {
			if failure == "invalid_digest" && mode != "electron" || failure == "invalid_version_pattern" && mode != "sparkle" {
				continue
			}
			t.Run(mode+"/"+failure, func(t *testing.T) {
				rule := domain.VendorResolver{Mode: mode, Architecture: "arm64", Variants: []domain.VendorVariant{
					{URL: "https://example.org/arm64/feed", Architecture: "arm64"},
					{URL: "https://example.org/x86_64/feed", Architecture: "x86_64"},
				}}
				if failure == "invalid_version_pattern" {
					rule.VersionPattern = `^v([0-9.]+)$`
				}
				calls := 0
				s := &Service{HTTP: &http.Client{Transport: vendorMetadataTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					body, _ := vendorArchitectureFixture(mode, "https://example.org/App.zip", strings.Repeat("a", 64))
					switch failure {
					case "invalid_feed":
						body = []byte("invalid feed")
					case "invalid_url":
						body = []byte(strings.ReplaceAll(string(body), "https://example.org/App.zip", "http://example.org/App.zip"))
					case "invalid_file":
						body = []byte(strings.ReplaceAll(string(body), "App.zip", "App.exe"))
					case "invalid_digest":
						body = []byte(strings.ReplaceAll(string(body), base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 64))), "bad-digest"))
					case "second_variant_invalid":
						if calls == 2 {
							body = []byte("invalid feed")
						}
					case "versions_differ":
						if calls == 2 {
							body = []byte(strings.ReplaceAll(string(body), "2.0", "3.0"))
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})}}
				release, err := s.ResolveVendor(context.Background(), domain.UpdateFeedInput{
					UpdateFeedKey: domain.UpdateFeedKey{BundleID: "org.example.App", Source: "vendor"},
					FeedURL:       "https://example.org/feed", TeamID: ptr("TESTTEAM01"), Resolver: &rule,
				})
				require.Error(t, err)
				require.Nil(t, release)
				if failure == "versions_differ" {
					require.EqualError(t, err, "vendor architecture versions differ; await coherent release")
				}
			})
		}
	}
}

func TestVendorSparklePreservesMinimumSystemVersion(t *testing.T) {
	for _, tc := range []struct{ item, enclosure, want string }{
		{"<s:minimumSystemVersion>14.0</s:minimumSystemVersion>", "", "14.0"},
		{"", `s:minimumSystemVersion="13.0"`, "13.0"},
		{"<s:minimumSystemVersion>14.0</s:minimumSystemVersion>", `s:minimumSystemVersion="13.0"`, "14.0"},
		{"", "", ""},
	} {
		body := `<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>1</s:version><s:shortVersionString>1.0</s:shortVersionString><s:minimumSystemVersion>11.0</s:minimumSystemVersion><enclosure url="https://example.org/Old.dmg"/></item><item><s:version>2</s:version><s:shortVersionString>2.0</s:shortVersionString>` + tc.item + `<enclosure url="https://example.org/New.dmg" ` + tc.enclosure + `/></item></channel></rss>`
		r, err := parseVendor([]byte(body), "https://example.org/feed", domain.VendorResolver{Mode: "sparkle"}, "stable")
		require.NoError(t, err)
		require.Equal(t, "2.0", r.Version)
		if tc.want == "" {
			require.Nil(t, r.MinimumOS)
		} else {
			require.NotNil(t, r.MinimumOS)
			require.Equal(t, tc.want, *r.MinimumOS)
		}
	}
}

func TestVendorVariantsRequireConsistentMinimumOS(t *testing.T) {
	for _, mode := range []string{"sparkle", "electron"} {
		for _, minima := range [][2]string{{"13.0", "15.0"}, {"13.0", ""}, {"13.0", "13.0"}, {"", ""}} {
			for _, reverse := range []bool{false, true} {
				rule := domain.VendorResolver{Mode: mode, Variants: []domain.VendorVariant{
					{URL: "https://example.org/x86_64/feed", Architecture: "x86_64"},
					{URL: "https://example.org/arm64/feed", Architecture: "arm64"},
				}}
				if reverse {
					rule.Variants[0], rule.Variants[1] = rule.Variants[1], rule.Variants[0]
				}
				s := &Service{HTTP: &http.Client{Transport: vendorMetadataTransport(func(r *http.Request) (*http.Response, error) {
					minimum := minima[0]
					if strings.Contains(r.URL.Path, "arm64") {
						minimum = minima[1]
					}
					body, _ := vendorArchitectureFixture(mode, "https://example.org/App.zip", strings.Repeat("a", 64))
					content := string(body)
					if mode == "sparkle" && minimum != "" {
						content = strings.Replace(content, "<item>", "<item><s:minimumSystemVersion>"+minimum+"</s:minimumSystemVersion>", 1)
					} else if mode == "electron" {
						content = strings.Replace(content, "minimumSystemVersion: '12.0'", "minimumSystemVersion: '"+minimum+"'", 1)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(content))}, nil
				})}}
				release, err := s.ResolveVendor(context.Background(), domain.UpdateFeedInput{
					UpdateFeedKey: domain.UpdateFeedKey{BundleID: "org.example.App", Source: "vendor"},
					FeedURL:       "https://example.org/feed", TeamID: ptr("TESTTEAM01"), Resolver: &rule,
				})
				if minima[0] != minima[1] {
					require.EqualError(t, err, "vendor architecture minimum system versions differ; cannot merge release")
					require.Nil(t, release)
				} else {
					require.NoError(t, err)
					require.Len(t, release.Files, 2)
					if minima[0] == "" {
						require.Nil(t, release.MinimumOS)
					} else {
						require.Equal(t, ptr(minima[0]), release.MinimumOS)
					}
				}
			}
		}
	}
}
