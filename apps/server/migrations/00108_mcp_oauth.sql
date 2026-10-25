-- +goose Up
CREATE TABLE mcp_oauth_clients (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 redirect_uris JSONB NOT NULL CHECK(jsonb_typeof(redirect_uris)='array'),
 scopes TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE mcp_oauth_grants (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 client_id TEXT NOT NULL REFERENCES mcp_oauth_clients(id),
 -- Retain authorization history when its human owner is deleted. Missing owners
 -- fail both access-token authorization and refresh-token exchange.
 user_id BIGINT REFERENCES admin_users(id) ON DELETE SET NULL,
 session_version INTEGER NOT NULL,
 agent_client_id BIGINT NOT NULL REFERENCES agent_clients(id),
 scopes TEXT NOT NULL,
 allow_delete BOOLEAN NOT NULL DEFAULT false,
 expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mcp_oauth_grants_user ON mcp_oauth_grants(user_id,id DESC);
ALTER TABLE agent_tokens ADD COLUMN oauth_grant_id BIGINT REFERENCES mcp_oauth_grants(id);
CREATE INDEX agent_tokens_oauth_grant ON agent_tokens(oauth_grant_id) WHERE oauth_grant_id IS NOT NULL;
CREATE TABLE mcp_oauth_codes (
 token_hash TEXT PRIMARY KEY,
 grant_id BIGINT NOT NULL REFERENCES mcp_oauth_grants(id),
 redirect_uri TEXT NOT NULL,
 challenge TEXT NOT NULL,
 resource TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ
);
CREATE TABLE mcp_oauth_refresh_tokens (
 token_hash TEXT PRIMARY KEY,
 grant_id BIGINT NOT NULL REFERENCES mcp_oauth_grants(id),
 resource TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ
);
CREATE INDEX mcp_oauth_refresh_grant ON mcp_oauth_refresh_tokens(grant_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'OAuth grants and credential history cannot be downgraded safely'; END $$;
-- +goose StatementEnd
