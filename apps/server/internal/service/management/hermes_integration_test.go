//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestEditorialNotesPublicationDates(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"notes-dates","version":"2.0","name":["Notes Dates"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	var pid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='notes-dates'").Scan(&pid).Error)
	// Historical backfills have a Homebrew commit date but no local observation date.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) VALUES(?,'1.0','1.0','2026-09-23T08:07:03Z')`, pid).Error)
	for _, version := range []string{"1.0", "2.0"} {
		t.Run(version, func(t *testing.T) {
			write := func(extra map[string]any) error {
				t.Helper()
				body := map[string]any{"sourceLocale": "en-US", "summary": "Release notes", "sections": []any{}}
				for key, value := range extra {
					body[key] = value
				}
				_, err := svc.Execute(ctx, "UpsertReleaseNotes", Input{ID: pid, Version: version, Body: body})
				return err
			}
			readDate := func() *time.Time {
				t.Helper()
				var row struct{ PublishedAt *time.Time }
				require.NoError(t, st.DB.Raw("SELECT published_at FROM releases WHERE package_id=? AND version=? AND source='editorial'", pid, version).Scan(&row).Error)
				return row.PublishedAt
			}
			require.NoError(t, write(nil))
			require.Nil(t, readDate(), "neither first observation nor Homebrew adoption is a publication date")
			stamp := "2026-09-22T10:15:00+08:00"
			expected, err := time.Parse(time.RFC3339, stamp)
			require.NoError(t, err)
			require.NoError(t, write(map[string]any{"publishedAt": stamp}))
			require.WithinDuration(t, expected, *readDate(), 0)
			require.NoError(t, write(map[string]any{"summary": "Edited notes"}))
			require.WithinDuration(t, expected, *readDate(), 0, "omission preserves the date")
			for _, invalid := range []any{"0001-01-01T00:00:00Z", "0001-01-01T08:00:00+08:00", "invalid", "", 42} {
				err := write(map[string]any{"publishedAt": invalid})
				var appErr *domain.AppError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, domain.CodeValidation, appErr.Code)
				require.WithinDuration(t, expected, *readDate(), 0, "invalid writes leave the date intact")
			}
			require.NoError(t, write(map[string]any{"publishedAt": nil}))
			require.Nil(t, readDate(), "explicit null clears the date")
			require.NoError(t, write(nil))
			require.Nil(t, readDate(), "an unknown date stays unknown")
		})
	}
}

