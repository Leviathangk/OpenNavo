//go:build integration

package seokeywords

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestKeywordLedgerTargetsAndRetryIsolation(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','example-editor','example-editor','homebrew/cask','Example Editor','1.0','1.0','{}','fixture')`).Error)
	s := &Service{DB: st.DB, WebURL: "https://example.test", Now: func() time.Time { return now }}
	volume := int64(100)
	o := Observation{Keyword: "Ｅxample＿Editor mac", Source: "gsc", Engine: "google", Country: "US", Language: "en-US", Device: "desktop", Period: "2026-10", SearchVolume: &volume, VolumeState: "known", ObservedAt: now}
	target := Target{EntityKey: "pkg:701", Intent: "download", Locale: "en-US", TargetURL: "https://example.test/apps/example-editor"}
	request := Request{Records: []Observation{o}, Targets: []Target{target}}
	first, err := s.Upsert(ctx, request, false, store.AuditActor{})
	require.NoError(t, err)
	second, err := s.Upsert(ctx, request, false, store.AuditActor{})
	require.NoError(t, err)
	require.Equal(t, first, second)
	var count int64
	require.NoError(t, st.DB.Table("seo_keywords").Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.NoError(t, st.DB.Table("seo_keyword_links").Where("package_id=701 AND state='confirmed'").Count(&count).Error)
	require.Equal(t, int64(1), count)
	// A stale import does not overwrite a newer observation.
	stale := o
	v := int64(2)
	stale.SearchVolume = &v
	stale.ObservedAt = now.Add(-time.Hour)
	_, err = s.Upsert(ctx, Request{Records: []Observation{stale}}, false, store.AuditActor{})
	require.NoError(t, err)
	var stored int64
	require.NoError(t, st.DB.Raw("SELECT search_volume FROM seo_keywords").Scan(&stored).Error)
	require.Equal(t, volume, stored)
	// A dry run validates the full transaction, including unique targets, then rolls it all back.
	preview := o
	preview.Keyword = "preview editor"
	result, err := s.Upsert(ctx, Request{Records: []Observation{preview}}, true, store.AuditActor{})
	require.NoError(t, err)
	require.Equal(t, true, result["dryRun"])
	require.NoError(t, st.DB.Table("seo_keywords").Count(&count).Error)
	require.Equal(t, int64(1), count)
	// Two workers cannot create different pages for one intent. The losing observation rolls back.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := o
			r.Keyword = fmt.Sprintf("concurrent %d", i)
			t := target
			t.Intent = "alternative"
			t.TargetURL = fmt.Sprintf("https://example.test/articles/alternative-%d", i)
			_, e := s.Upsert(ctx, Request{Records: []Observation{r}, Targets: []Target{t}}, false, store.AuditActor{})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	failures := 0
	for err := range errs {
		if err != nil {
			failures++
		}
	}
	require.Equal(t, 1, failures)
	require.NoError(t, st.DB.Table("seo_targets").Where("intent='alternative'").Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.NoError(t, st.DB.Table("seo_keywords").Count(&count).Error)
	require.Equal(t, int64(2), count)
	// Unsupported observations remain null, never zero, even if a caller submits zero.
	ru := o
	ru.Source = "openseo:dataforseo"
	ru.Language = "ru-RU"
	ru.Country = "RU"
	_, err = s.Upsert(ctx, Request{Records: []Observation{ru}}, false, store.AuditActor{})
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_keywords").Where("language='ru-RU' AND volume_state='unsupported' AND search_volume IS NULL").Count(&count).Error)
	require.Equal(t, int64(1), count)
}
