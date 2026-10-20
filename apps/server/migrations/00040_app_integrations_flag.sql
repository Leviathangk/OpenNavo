-- +goose Up
INSERT INTO app_config(key,value,description) VALUES('web.appIntegrationsEnabled','false','Show verified MCP and Skill facts on existing App pages') ON CONFLICT(key) DO NOTHING;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 LOCK TABLE app_config IN SHARE ROW EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM app_config WHERE key='web.appIntegrationsEnabled' AND value IS DISTINCT FROM 'false'::jsonb) THEN
  RAISE EXCEPTION 'Explicitly disable App integrations before Down 00040';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM app_config WHERE key='web.appIntegrationsEnabled';
