package store

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// URLChange is one catalog_changes row the URL change feed (WP4) turns into page refreshes.
type URLChange struct {
	Seq           int64
	PackageID     int64
	Kind, Token   string
	Op, Reason    string
	EventType     *string
	PreviousToken *string
	CreatedAt     time.Time
}

type URLNotifyState struct {
	Cursor             int64
	ContentCursor      int64
	LastRunAt          *time.Time
	LastURLs           int
	LastIndexnowStatus string
	LastPurgeStatus    string
	LastError          string
	// IndexBackfill names the completed backfill, or "<name>:purging" once its purge queue is durable.
	IndexBackfill        string
	PendingPurgePrefixes []string `gorm:"-"`
}

// URLNotifyQueue summarizes undelivered announcements, so a stalled feed shows up on the
// settings page and to agents instead of waiting silently.
type URLNotifyQueue struct {
	PendingAnnouncements int
	OldestPendingAt      *time.Time
	DeadAnnouncements    int
	ProbeCooldownUntil   *time.Time
	OverloadedProbes     int
}

func (s *Store) URLNotifyQueue(ctx context.Context, now time.Time) (URLNotifyQueue, error) {
	var q URLNotifyQueue
	// Dead receipts count until a newer receipt for the URL exists, for at most 30 days.
	err := s.DB.WithContext(ctx).Raw(`SELECT live.pending_announcements,live.oldest_pending_at,
 (SELECT count(*) FROM url_notify_outbox d WHERE d.state='dead' AND d.announce AND d.updated_at>=?::timestamptz-interval '30 days'
  AND NOT EXISTS (SELECT 1 FROM url_notify_outbox newer WHERE newer.url=d.url AND newer.id>d.id)) AS dead_announcements,
 CASE WHEN st.probe_ready_at>? THEN st.probe_ready_at END AS probe_cooldown_until,
 coalesce(st.probe_failures,0) AS overloaded_probes
 FROM (SELECT count(*) AS pending_announcements,min(created_at) AS oldest_pending_at FROM url_notify_outbox
  WHERE announce AND state IN ('pending','invalidated','probed')) live
 LEFT JOIN url_notify_state st ON st.id=true`, now, now).Scan(&q).Error
	return q, err
}

func (s *Store) URLNotifyState(ctx context.Context) (URLNotifyState, error) {
	var row struct {
		URLNotifyState
		PendingPurges string
	}
	err := s.DB.WithContext(ctx).Raw("SELECT cursor,content_cursor,last_run_at,last_urls,last_indexnow_status,last_purge_status,last_error,index_backfill,pending_purges::text FROM url_notify_state WHERE id=true").Scan(&row).Error
	if err == nil {
		err = json.Unmarshal([]byte(row.PendingPurges), &row.PendingPurgePrefixes)
	}
	return row.URLNotifyState, err
}

// PendingURLChanges reads casks changed after cursor and at least settle ago: renders that started
// before a commit have finished by then, so refreshing their pages cannot leave stale copies behind.
func (s *Store) PendingURLChanges(ctx context.Context, cursor int64, before time.Time, limit int) ([]URLChange, error) {
	var rows []URLChange
	err := s.DB.WithContext(ctx).Raw("SELECT seq,package_id,kind,token,op,reason,event_type,previous_token,created_at FROM catalog_changes WHERE seq>? AND created_at<=? AND kind IN ('cask','link') ORDER BY seq LIMIT ?", cursor, before, limit).Scan(&rows).Error
	return rows, err
}

// URLChangeCursorEnd is the newest row older than before, so a run over non-cask rows still advances.
func (s *Store) URLChangeCursorEnd(ctx context.Context, cursor int64, before time.Time, limit int) (int64, error) {
	var end *int64
	err := s.DB.WithContext(ctx).Raw("SELECT max(seq) FROM (SELECT seq FROM catalog_changes WHERE seq>? AND created_at<=? ORDER BY seq LIMIT ?) r", cursor, before, limit).Scan(&end).Error
	if end == nil {
		return cursor, err
	}
	return *end, err
}

// CurrentPackageTokens returns the current token and removal state of each package.
func (s *Store) CurrentPackageTokens(ctx context.Context, ids []int64) (map[int64]URLChange, error) {
	var rows []struct {
		ID        int64
		Kind      string
		Token     string
		RemovedAt *time.Time
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT id,kind,token,removed_at FROM packages WHERE id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]URLChange, len(rows))
	for _, row := range rows {
		op := "upsert"
		if row.RemovedAt != nil {
			op = "delete"
		}
		out[row.ID] = URLChange{PackageID: row.ID, Kind: row.Kind, Token: row.Token, Op: op}
	}
	return out, nil
}

// PausedIndexRows loads index facts for live apps the 90-day rule paused, in id order; hidden ones
// included, so callers apply the same decision as everywhere else.
func (s *Store) PausedIndexRows(ctx context.Context) ([]IndexRow, error) {
	return s.indexRows(ctx, "p.stale_disabled", "ORDER BY p.id")
}

// IndexRowsByID loads index facts for live casks (removed ones are absent).
func (s *Store) IndexRowsByID(ctx context.Context, ids []int64) ([]IndexRow, error) {
	if len(ids) == 0 {
		return []IndexRow{}, nil
	}
	return s.indexRows(ctx, "p.id IN ?", "", ids)
}

