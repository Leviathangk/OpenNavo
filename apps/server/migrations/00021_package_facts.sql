-- +goose Up
-- WP12: evidence-backed price, a manual macOS supplement and directional alternatives for apps.
-- Each fact carries its source URL, verification time, verifier and method, and stays part of the
-- package revision history. Public wording comes from fixed codes translated in the repository, so
-- none of this enters the content translation pipeline; notes are internal.
CREATE TABLE package_facts (
 package_id BIGINT PRIMARY KEY REFERENCES packages(id) ON DELETE CASCADE,
 -- NULL price means unknown; the server never infers "free" from a free download.
 price JSONB CHECK (price IS NULL OR (jsonb_typeof(price)='object'
  AND price->>'type' IN ('free','free_with_iap','paid','subscription','trial')
  AND price->>'status' IN ('verified','needs_review')
  AND price->>'sourceUrl' LIKE 'https://%' AND price ? 'verifiedAt')),
 -- NULL macos means no supplement; Homebrew's requirements stay the default.
 macos JSONB CHECK (macos IS NULL OR (jsonb_typeof(macos)='object'
  AND jsonb_typeof(macos->'ranges')='array' AND jsonb_array_length(macos->'ranges') BETWEEN 1 AND 2
  AND macos->>'status' IN ('verified','needs_review')
  AND macos->>'sourceUrl' LIKE 'https://%' AND macos ? 'verifiedAt')),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_by BIGINT REFERENCES admin_users(id) ON DELETE SET NULL,
 CHECK (price IS NOT NULL OR macos IS NOT NULL)
);

-- Directional: A listing B does not make A an alternative to B. Zero to eight per app.
CREATE TABLE package_alternatives (
 package_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 alternative_id BIGINT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 sort SMALLINT NOT NULL CHECK (sort BETWEEN 0 AND 7),
 reasons TEXT[] NOT NULL CHECK (cardinality(reasons) BETWEEN 1 AND 4),
 limits TEXT[] NOT NULL DEFAULT '{}' CHECK (cardinality(limits) <= 3),
 best_for TEXT,
 note TEXT CHECK (note IS NULL OR length(note) BETWEEN 1 AND 500),
 source TEXT NOT NULL CHECK (source IN ('admin','agent')),
 PRIMARY KEY (package_id, alternative_id),
 CHECK (package_id <> alternative_id)
);
CREATE INDEX package_alternatives_alternative ON package_alternatives(alternative_id);

-- +goose Down
DROP TABLE package_alternatives;
DROP TABLE package_facts;
