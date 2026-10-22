-- +goose Up
ALTER TABLE article_revisions ADD COLUMN editorial_reviewed_at TIMESTAMPTZ;
-- Only the validators no longer applied to package/release translations, on the
-- current source (rule names as in llm.ContentViolation and agentgate). Preserve
-- manual quarantines, completed work, obsolete snapshots and unrelated task kinds.
UPDATE seo_tasks t SET state='open',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,
 fencing=fencing+1,result=jsonb_build_object('reason','catalog_validator_removed','previous',t.result),updated_at=now()
WHERE t.kind='translate' AND t.state IN ('quarantined','skipped')
 AND (t.expires_at IS NULL OR t.expires_at>now())
 AND EXISTS(SELECT 1 FROM content_translation_sources s WHERE s.entity IN ('package','release')
   AND s.entity||':'||s.object_key=t.entity_key AND s.source_hash=t.evidence_hash)
 AND coalesce(t.result->>'reason','') ~ '(changed_literal|changed_brand|changed_structure|length_ratio|missing_glossary|untranslated|too_long|wrong_language|test_artifact_required|non_mac_section|version_mismatch)';

-- +goose Down
ALTER TABLE article_revisions DROP COLUMN editorial_reviewed_at;
-- Recovered tasks may already have run; never rewind their state.