// QueueURLPurges adds edge purge prefixes to the durable queue the feed retries every run without a deadline,
// each prefix once and in order. A queue longer than limit becomes fallback, the long-cached sections, which one
// purge clears instead of that many pages.
func (s *Store) QueueURLPurges(ctx context.Context, prefixes []string, limit int, fallback []string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var raw string
		if err := tx.Raw("SELECT pending_purges::text FROM url_notify_state WHERE id=true FOR UPDATE").Scan(&raw).Error; err != nil {
			return err
		}
		var queue []string
		if err := json.Unmarshal([]byte(raw), &queue); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, prefix := range queue {
			seen[prefix] = true
		}
		for _, prefix := range prefixes {
			if prefix != "" && !seen[prefix] {
				seen[prefix] = true
				queue = append(queue, prefix)
			}
		}
		if len(queue) > limit {
			queue = fallback
		}
		next, err := json.Marshal(queue)
		if err != nil {
			return err
		}
		return tx.Exec("UPDATE url_notify_state SET pending_purges=?::jsonb WHERE id=true", string(next)).Error
	})
}

func (s *Store) SaveURLNotifyState(ctx context.Context, state URLNotifyState) error {
	pending, err := json.Marshal(state.PendingPurgePrefixes)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec("UPDATE url_notify_state SET pending_purges=?::jsonb,cursor=?,content_cursor=?,last_run_at=?,last_urls=?,last_indexnow_status=?,last_purge_status=?,last_error=?,index_backfill=? WHERE id=true",
		string(pending), state.Cursor, state.ContentCursor, state.LastRunAt, state.LastURLs, state.LastIndexnowStatus, state.LastPurgeStatus, state.LastError, state.IndexBackfill).Error
}

// ContentURLChange is a batch of changed pages outside the catalog (articles, authors).
type ContentURLChange struct {
	Seq           int64
	Paths         []string
	Announce      bool
	PurgeSections bool
}

// PendingContentURLChanges reads page changes after cursor that settled before the given time.
func (s *Store) PendingContentURLChanges(ctx context.Context, cursor int64, before time.Time, limit int) ([]ContentURLChange, error) {
	var rows []struct {
		Seq           int64
		Paths         string
		Announce      bool
		PurgeSections bool
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT seq,to_json(paths)::text AS paths,announce,purge_sections FROM content_url_changes WHERE seq>? AND created_at<=? ORDER BY seq LIMIT ?", cursor, before, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ContentURLChange, 0, len(rows))
	for _, row := range rows {
		change := ContentURLChange{Seq: row.Seq, Announce: row.Announce, PurgeSections: row.PurgeSections}
		if err := json.Unmarshal([]byte(row.Paths), &change.Paths); err != nil {
			return nil, err
		}
		out = append(out, change)
	}
	return out, nil
}

// ReserveURLPurge shares a durable request interval between release and content purges.
// Reserve before sending, so failed requests and process restarts also consume allowance.
func (s *Store) ReserveURLPurge(ctx context.Context, now time.Time, interval time.Duration) (bool, error) {
	result := s.DB.WithContext(ctx).Exec("UPDATE url_notify_state SET next_purge_at=? WHERE id=true AND (next_purge_at IS NULL OR next_purge_at<=?)", now.Add(interval), now)
	return result.RowsAffected == 1, result.Error
}

func (s *Store) URLPurgeReadyAt(ctx context.Context) (time.Time, error) {
	var due time.Time
	err := s.DB.WithContext(ctx).Raw("SELECT next_purge_at FROM url_notify_state WHERE id=true").Scan(&due).Error
	return due, err
}

// Account-level throttling may outlive our own interval. Persist the cooldown
// so release purges, other workers and restarts all honor the same deadline.
func (s *Store) DelayURLPurge(ctx context.Context, due time.Time) error {
	return s.DB.WithContext(ctx).Exec("UPDATE url_notify_state SET next_purge_at=greatest(next_purge_at,?) WHERE id=true", due).Error
}

// URLProbeReadyAt persists overload cooldown across notifier restarts.
func (s *Store) URLProbeReadyAt(ctx context.Context) (time.Time, error) {
	var row struct{ Ready time.Time }
	err := s.DB.WithContext(ctx).Raw("SELECT coalesce(probe_ready_at, 'epoch'::timestamptz) AS ready FROM url_notify_state WHERE id=true").Scan(&row).Error
	return row.Ready, err
}

func (s *Store) RecordURLProbe(ctx context.Context, now, retryAt time.Time, overloaded bool) error {
	if !overloaded {
		return s.DB.WithContext(ctx).Exec("UPDATE url_notify_state SET probe_failures=0 WHERE id=true").Error
	}
	return s.DB.WithContext(ctx).Exec(`UPDATE url_notify_state SET
 probe_ready_at=greatest(probe_ready_at, ?, ?::timestamptz + make_interval(secs => 30 * power(2, least(probe_failures, 6))::int)),
 probe_failures=least(probe_failures+1, 7) WHERE id=true`, retryAt, now).Error
}
