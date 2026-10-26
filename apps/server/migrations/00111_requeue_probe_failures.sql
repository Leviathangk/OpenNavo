-- +goose Up
-- The public probe compared robots as one string, so every announced article failed: indexable
-- article pages carry preview controls (max-image-preview:large, ...). The web's load shedding
-- also answered low-priority probes with 503, and each of those spent a retry. Neither was a
-- delivery failure. The worker now compares indexing intent and waits out load shedding, so give
-- the affected receipts a fresh budget. Other errors, completed work and active leases stay as
-- they are.

UPDATE url_notify_outbox live SET attempts=0,next_attempt_at=now(),last_error='',updated_at=now()
WHERE live.state='invalidated' AND live.announce AND live.attempts>0
AND (live.lease_until IS NULL OR live.lease_until<=now())
AND (live.last_error LIKE 'public canonical or robots differs from page decision%'
 OR live.last_error IN ('public page probe returned 503','public page probe returned 429'));

-- A dead receipt restarts from pending, like 00103, so its pages are refreshed before the probe.
UPDATE url_notify_outbox old SET state='pending',attempts=0,next_attempt_at=now(),
 lease_token=NULL,lease_until=NULL,last_error='',updated_at=now()
WHERE old.state='dead' AND old.announce
AND (old.last_error LIKE 'public canonical or robots differs from page decision%'
 OR old.last_error IN ('public page probe returned 503','public page probe returned 429'))
AND NOT EXISTS (SELECT 1 FROM url_notify_outbox active WHERE active.url=old.url
 AND active.state IN ('pending','invalidated','probed'))
AND NOT EXISTS (SELECT 1 FROM url_notify_outbox newer WHERE newer.url=old.url AND newer.id>old.id);

-- +goose Down
-- Recovered receipts are not reverted; a retry budget carries no schema.
SELECT 1;
