package seoingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeGoogle struct {
	query   func(Query) (SearchResult, error)
	inspect func(string) (Inspection, error)
}

func (f fakeGoogle) Analytics(_ context.Context, q Query) (SearchResult, error) { return f.query(q) }
func (f fakeGoogle) Inspect(_ context.Context, u string) (Inspection, error)    { return f.inspect(u) }

func TestGSCCompletenessPaginationAndFailure(t *testing.T) {
	for _, source := range []string{"gsc_page", "gsc_query"} {
		t.Run(source, func(t *testing.T) {
			calls := 0
			fake := fakeGoogle{query: func(q Query) (SearchResult, error) {
				calls++
				require.Equal(t, "all", q.DataState)
				require.Equal(t, "date", q.Dimensions[0])
				require.Equal(t, (calls-1)*pageLimit, q.StartRow)
				result := SearchResult{}
				result.Metadata.FirstIncompleteDate = "2026-10-18"
				if calls == 1 {
					for i := 0; i < pageLimit; i++ {
						keys := []string{"2026-10-18", fmt.Sprintf("https://example.test/apps/item-%d", i), "example query"}
						if source == "gsc_page" {
							keys = []string{keys[0], keys[1], "usa", "DESKTOP"}
						}
						result.Rows = append(result.Rows, SearchRow{Keys: keys, Impressions: 1, Position: 1})
					}
				}
				return result, nil
			}}
			rows, first, complete, err := fetchDay(context.Background(), fake, "2026-10-18", source)
			require.NoError(t, err)
			require.Len(t, rows, pageLimit)
			require.Equal(t, "2026-10-18", first)
			require.True(t, complete)
			require.Equal(t, 2, calls)
		})
	}
	calls := 0
	fake := fakeGoogle{query: func(q Query) (SearchResult, error) {
		calls++
		result := SearchResult{}
		for i := 0; i < pageLimit; i++ {
			result.Rows = append(result.Rows, SearchRow{Keys: []string{q.StartDate, fmt.Sprintf("https://example.test/%d", q.StartRow+i), "query"}, Impressions: 1, Position: 1})
		}
		return result, nil
	}}
	rows, _, complete, err := fetchDay(context.Background(), fake, "2026-10-18", "gsc_query")
	require.NoError(t, err)
	require.Len(t, rows, maxDailyRows)
	require.False(t, complete)
	require.Equal(t, 2, calls)
	fake.query = func(Query) (SearchResult, error) { return SearchResult{}, context.DeadlineExceeded }
	rows, _, _, err = fetchDay(context.Background(), fake, "2026-10-18", "gsc_query")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, rows)
}

func TestAuditRulesMatchRegistryAndDetectSSRFailures(t *testing.T) {
	var registry struct{ Rules []struct{ ID string } }
	require.NoError(t, json.Unmarshal(AuditRules, &registry))
	expected := `{"status":200,"canonical":"https://example.test/apps/sample","robots":"index,follow","requiredText":["Important release notes"],"hreflang":{"ja":"https://example.test/ja/apps/sample"},"factsSelector":"#facts","faqSelector":"#faq"}`
	u := URL{URL: "https://example.test/apps/sample", Expected: expected}
	issues, err := CheckDocument(u, Document{Status: 200, Header: http.Header{"X-Robots-Tag": {"noindex"}}, HTML: `<link rel="canonical" href="https://example.test/wrong"><meta name="robots" content="index,follow"><script type="application/ld+json">{"@id":"#missing"}</script><div id="faq"></div><div id="facts"></div><a href="/apps/sample?returnTo=recent">x</a>`})
	require.NoError(t, err)
	got := map[string]bool{}
	for _, issue := range issues {
		got[issue.Rule] = true
	}
	for _, rule := range registry.Rules {
		if rule.ID != "seo.fetch" && rule.ID != "seo.status" {
			require.True(t, got[rule.ID], rule.ID)
		}
	}
	statusIssues, statusErr := CheckDocument(u, Document{Status: 503, Header: http.Header{}})
	require.NoError(t, statusErr)
	require.Contains(t, statusIssues, Issue{"seo.status", "p0", "unexpected HTTP status"})
	u.Expected = `{"requiredText":["Only inside script"]}`
	issues, err = CheckDocument(u, Document{Status: 200, Header: http.Header{}, HTML: `<link rel="canonical" href="https://example.test/apps/sample"><script>Only inside script</script>`})
	require.NoError(t, err)
	require.Contains(t, issues, Issue{"seo.ssr_content", "p1", "required content absent from SSR"})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGoogleHTTPUsesMetadataAndRedactsCredentialFailures(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "www.googleapis.com", r.URL.Host)
		require.Equal(t, "Bearer fake-token", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"rows":[],"metadata":{"first_incomplete_date":"2026-10-18"}}`)), Header: http.Header{}}, nil
	})}
	g := &googleHTTP{client: client, property: "https://example.test/", token: "fake-token"}
	r, err := g.Analytics(context.Background(), Query{Dimensions: []string{"date"}})
	require.NoError(t, err)
	require.Equal(t, "2026-10-18", r.Metadata.FirstIncompleteDate)
	client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("fake-token"))}, nil
	})
	_, err = g.Analytics(context.Background(), Query{})
	require.ErrorIs(t, err, ErrUpstream)
	require.NotContains(t, err.Error(), "fake-token")
}

func TestBingDates(t *testing.T) {
	for _, v := range []string{"2026-10-18", "2026-10-18T00:00:00Z", "/Date(1792281600000)/", "/Date(1792281600000+0000)/"} {
		d, err := bingDate(v)
		require.NoError(t, err)
		require.Equal(t, "2026-10-18", d)
	}
	_, err := bingDate("invalid")
	require.Error(t, err)
}
