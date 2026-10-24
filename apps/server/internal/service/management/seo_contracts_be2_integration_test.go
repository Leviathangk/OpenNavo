//go:build integration

package management

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func be2Context(name string) context.Context {
	return domain.WithOperation(context.Background(), &domain.Operation{
		Name: name, RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2,
		Permissions: []string{"*"}, RequiredPermissions: []string{historyPermission(name)},
	})
}

func TestBE2PartialLocalizedWritesPreserveLegacyOverlongLanguage(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO collections(id,slug,source_locale) VALUES(901,'legacy-collection','en-US');
INSERT INTO categories(id,slug,icon,source_locale) VALUES(901,'legacy-category','lucide:box','en-US');
INSERT INTO features(id,placement,target_type,url,source_locale) VALUES(901,'home_hero','url','https://vendor.example/','en-US')`).Error)
	svc := &Service{Store: st}
	for _, test := range []struct {
		name, table, key, label, longField string
		body                               map[string]any
	}{
		{"UpdateCollection", "collection_i18n", "collection_id", "title", "subtitle", map[string]any{"slug": "legacy-collection"}},
		{"UpdateCategory", "category_i18n", "category_id", "name", "description", map[string]any{"slug": "legacy-category", "icon": "lucide:box"}},
		{"UpdateFeature", "feature_i18n", "feature_id", "title", "subtitle", map[string]any{"placement": "home_hero", "targetType": "url", "url": "https://vendor.example/", "status": "draft"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			legacy := strings.Repeat("x", 250)
			require.NoError(t, st.DB.Exec("INSERT INTO "+test.table+"("+test.key+",locale,"+test.label+","+test.longField+") VALUES(901,'de-DE','Legacy',?)", legacy).Error)
			source := map[string]any{test.label: "Source"}
			body := test.body
			body["sourceLocale"] = "en-US"
			body["i18n"] = map[string]any{"en-US": source, "ja-JP": map[string]any{test.label: "更新"}}
			_, err := svc.Execute(be2Context(test.name), test.name, Input{ID: 901, Body: hermesMap(body)})
			require.NoError(t, err)
			var untouched, updated string
			require.NoError(t, st.DB.Raw("SELECT "+test.longField+" FROM "+test.table+" WHERE "+test.key+"=901 AND locale='de-DE'").Scan(&untouched).Error)
			require.Equal(t, legacy, untouched)
			require.NoError(t, st.DB.Raw("SELECT "+test.label+" FROM "+test.table+" WHERE "+test.key+"=901 AND locale='ja-JP'").Scan(&updated).Error)
			require.Equal(t, "更新", updated)
			// Replaying the old language reproduces the original rejection.
			body["i18n"].(map[string]any)["de-DE"] = map[string]any{test.label: "Legacy", test.longField: legacy}
			_, err = svc.Execute(be2Context(test.name), test.name, Input{ID: 901, Body: hermesMap(body)})
			require.ErrorContains(t, err, "rejected: field_too_long:")
		})
	}
}

func TestBE2TranslationsRejectLongSubtitlePerLanguage(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO collections(id,slug,source_locale) VALUES(902,'translation-limit','en-US');
INSERT INTO collection_i18n(collection_id,locale,title,subtitle,status,source_locale) VALUES(902,'en-US','Source','Source subtitle','source','en-US')`).Error)
	svc := &Service{Store: st}
	result, err := svc.Execute(be2Context("WriteTranslations"), "WriteTranslations", Input{Body: hermesMap(map[string]any{
		"ref": map[string]any{"entity": "collection", "id": 902},
		"translations": []any{
			map[string]any{"locale": "de-DE", "fields": map[string]any{"title": "Sammlung", "subtitle": strings.Repeat("x", 81)}},
			map[string]any{"locale": "ja-JP", "fields": map[string]any{"title": "合集", "subtitle": strings.Repeat("字", 80)}},
		},
	})})
	require.NoError(t, err)
	rows := result.(map[string]any)["results"].([]any)
	require.Equal(t, "failed", rows[0].(map[string]any)["status"])
	require.Equal(t, "validation_failed:rejected: field_too_long:collection.subtitle.de-DE", rows[0].(map[string]any)["reason"])
	require.Equal(t, "written", rows[1].(map[string]any)["status"])
	var count int64
	require.NoError(t, st.DB.Table("collection_i18n").Where("collection_id=902 AND locale='de-DE'").Count(&count).Error)
	require.Zero(t, count)
	var subtitle string
	require.NoError(t, st.DB.Raw("SELECT subtitle FROM collection_i18n WHERE collection_id=902 AND locale='ja-JP'").Scan(&subtitle).Error)
	require.Equal(t, strings.Repeat("字", 80), subtitle)
}

func TestBE2FactRecordsDefaultNullExpiryAndRejectInvalidEvidence(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
VALUES(903,'cask','expiry-fixture','expiry-fixture','homebrew/cask','Fixture','1','1','{}','fixture')`).Error)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	checked := now.Add(-24 * time.Hour)
	svc := &Service{Store: st, Now: func() time.Time { return now }}
	write := func(key string, evidence map[string]any, value map[string]any) error {
		_, err := svc.Execute(be2Context("SetPackageFactRecords"), "SetPackageFactRecords", Input{ID: 903, Body: hermesMap(map[string]any{"items": []any{map[string]any{
			"kind": "attribute", "key": key, "value": value, "evidence": evidence,
		}}})})
		return err
	}
	proof := map[string]any{"sourceUrl": "https://vendor.example/docs", "checkedAt": checked.Format(time.RFC3339), "validUntil": nil, "status": "verified"}
	value := map[string]any{"name": "offline", "booleanValue": true}
	require.NoError(t, write("null-expiry", proof, value))
	delete(proof, "validUntil")
	require.NoError(t, write("omitted-expiry", proof, value))
	rows, err := st.FactRecords(context.Background(), 903)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, checked.Add(30*24*time.Hour), *row.Evidence.ValidUntil)
	}
	proof["validUntil"] = checked.Format(time.RFC3339)
	require.ErrorContains(t, write("invalid-expiry", proof, value), "rejected: evidence_valid_until_invalid")
	proof["validUntil"] = nil
	proof["checkedAt"] = now.Add(time.Hour).Format(time.RFC3339)
	require.ErrorContains(t, write("future", proof, value), "rejected: evidence_checked_at_invalid")
	proof["checkedAt"] = checked.Format(time.RFC3339)
	require.ErrorContains(t, write("attribute", proof, map[string]any{"name": "offline"}), "rejected: attribute_value_invalid")
	require.ErrorContains(t, write("unknown", proof, map[string]any{"name": "offline", "booleanValue": true, "secret-input": true}), "rejected: unknown_field")
	rows, err = st.FactRecords(context.Background(), 903)
	require.NoError(t, err)
	require.Len(t, rows, 2, "rejected facts never reach persistence")
}
