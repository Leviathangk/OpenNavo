-- +goose Up
-- telegram@beta kept an auto github_releases source for overtake/TelegramSwift after that
-- repository was removed. The row stayed enabled, so each cask version change enqueued a
-- fetch the scope then rejected (#228). Disable that one source. Homebrew commit history
-- on the same package is untouched, and so is a manual source or any other package that
-- still names the dead repository. The resolver's upsert does not write enabled, so this
-- stays off after the next resolve.
UPDATE changelog_sources AS s
SET enabled = FALSE,
    last_error = 'github repository overtake/TelegramSwift is gone',
    updated_at = now()
FROM packages AS p
WHERE s.package_id = p.id
  AND p.kind = 'cask'
  AND p.token = 'telegram@beta'
  AND s.type = 'github_releases'
  AND s.resolved_by = 'auto'
  AND s.config->>'repo' = 'overtake/TelegramSwift'
  AND s.enabled;

-- +goose Down
-- Nothing to undo. Up replaces last_error and does not keep the previous value, so a
-- restore would invent a state. Matching the text written here would also re-enable a
-- source an operator later left disabled with that same text. The dead repository stays disabled.
SELECT 1;
