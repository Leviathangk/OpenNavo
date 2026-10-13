-- +goose Up
-- Former tokens of an app (WP2). Each alias points at the package row, so chains of renames resolve
-- in one step; a live package that holds the same token always wins over an alias.
CREATE TABLE package_aliases (
  kind       TEXT NOT NULL CHECK (kind IN ('cask','formula')),
  token      TEXT NOT NULL,
  package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
  source     TEXT NOT NULL CHECK (source IN ('renamed','homebrew')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (kind, token)
);
CREATE INDEX package_aliases_package ON package_aliases (package_id);

-- Renames OpenNavo observed, newest first per token.
INSERT INTO package_aliases(kind,token,package_id,source,created_at)
SELECT DISTINCT ON (c.kind, c.previous_token) c.kind, c.previous_token, c.package_id, 'renamed', c.created_at
FROM catalog_changes c JOIN packages p ON p.id=c.package_id
WHERE c.event_type='renamed' AND c.previous_token IS NOT NULL AND c.previous_token<>p.token
ORDER BY c.kind, c.previous_token, c.seq DESC
ON CONFLICT DO NOTHING;

-- Former tokens Homebrew declares for casks.
INSERT INTO package_aliases(kind,token,package_id,source)
SELECT p.kind, t.token, p.id, 'homebrew'
FROM packages p CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(p.raw->'old_tokens','[]'::jsonb)) AS t(token)
WHERE p.kind='cask' AND t.token<>p.token
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE package_aliases;
