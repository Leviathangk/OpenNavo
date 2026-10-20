-- +goose Up
ALTER TABLE content_url_changes ADD COLUMN purge_sections BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE url_notify_outbox (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 url TEXT NOT NULL,
 path TEXT NOT NULL CHECK (left(path,1)='/'),
 locale TEXT NOT NULL CHECK(locale IN ('en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU','de-DE','fr-FR','it-IT','zh-Hant')),
 reason TEXT NOT NULL CHECK(reason IN ('created','updated','removed','renamed','index','settings')),
 purge_prefix TEXT NOT NULL DEFAULT '',
 announce BOOLEAN NOT NULL DEFAULT false,
 robots TEXT NOT NULL DEFAULT '',
 canonical_path TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','invalidated','probed','announced','dead')),
 revision BIGINT NOT NULL DEFAULT 1,
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 lease_token TEXT,
 lease_until TIMESTAMPTZ,
 last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX url_notify_outbox_live ON url_notify_outbox(url) WHERE state IN ('pending','invalidated','probed');
CREATE INDEX url_notify_outbox_due ON url_notify_outbox(next_attempt_at,id) WHERE state IN ('pending','invalidated','probed');

-- +goose Down
-- Never discard pending delivery obligations during a rollback.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM url_notify_outbox WHERE state<>'announced') THEN
  RAISE EXCEPTION 'Drain or explicitly resolve URL notifications before rolling back';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE url_notify_outbox;
ALTER TABLE content_url_changes DROP COLUMN purge_sections;
