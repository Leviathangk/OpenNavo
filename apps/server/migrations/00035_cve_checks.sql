-- +goose Up
CREATE TABLE package_cve_checks (
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 product_cpe TEXT NOT NULL,
 app_version TEXT NOT NULL,
 matches JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(matches)='array'),
 checked_at TIMESTAMPTZ NOT NULL,
 valid_until TIMESTAMPTZ NOT NULL CHECK(valid_until>checked_at),
 PRIMARY KEY(package_id,product_cpe)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE package_cve_checks IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM package_cve_checks) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve package_cve_checks evidence before Down 00035';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE package_cve_checks;