func TestHermesContentHistoryTransactions(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"hermes-test","version":"1.0","name":["Hermes Test"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='hermes-test'").Scan(&id).Error)
	call := func(op string, in Input) (any, error) { t.Helper(); return svc.Execute(ctx, op, in) }
	body := map[string]any{"sourceLocale": "zh-CN", "displayName": "测试", "summary": "简介", "description": "完整说明"}
	_, err = call("UpdatePackageText", Input{ID: id, Body: body})
	require.NoError(t, err)
	var revision int64
	require.NoError(t, st.DB.Raw("SELECT max(id) FROM content_revisions").Scan(&revision).Error)
	require.Positive(t, revision)
	var revisionLocale string
	require.NoError(t, st.DB.Raw("SELECT locale FROM content_revisions WHERE id=?", revision).Scan(&revisionLocale).Error)
	require.Equal(t, "zh-CN", revisionLocale)
	before, err := st.HistorySnapshot(ctx, content.Ref{Entity: "package", ID: id})
	require.NoError(t, err)
	var audits, outbox int64
	require.NoError(t, st.DB.Table("audit_logs").Count(&audits).Error)
	require.NoError(t, st.DB.Table("job_outbox").Count(&outbox).Error)
	result, err := call("UpdatePackageText", Input{ID: id, Params: map[string]any{"dryrun": true}, Body: map[string]any{"sourceLocale": "zh-CN", "summary": "预览"}})
	require.NoError(t, err)
	require.Equal(t, true, result.(map[string]any)["dryRun"])
	after, err := st.HistorySnapshot(ctx, content.Ref{Entity: "package", ID: id})
	require.NoError(t, err)
	require.Equal(t, before, after)
	var n int64
	require.NoError(t, st.DB.Table("audit_logs").Count(&n).Error)
	require.Equal(t, audits, n)
	require.NoError(t, st.DB.Table("job_outbox").Count(&n).Error)
	require.Equal(t, outbox, n)
	_, err = call("UpdatePackageText", Input{ID: id, Body: map[string]any{"sourceLocale": "zh-CN", "summary": "新简介"}})
	require.NoError(t, err)
	_, err = call("RestoreRevision", Input{ID: revision, Body: map[string]any{}})
	require.NoError(t, err)
	var summary string
	require.NoError(t, st.DB.Raw("SELECT summary FROM package_i18n WHERE package_id=? AND locale='zh-CN'", id).Scan(&summary).Error)
	require.Equal(t, "简介", summary)
	_, err = call("FixTranslation", Input{Body: map[string]any{"ref": map[string]any{"entity": "package", "id": id}, "locale": "en-US", "fields": map[string]any{"displayName": "Test application", "summary": "A short introduction.", "description": "The complete description of this application."}}})
	require.NoError(t, err)
	var translationStatus string
	require.NoError(t, st.DB.Raw("SELECT status FROM package_i18n WHERE package_id=? AND locale='en-US'", id).Scan(&translationStatus).Error)
	require.Equal(t, "manual", translationStatus)
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='translate:content'").Count(&n).Error)
	require.Zero(t, n)
	_, err = call("UpsertReleaseNotes", Input{ID: id, Version: "1.0", Body: map[string]any{"sourceLocale": "zh-CN", "title": "第一版", "summary": "更新内容", "sections": []any{map[string]any{"area": "功能", "items": []any{"新功能"}}}}})
	require.NoError(t, err)
	_, err = call("UpsertReleaseNotes", Input{ID: id, Version: "1.0", Body: map[string]any{"clear": true}})
	require.NoError(t, err)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM releases WHERE source='editorial' AND hidden").Scan(&n).Error)
	require.EqualValues(t, 1, n)
	var clearedRevision int64
	require.NoError(t, st.DB.Raw("SELECT max(id) FROM content_revisions WHERE entity='release'").Scan(&clearedRevision).Error)
	_, err = call("UpsertReleaseNotes", Input{ID: id, Version: "1.0", Body: map[string]any{"sourceLocale": "zh-CN", "summary": "恢复可见"}})
	require.NoError(t, err)
	noDelete := domain.WithOperation(ctx, &domain.Operation{RequestID: uuid.NewString(), Permissions: []string{"*"}, AllowDelete: false})
	_, err = svc.Execute(noDelete, "RestoreRevision", Input{ID: clearedRevision, Body: map[string]any{}})
	var forbidden *domain.AppError
	require.ErrorAs(t, err, &forbidden)
	require.Equal(t, domain.CodeForbidden, forbidden.Code)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM releases WHERE source='editorial' AND NOT hidden").Scan(&n).Error)
	require.EqualValues(t, 1, n)
	_, err = call("UpsertGlossaryTerm", Input{Body: map[string]any{"term": "Hermes", "translations": map[string]any{}, "doNotTranslate": true}})
	require.NoError(t, err)
	var gid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM i18n_glossary WHERE term='Hermes'").Scan(&gid).Error)
	_, err = call("DeleteGlossaryTerm", Input{ID: gid})
	require.NoError(t, err)
	var trash int64
	require.NoError(t, st.DB.Raw("SELECT id FROM content_trash WHERE entity='glossary'").Scan(&trash).Error)
	_, err = call("RestoreTrash", Input{ID: trash})
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("i18n_glossary").Where("id=?", gid).Count(&n).Error)
	require.EqualValues(t, 1, n)
	request := uuid.NewString()
	opctx := domain.WithOperation(ctx, &domain.Operation{RequestID: request, Name: "upsertGlossaryTerm", Permissions: []string{"*"}, RequiredPermissions: []string{"content:glossary:edit"}, AllowDelete: true})
	_, err = svc.Execute(opctx, "UpsertGlossaryTerm", Input{Body: map[string]any{"term": "Undo", "translations": map[string]any{}, "doNotTranslate": true}})
	require.NoError(t, err)
	_, err = call("RevertRequest", Input{RequestID: request})
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("i18n_glossary").Where("term='Undo'").Count(&n).Error)
	require.Zero(t, n)
	_, err = call("RevertRequest", Input{RequestID: request})
	require.Error(t, err)
}

func TestHermesHistoryRetentionAndConflict(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	write := func(request, term string) {
		t.Helper()
		op := &domain.Operation{RequestID: request, Name: "upsertGlossaryTerm", Permissions: []string{"*"}, RequiredPermissions: []string{"content:glossary:edit"}, AllowDelete: true}
		_, err := svc.Execute(domain.WithOperation(ctx, op), "UpsertGlossaryTerm", Input{Body: map[string]any{"term": "retention", "translations": map[string]any{"en-US": term}}})
		require.NoError(t, err)
	}
	first := uuid.NewString()
	write(first, "first")
	write(uuid.NewString(), "later")
	_, err := svc.Execute(ctx, "RevertRequest", Input{RequestID: first})
	require.Error(t, err)
	for range 51 {
		write(uuid.NewString(), uuid.NewString())
	}
	var count int64
	require.NoError(t, st.DB.Table("content_revisions").Where("entity='glossary'").Count(&count).Error)
	require.EqualValues(t, 50, count)
	_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: first})
	require.Error(t, err)
	// If one call changes an object multiple times, undo to before its first change only; partial undo is forbidden.
	request := uuid.NewString()
	write(request, "step one")
	write(request, "step two")
	_, err = svc.Execute(ctx, "RevertRequest", Input{RequestID: request})
	require.NoError(t, err)
}

