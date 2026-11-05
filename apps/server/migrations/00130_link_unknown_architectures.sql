-- +goose Up
-- Link packages are created without an architecture declaration. The columns default to
-- true, so every link page looked like it supported Apple silicon and Intel. Any remaining
-- true flag without a known supportStatus is that default, not a one-chip declaration.
-- A known supportStatus is a real declaration and is left alone.
UPDATE packages
SET supports_arm64 = FALSE, supports_x86_64 = FALSE, updated_at = now()
WHERE kind = 'link'
  AND (supports_arm64 OR supports_x86_64)
  AND COALESCE(dependencies->>'supportStatus', '') <> 'known';

-- +goose Down
-- Restores the historical both-true default. This cannot recover a one-architecture
-- value, and it also resets link rows created after Up, which are inserted as false/false.
UPDATE packages
SET supports_arm64 = TRUE, supports_x86_64 = TRUE, updated_at = now()
WHERE kind = 'link'
  AND NOT supports_arm64
  AND NOT supports_x86_64
  AND COALESCE(dependencies->>'supportStatus', '') <> 'known';
