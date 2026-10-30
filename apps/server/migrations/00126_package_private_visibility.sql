-- +goose Up
ALTER TABLE package_meta
    ADD COLUMN private boolean NOT NULL DEFAULT false,
    ADD COLUMN visibility_reason text NOT NULL DEFAULT '';

UPDATE package_meta
SET visibility_reason = 'Legacy setting; reason not recorded.'
WHERE hidden = true AND btrim(visibility_reason) = '';

ALTER TABLE package_meta ADD CONSTRAINT package_private_reason
    CHECK (NOT private OR btrim(visibility_reason) <> '');

CREATE INDEX package_meta_private ON package_meta(package_id) WHERE private;

-- +goose Down
ALTER TABLE package_meta
    DROP CONSTRAINT package_private_reason,
    DROP COLUMN visibility_reason,
    DROP COLUMN private;
