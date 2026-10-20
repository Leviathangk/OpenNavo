//go:build integration

package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestV2KeepsLegacyDesktopLanguageContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,source_locale,raw,raw_hash) VALUES(701,'cask','editor','editor','homebrew/cask','Editor','1','1','de-DE','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO categories(id,slug,icon,source_locale) VALUES(701,'tools','box','de-DE')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,source,status,summary) VALUES(701,'de-DE','de-DE','human','source','Werkzeug')`).Error)
	svc := &Service{Store: st, Objects: memoryObjects{}}
	bundle, err := svc.BuildLocal(ctx)
	require.NoError(t, err)
	raw, err := json.Marshal(bundle.Info.TextPacks)
	require.NoError(t, err)
	var packs map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &packs))
	require.Len(t, packs, 6)
	require.NotContains(t, packs, "de-DE")
	require.Len(t, bundle.Files, 7)
	reader, err := gzip.NewReader(bytes.NewReader(bundle.Files[0].Data))
	require.NoError(t, err)
	raw, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var base struct {
		Items      []struct{ SourceLocale string }
		Categories []struct{ SourceLocale string }
	}
	require.NoError(t, json.Unmarshal(raw, &base))
	require.Len(t, base.Items, 1)
	require.Len(t, base.Categories, 1)
	require.Equal(t, "en-US", base.Items[0].SourceLocale)
	require.Equal(t, "en-US", base.Categories[0].SourceLocale)
}

func TestLinksKeepLegacyPricesAndLocales(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,source_locale,raw,raw_hash) VALUES(701,'link','known','known','','Known','1','1','en-US','{}','fixture'),(702,'link','unknown','unknown','','Unknown','1','1','en-US','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO link_apps(package_id,homepage,pricing,notes) VALUES(701,'https://example.com','free','{"en-US":"English","de-DE":"Deutsch"}'),(702,'https://example.com','unknown','{}')`).Error)
	svc := &Service{Store: st}
	catalog, err := svc.Links(ctx)
	require.NoError(t, err)
	require.Len(t, catalog.Items, 1)
	require.Equal(t, "known", catalog.Items[0].Token)
	raw, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "de-DE")
	require.Contains(t, string(raw), "English")
	// Compatibility projection must not delete website data.
	var notes string
	require.NoError(t, st.DB.Raw("SELECT notes->>'de-DE' FROM link_apps WHERE package_id=701").Scan(&notes).Error)
	require.Equal(t, "Deutsch", notes)
}
