-- +goose Up
CREATE TABLE collection_setup_criteria (
 collection_id BIGINT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
 criteria TEXT NOT NULL CHECK(length(criteria) BETWEEN 1 AND 10000),
 evidence JSONB NOT NULL,
 PRIMARY KEY(collection_id,locale)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE collection_setup_criteria IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM collection_setup_criteria) THEN
  RAISE EXCEPTION 'Archive and explicitly resolve collection_setup_criteria evidence before Down 00037';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE collection_setup_criteria;
