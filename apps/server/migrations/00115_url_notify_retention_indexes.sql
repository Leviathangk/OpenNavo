-- +goose NO TRANSACTION
-- +goose Up
-- Concurrent builds keep notifications writable while historical receipts are indexed.
-- Drop/rebuild makes a retry recover from an interrupted, invalid concurrent index.
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_url_history;
CREATE INDEX CONCURRENTLY url_notify_outbox_url_history ON url_notify_outbox(url,id);
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_settings_history;
CREATE INDEX CONCURRENTLY url_notify_outbox_settings_history ON url_notify_outbox(id) WHERE path='/';
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_terminal;
CREATE INDEX CONCURRENTLY url_notify_outbox_terminal ON url_notify_outbox(updated_at,id) WHERE state IN ('announced','dead');

DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_dead;
CREATE INDEX CONCURRENTLY url_notify_outbox_dead ON url_notify_outbox(url,id) WHERE state='dead';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_dead;
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_terminal;
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_settings_history;
DROP INDEX CONCURRENTLY IF EXISTS url_notify_outbox_url_history;
