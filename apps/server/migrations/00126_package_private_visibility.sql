-- +goose Up
ALTER TABLE package_meta
    ADD COLUMN private boolean NOT NULL DEFAULT false,
    ADD COLUMN visibility_reason text NOT NULL DEFAULT '';

UPDATE package_meta
SET visibility_reason = 'Legacy setting; reason not recorded.'
WHERE hidden = true AND btrim(visibility_reason) = '';

-- Legacy writers, including history replay and link adoption, may set hidden or
-- private without a reason. Fill a default before constraints run so old
-- snapshots and in-flight writers cannot fail or store a blank reason.
-- +goose StatementBegin
CREATE FUNCTION fill_package_visibility_reason() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.hidden OR NEW.private) AND btrim(COALESCE(NEW.visibility_reason,'')) = '' THEN
        NEW.visibility_reason := 'Legacy setting; reason not recorded.';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER package_visibility_reason_default BEFORE INSERT OR UPDATE ON package_meta
    FOR EACH ROW EXECUTE FUNCTION fill_package_visibility_reason();

ALTER TABLE package_meta ADD CONSTRAINT package_private_reason
    CHECK (NOT private OR btrim(visibility_reason) <> '');
ALTER TABLE package_meta ADD CONSTRAINT package_hidden_reason
    CHECK (NOT hidden OR btrim(visibility_reason) <> '');

CREATE INDEX package_meta_private ON package_meta(package_id) WHERE private;

CREATE TABLE catalog_visibility (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    generation bigint NOT NULL DEFAULT 0,
    revocation_generation bigint NOT NULL DEFAULT 0,
    snapshot_revocation bigint NOT NULL DEFAULT 0
);
INSERT INTO catalog_visibility(id) VALUES(true);

-- +goose StatementBegin
CREATE FUNCTION bump_catalog_visibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF OLD.hidden IS NOT DISTINCT FROM NEW.hidden AND OLD.private IS NOT DISTINCT FROM NEW.private THEN
            RETURN NEW;
        END IF;
    ELSIF TG_OP = 'INSERT' THEN
        IF NOT NEW.hidden AND NOT NEW.private THEN RETURN NEW; END IF;
    ELSE
        IF NOT OLD.hidden AND NOT OLD.private THEN RETURN OLD; END IF;
    END IF;
    UPDATE catalog_visibility SET generation=generation+1 WHERE id=true;
    IF TG_OP <> 'DELETE' AND NEW.private AND (TG_OP = 'INSERT' OR NOT OLD.private) THEN
        UPDATE catalog_visibility SET revocation_generation=revocation_generation+1 WHERE id=true;
        INSERT INTO job_outbox(unique_key,job_type,payload) VALUES('snapshot:private','snapshot:build','{}'::jsonb)
            ON CONFLICT(unique_key) DO UPDATE SET available_at=now();
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER package_visibility_generation AFTER INSERT OR UPDATE OR DELETE ON package_meta
    FOR EACH ROW EXECUTE FUNCTION bump_catalog_visibility();

-- +goose Down
DROP TRIGGER package_visibility_reason_default ON package_meta;
DROP FUNCTION fill_package_visibility_reason();
DROP TRIGGER package_visibility_generation ON package_meta;
DROP FUNCTION bump_catalog_visibility();
DROP TABLE catalog_visibility;
ALTER TABLE package_meta
    DROP CONSTRAINT package_private_reason,
    DROP CONSTRAINT package_hidden_reason,
    DROP COLUMN visibility_reason,
    DROP COLUMN private;
