package store

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/content"
)

const AdminReleaseFrom = `FROM releases r JOIN packages p ON p.id=r.package_id LEFT JOIN LATERAL (SELECT t.* FROM release_i18n t WHERE t.release_id=r.id AND t.locale IN (@onv_locale,r.source_locale,'en-US') ORDER BY ` + adminLocaleOrder + ` LIMIT 1) i ON TRUE`
const AdminReleaseJSON = `jsonb_build_object('id',r.id,'sourceLocale',r.source_locale,'packageId',r.package_id,'kind',p.kind,'token',p.token,'packageName',p.name,'version',r.version,'buildVersion',(SELECT pv.version FROM package_versions pv WHERE pv.id=r.package_version_id),'title',r.title,'publishedAt',r.published_at,'source',r.source,'sourceUrl',r.source_url,'hidden',r.hidden,'isPrerelease',r.is_prerelease,'translationStatus',coalesce(i.status,CASE WHEN r.body_markdown IS NULL THEN 'skipped' ELSE 'pending' END),'fetchedAt',r.fetched_at)`

func (s *Store) AdminRelease(ctx context.Context, id int64) (json.RawMessage, error) {
	raw, err := s.JSONRow(ctx, "SELECT "+AdminReleaseJSON+`||jsonb_build_object('bodyMarkdown',r.body_markdown,'i18n',coalesce((SELECT jsonb_agg(jsonb_build_object('locale',t.locale,'sourceLocale',t.source_locale,'sourceHash',t.source_hash,'translatedAt',t.translated_at,'title',t.title,'status',t.status,'summary',t.summary,'sections',t.sections,'bodyMarkdown',t.body_markdown,'failReason',t.fail_reason,'stale',t.source_hash IS DISTINCT FROM r.body_hash,'model',t.model,'reviewedBy',u.user_name,'reviewedAt',t.reviewed_at) ORDER BY t.locale) FROM release_i18n t LEFT JOIN admin_users u ON u.id=t.reviewed_by WHERE t.release_id=r.id),'[]'::jsonb)) AS data `+AdminReleaseFrom+" WHERE r.id=?", id)
	if err != nil {
		return nil, err
	}
	return s.withContentStale(ctx, raw, content.Ref{Entity: "release", ID: id})
}
