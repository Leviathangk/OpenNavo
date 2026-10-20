-- +goose Up
-- Dashboard audit alerts: unresolved first, then the latest observation and ID.
CREATE INDEX seo_audit_alert_order ON seo_audit_issues
 ((resolved_at IS NULL) DESC,last_seen DESC,id DESC);

-- +goose Down
DROP INDEX seo_audit_alert_order;