func TestTrashProtectsAssetAfterRevisionPruning(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	asset, err := st.SaveAsset(ctx, store.Asset{Kind: "cover", StorageKey: "test/retained.png", URL: "https://example.test/retained.png", MIME: "image/png", Bytes: 1, SHA256: strings.Repeat("a", 64)}, nil)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE assets SET created_at=now()-interval '60 days' WHERE id=?", asset.ID).Error)
	result, err := svc.Execute(ctx, "CreateCollection", Input{Body: map[string]any{"slug": "retained", "sort": float64(0), "coverAssetId": float64(asset.ID), "sourceLocale": "zh-CN", "i18n": map[string]any{"zh-CN": map[string]any{"title": "保留资源"}}}})
	require.NoError(t, err)
	id := valueID(result.(map[string]any)["id"])
	require.Positive(t, id)
	_, err = svc.Execute(ctx, "DeleteCollection", Input{ID: id})
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("DELETE FROM content_revisions WHERE entity='collection'").Error)
	removed := 0
	count, err := st.PruneAssets(ctx, time.Now().AddDate(0, 0, -30), func(context.Context, store.Asset) error { removed++; return nil })
	require.NoError(t, err)
	require.Zero(t, count)
	require.Zero(t, removed)
	var trash int64
	require.NoError(t, st.DB.Raw("SELECT id FROM content_trash WHERE entity='collection'").Scan(&trash).Error)
	_, err = svc.Execute(ctx, "RestoreTrash", Input{ID: trash})
	require.NoError(t, err)
	var restoredAsset int64
	require.NoError(t, st.DB.Raw("SELECT cover_asset_id FROM collections WHERE id=?", id).Scan(&restoredAsset).Error)
	require.Equal(t, asset.ID, restoredAsset)
}

func TestHermesCatalogActivityAndListingGaps(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	svc := &Service{Store: st}
	apply := func(version, description string) int64 {
		t.Helper()
		rows, err := st.CatalogRows(ctx, "cask")
		require.NoError(t, err)
		raw, err := json.Marshal(map[string]any{"token": "hermes-events", "version": version, "desc": description, "name": []string{"Hermes Events"}})
		require.NoError(t, err)
		pkg, err := homebrew.Normalize("cask", raw)
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg, Previous: rows["hermes-events"]}}))
		rows, err = st.CatalogRows(ctx, "cask")
		require.NoError(t, err)
		return rows["hermes-events"].ID
	}
	id := apply("1.0", "First")
	apply("1.0", "Metadata only")
	apply("2.0", "New version")
	apply("1.0", "Previously seen version")
	var kinds []string
	require.NoError(t, st.DB.Raw("SELECT event_type FROM catalog_changes WHERE package_id=? AND event_type IS NOT NULL ORDER BY seq", id).Scan(&kinds).Error)
	require.Equal(t, []string{"created", "updated"}, kinds)
	read := func(op string, params map[string]any) map[string]any {
		t.Helper()
		value, err := svc.Execute(ctx, op, Input{Params: params})
		require.NoError(t, err)
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var result map[string]any
		require.NoError(t, json.Unmarshal(raw, &result))
		return result
	}
	require.EqualValues(t, 2, read("ListAdminCatalogChanges", nil)["total"])
	require.EqualValues(t, 2, read("ListAdminVersions", map[string]any{strings.ToLower("packageId"): id})["total"])
	gaps := map[string]any{"gaps": []string{"zhSummary", "latestEditorial"}, "gapmode": "all"}
	require.EqualValues(t, 1, read("ListAdminPackages", gaps)["total"])
	_, err := svc.Execute(ctx, "UpdatePackageText", Input{ID: id, Body: map[string]any{"sourceLocale": "zh-CN", "summary": "事件测试"}})
	require.NoError(t, err)
	require.EqualValues(t, 0, read("ListAdminPackages", gaps)["total"])
	gaps["gapmode"] = "any"
	require.EqualValues(t, 1, read("ListAdminPackages", gaps)["total"])
	_, err = svc.Execute(ctx, "UpsertReleaseNotes", Input{ID: id, Version: "1.0", Body: map[string]any{"sourceLocale": "zh-CN", "summary": "版本说明"}})
	require.NoError(t, err)
	require.EqualValues(t, 0, read("ListAdminPackages", gaps)["total"])
	require.EqualValues(t, 1, read("ListAdminVersions", map[string]any{strings.ToLower("packageId"): id, "haseditorial": true})["total"])
	require.EqualValues(t, 0, read("ListTranslations", map[string]any{"entity": "package", "status": "missing", "stale": true})["total"])
	require.EqualValues(t, 0, read("ListTranslations", map[string]any{"entity": "package", "status": "missing", "q": "no-such-content"})["total"])
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	require.NoError(t, st.RemoveCatalogRows(ctx, []*store.CatalogRow{rows["hermes-events"]}))
	require.EqualValues(t, 1, read("ListAdminCatalogChanges", map[string]any{"type": "removed"})["total"])
}
