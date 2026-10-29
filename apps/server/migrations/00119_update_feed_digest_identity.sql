-- +goose Up
-- An unsigned publisher can provide authenticated release metadata instead of
-- a Team ID. Resolvers and clients still require a digest and client consent.
ALTER TABLE update_feeds DROP CONSTRAINT update_feeds_expected_team;
ALTER TABLE update_feeds ADD CONSTRAINT update_feeds_vendor_verification CHECK (
  source <> 'vendor' OR team_id IS NOT NULL OR COALESCE(
    resolver->>'mode' = 'electron'
    OR resolver->'checksumForUrl' = 'true'::jsonb
    OR NULLIF(resolver->>'sha256Path', '') IS NOT NULL
    OR NULLIF(resolver->>'sha256Pattern', '') IS NOT NULL,
    FALSE
  )
);

-- +goose Down
-- Refuse rollback if unsigned rules now exist; never silently delete rules.
ALTER TABLE update_feeds ADD CONSTRAINT update_feeds_expected_team CHECK (source NOT IN ('github','vendor') OR team_id IS NOT NULL);
ALTER TABLE update_feeds DROP CONSTRAINT update_feeds_vendor_verification;
