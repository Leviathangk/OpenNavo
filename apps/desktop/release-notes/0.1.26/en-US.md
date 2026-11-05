## New

- Cleanup can remove regenerable caches, logs and abandoned downloads from AI coding tools, including Codex, Claude Code, Cursor, Antigravity, Continue and opencode. Sessions, memories and credentials stay.
- Inside a project you are still using, incremental build files can be removed on their own. That only slows the next build. Rebuilding the whole project stays a separate choice and starts unselected.
- AI history stores stay read-only. Transcript files older than 30 days can be moved to the Trash one at a time. The review shows how many sessions fall within 30, 90, 180 and 365 days, plus the oldest and newest dates.
- Downloaded model weights for Ollama, LM Studio, Hugging Face, Jan and GPT4All can be moved to the Trash. Chats, datasets and local document indexes stay. If that tool is running, its store is left in place. Local MCP cannot select these items.
- Git worktrees and duplicate clones are listed with the space they use. A linked worktree is removed only when Git accepts it, so uncommitted work is refused; the branch and the main repository stay. A duplicate clone moves to the Trash only when it is clean, fully pushed and has no stash. A broken worktree is shown and cannot be removed here. Rows past the list limit are counted, not shown.

## Improvements

- A project with no source edits for 30 days can offer its build output for cleanup. A project with recent edits stays active, so only the smaller incremental build is offered.
- Files from the last 7 days stay read-only. A 7–30 day target is offered unselected. Only a target the activity check found stale starts selected. A folder and the files inside it are not counted twice.

## Fixes

- Each Cursor cache is shown with its own evidence.
- Local model cleanup lists model files only, and only when that model directory exists.
- Removing a worktree checks again that the folder still belongs to that repository.
