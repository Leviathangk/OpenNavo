//go:build integration

package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
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
	keys := func(info publicapi.CatalogSnapshotInfo) map[string]json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(info.TextPacks)
		require.NoError(t, err)
		var packs map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &packs))
		return packs
	}
	// Clients up to 0.1.13 reject unknown language keys: the metadata they read lists the original six packs.
	legacy := keys(legacyInfo(info(bundle.Record)))
	require.Len(t, legacy, 6)
	require.NotContains(t, legacy, "de-DE")
	// The ten-language client gets every pack; German is one of its languages now, so the German text has a pack.
	require.Len(t, keys(bundle.Info), len(i18n.DesktopLocales))
	require.Contains(t, keys(bundle.Info), "de-DE")
	require.Len(t, bundle.Files, 2+len(i18n.DesktopLocales))
	german := false
	for _, file := range bundle.Files[1 : len(bundle.Files)-1] {
		reader, err := gzip.NewReader(bytes.NewReader(file.Data))
		require.NoError(t, err)
		var pack struct {
			Locale string
			Items  []struct{ Summary *string }
		}
		require.NoError(t, json.NewDecoder(reader).Decode(&pack))
		if pack.Locale == "de-DE" {
			german = true
			require.Len(t, pack.Items, 1)
			require.Equal(t, "Werkzeug", *pack.Items[0].Summary)
		}
	}
	require.True(t, german)
	// Modern clients must discover and download the original German text even in a French UI.
	reader, err := gzip.NewReader(bytes.NewReader(bundle.Files[0].Data))
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var base struct {
		Items      []struct{ SourceLocale string }
		Categories []struct{ SourceLocale string }
	}
	require.NoError(t, json.Unmarshal(raw, &base))
	require.Len(t, base.Items, 1)
	require.Len(t, base.Categories, 1)
	require.Equal(t, "de-DE", base.Items[0].SourceLocale)
	require.Equal(t, "de-DE", base.Categories[0].SourceLocale)
	// The legacy projection remains independently readable and survives metadata persistence.
	reader, err = gzip.NewReader(bytes.NewReader(bundle.Files[len(bundle.Files)-1].Data))
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(reader).Decode(&base))
	require.NoError(t, reader.Close())
	require.Equal(t, "en-US", base.Items[0].SourceLocale)
	require.Equal(t, "en-US", base.Categories[0].SourceLocale)
	require.NoError(t, st.SaveSnapshot(ctx, bundle.Record))
	// A rolled-back service reads only the main row and original manifest key.
	var rollbackRecord store.SnapshotRecord
	require.NoError(t, st.DB.Raw("SELECT * FROM catalog_snapshots WHERE format_version=2 AND storage_key LIKE 'snapshots/base-v2-%' ORDER BY created_at DESC,id DESC LIMIT 1").Scan(&rollbackRecord).Error)
	require.NotZero(t, rollbackRecord.ID)
	var rollbackFile []byte
	for _, file := range bundle.Files {
		if file.Key == rollbackRecord.StorageKey {
			rollbackFile = file.Data
		}
	}
	reader, err = gzip.NewReader(bytes.NewReader(rollbackFile))
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(reader).Decode(&base))
	require.NoError(t, reader.Close())
	require.Equal(t, "en-US", base.Items[0].SourceLocale)
	require.Equal(t, "en-US", base.Categories[0].SourceLocale)
	var row struct{ Value json.RawMessage }
	require.NoError(t, st.DB.Raw("SELECT value FROM app_config WHERE key=?", "catalog.snapshotManifest:"+rollbackRecord.SHA256).Scan(&row).Error)
	var rollbackPacks map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(row.Value, &rollbackPacks))
	require.Len(t, rollbackPacks, 6)
	require.NotContains(t, rollbackPacks, "de-DE")

	old, err := svc.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, bundle.Record.URL, old.Url)
	require.Len(t, keys(old), 6)
	modern, err := svc.Latest(domain.WithAllCatalogLocales(ctx, true))
	require.NoError(t, err)
	require.Equal(t, bundle.Info, modern)
	_, skip, err := svc.bundle(ctx, true)
	require.NoError(t, err)
	require.True(t, skip)
	// Snapshots without modern metadata must be rebuilt.
	require.NoError(t, st.DB.Exec("DELETE FROM app_config WHERE key=?", "catalog.snapshotModernBase:"+bundle.Record.SHA256).Error)
	_, skip, err = svc.bundle(ctx, true)
	require.NoError(t, err)
	require.False(t, skip)

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
