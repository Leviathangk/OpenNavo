//go:build integration

package management

import (
	"encoding/json"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArticleTranslationReferences(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ctx := hermesContext("translation:review", "content:article:edit", "content:collection:edit")
	// Independent identity sequences deliberately collide: article 31 is not
	// revision 31, which is another article's published content.
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return tx.Exec(`INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at,archived_at) VALUES
 (31,'reference-target','published',402,401,now(),NULL),
 (32,'reference-other','published',31,31,now(),NULL),
 (33,'reference-draft','draft',403,NULL,NULL,NULL),
 (34,'reference-draft-collision','published',33,33,now(),NULL),
 (35,'reference-archived','archived',405,405,now(),now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type,published_at) VALUES
 (400,31,1,'en-US','news',now()), (401,31,2,'en-US','news',now()), (402,31,3,'en-US','news',NULL),
 (31,32,1,'en-US','news',now()), (403,33,1,'en-US','news',NULL),
 (33,34,1,'en-US','news',now()), (405,35,1,'en-US','news',now());
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status)
 SELECT id,'en-US','Reference guide','en-US','source' FROM article_revisions;
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status) VALUES
 (31,'de-DE','Unrelated translation sentinel','en-US','manual'),
 (403,'de-DE','Draft translation sentinel','en-US','manual')`).Error
	}))
	snapshot := func() string {
		var rows string
		require.NoError(t, st.DB.Raw(`SELECT jsonb_agg(to_jsonb(i) ORDER BY revision_id,locale)::text FROM article_revision_i18n i`).Scan(&rows).Error)
		return rows
	}
	before := snapshot()
	articleBody := func(request translationBatch) map[string]any {
		body := hermesMap(request)
		if request.Ref.ID == 0 {
			delete(body["ref"].(map[string]any), "id")
		}
		return body
	}
	for _, tc := range []struct {
		name      string
		id        int64
		articleID *int64
		reason    string
	}{
		{name: "article identity collides with another published revision", id: 31, reason: "article_id_probably_passed"},
		{name: "draft identity collides with another published revision", id: 33, reason: "publish_article_first"},
		{name: "draft revision", id: 403, reason: "publish_article_first"},
		{name: "draft articleId", articleID: int64ptr(33), reason: "article_must_be_published_first"},
		{name: "archived articleId", articleID: int64ptr(35), reason: "article_must_be_published_first"},
		{name: "old revision", id: 400, reason: "article_ref_must_be_published_revision"},
		{name: "working revision", id: 402, reason: "article_ref_must_be_published_revision"},
		{name: "archived revision", id: 405, reason: "publish_article_first"},
		{name: "missing revision", id: 9999, reason: "article_ref_must_be_published_revision"},
		{name: "missing articleId", articleID: int64ptr(9999), reason: "article_not_found"},
		{name: "mismatched articleId and ref.id", id: 31, articleID: int64ptr(31), reason: "article_ref_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"entity": "article"}
			if tc.id != 0 {
				params["id"] = float64(tc.id)
			}
			if tc.articleID != nil {
				params["articleid"] = float64(*tc.articleID)
			}
			read, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: params})
			require.Nil(t, read, "invalid references must not return translation data")
			require.ErrorContains(t, err, tc.reason)
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, domain.CodeValidation, app.Code)
			write, err := svc.Execute(ctx, "WriteTranslations", Input{Body: articleBody(translationBatch{
				Ref: content.Ref{Entity: "article", ID: tc.id}, ArticleID: tc.articleID,
				Translations: []translationItem{{Locale: "de-DE", Fields: map[string]any{"title": "Referenzhandbuch"}}},
			})})
			require.Nil(t, write, "invalid references must not echo a current translation value")
			require.ErrorContains(t, err, tc.reason)
			require.JSONEq(t, before, snapshot(), "no revision may change after rejection")
		})
	}

	t.Run("explicit zero ID does not match articleId", func(t *testing.T) {
		read, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: map[string]any{"entity": "article", "id": float64(0), "articleid": float64(31)}})
		require.ErrorContains(t, err, "article_ref_mismatch")
		require.Nil(t, read)
		write, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(translationBatch{
			Ref: content.Ref{Entity: "article", ID: 0}, ArticleID: int64ptr(31),
			Translations: []translationItem{{Locale: "de-DE", Fields: map[string]any{"title": "Referenzhandbuch"}}},
		})})
		require.ErrorContains(t, err, "article_ref_mismatch")
		require.Nil(t, write)
		require.JSONEq(t, before, snapshot())
	})

	for _, tc := range []struct {
		name      string
		id        int64
		articleID *int64
		resolved  int64
	}{
		{name: "published revision", id: 401, resolved: 401},
		{name: "articleId", articleID: int64ptr(31), resolved: 401},
		{name: "matching articleId and revision", id: 401, articleID: int64ptr(31), resolved: 401},
		{name: "articleId disambiguates collision", id: 31, articleID: int64ptr(32), resolved: 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"entity": "article"}
			if tc.id != 0 {
				params["id"] = float64(tc.id)
			}
			if tc.articleID != nil {
				params["articleid"] = float64(*tc.articleID)
			}
			read, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: params})
			require.NoError(t, err)
			view := hermesMap(read)
			require.EqualValues(t, tc.resolved, view["ref"].(map[string]any)["id"])
			value, err := st.TranslationValue(ctx, content.Ref{Entity: "article", ID: tc.resolved}, "de-DE")
			require.NoError(t, err)
			write, err := svc.Execute(ctx, "WriteTranslations", Input{Body: articleBody(translationBatch{
				Ref: content.Ref{Entity: "article", ID: tc.id}, ArticleID: tc.articleID,
				Translations: []translationItem{{Locale: "de-DE", SourceHash: view["sourceHash"].(string), ExpectedRevision: value.Revision, Fields: map[string]any{"title": "Referenzhandbuch"}}},
			})})
			require.NoError(t, err)
			result := hermesMap(write)["results"].([]any)[0].(map[string]any)
			require.Equal(t, "written", result["status"], result)
			updated, err := st.TranslationValue(ctx, content.Ref{Entity: "article", ID: tc.resolved}, "de-DE")
			require.NoError(t, err)
			require.Equal(t, "Referenzhandbuch", updated.Fields["title"])
			after, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: params})
			require.NoError(t, err)
			require.Contains(t, string(mustJSON(t, after)), "Referenzhandbuch")
		})
	}

	t.Run("failed locale returns its reason without stored values", func(t *testing.T) {
		before := snapshot()
		write, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(translationBatch{
			Ref: content.Ref{Entity: "article", ID: 401}, ArticleID: int64ptr(31),
			Translations: []translationItem{{Locale: "en-US", Fields: map[string]any{"title": "Reference guide"}}},
		})})
		require.NoError(t, err)
		result := hermesMap(write)["results"].([]any)[0].(map[string]any)
		require.Equal(t, "failed", result["status"])
		require.Equal(t, "validation_failed:rejected: translation_locale_is_source", result["reason"])
		require.Nil(t, result["value"])
		require.JSONEq(t, before, snapshot())
	})

	t.Run("publication changes after reference resolution", func(t *testing.T) {
		request := translationBatch{Ref: content.Ref{Entity: "article", ID: 401}, ArticleID: int64ptr(31)}
		require.NoError(t, st.DB.Exec("UPDATE articles SET published_revision_id=402 WHERE id=31").Error)
		before := snapshot()
		_, err := svc.writeTranslationLocale(ctx, request, translationItem{Locale: "de-DE", Fields: map[string]any{"title": "Neue Übersetzung"}})
		require.ErrorContains(t, err, "article_ref_mismatch")
		require.JSONEq(t, before, snapshot())
	})

	t.Run("collection_item retains composite reference", func(t *testing.T) {
		require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(991,'cask','reference-app','reference-app','','Reference App','1','1','{}','fixture');
 INSERT INTO collections(id,slug,source_locale) VALUES(810,'reference-collection','en-US');
 INSERT INTO collection_items(collection_id,package_id,source_locale) VALUES(810,991,'en-US');
 INSERT INTO collection_item_i18n(collection_id,package_id,locale,note,source_locale,status)
 VALUES(810,991,'en-US','Reference note','en-US','source')`).Error)
		params := map[string]any{"entity": "collection_item", "id": float64(810), "secondaryid": float64(991)}
		read, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: params})
		require.NoError(t, err)
		view := hermesMap(read)
		require.EqualValues(t, 991, view["ref"].(map[string]any)["secondaryId"])
		write, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(translationBatch{
			Ref:          content.Ref{Entity: "collection_item", ID: 810, SecondaryID: 991},
			Translations: []translationItem{{Locale: "de-DE", SourceHash: view["sourceHash"].(string), Fields: map[string]any{"note": "Referenznotiz"}}},
		})})
		require.NoError(t, err)
		result := hermesMap(write)["results"].([]any)[0].(map[string]any)
		require.Equal(t, "written", result["status"], result)
		require.Equal(t, "Referenznotiz", result["value"].(map[string]any)["fields"].(map[string]any)["note"])
	})

	t.Run("non-article cannot supply articleId", func(t *testing.T) {
		_, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: map[string]any{"entity": "collection_item", "id": float64(810), "secondaryid": float64(991), "articleid": float64(31)}})
		require.ErrorContains(t, err, "article_id_requires_article")
	})
}

func int64ptr(v int64) *int64 { return &v }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}
