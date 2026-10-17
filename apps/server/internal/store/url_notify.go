package store

import (
	"context"
	"encoding/json"
	"time"
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
	Cursor               int64
	ContentCursor        int64
	LastRunAt            *time.Time
	LastURLs             int
	LastIndexnowStatus   string
	LastPurgeStatus      string
	LastError            string
	PendingPurgePrefixes []string `gorm:"-"`
}

func (s *Store) URLNotifyState(ctx context.Context) (URLNotifyState, error) {
	var row struct {
		URLNotifyState
		PendingPurges string
	}
	err := s.DB.WithContext(ctx).Raw("SELECT cursor,content_cursor,last_run_at,last_urls,last_indexnow_status,last_purge_status,last_error,pending_purges::text FROM url_notify_state WHERE id=true").Scan(&row).Error
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

// IndexRowsByID loads index facts for live casks (removed ones are absent).
func (s *Store) IndexRowsByID(ctx context.Context, ids []int64) ([]IndexRow, error) {
	if len(ids) == 0 {
		return []IndexRow{}, nil
	}
	return s.indexRows(ctx, "p.id IN ?", "", ids)
}

func (s *Store) SaveURLNotifyState(ctx context.Context, state URLNotifyState) error {
	pending, err := json.Marshal(state.PendingPurgePrefixes)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec("UPDATE url_notify_state SET pending_purges=?::jsonb,cursor=?,content_cursor=?,last_run_at=?,last_urls=?,last_indexnow_status=?,last_purge_status=?,last_error=? WHERE id=true",
		string(pending), state.Cursor, state.ContentCursor, state.LastRunAt, state.LastURLs, state.LastIndexnowStatus, state.LastPurgeStatus, state.LastError).Error
}

// ContentURLChange is a batch of changed pages outside the catalog (articles, authors).
type ContentURLChange struct {
	Seq      int64
	Paths    []string
	Announce bool
}

// PendingContentURLChanges reads page changes after cursor that settled before the given time.
func (s *Store) PendingContentURLChanges(ctx context.Context, cursor int64, before time.Time, limit int) ([]ContentURLChange, error) {
	var rows []struct {
		Seq      int64
		Paths    string
		Announce bool
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT seq,to_json(paths)::text AS paths,announce FROM content_url_changes WHERE seq>? AND created_at<=? ORDER BY seq LIMIT ?", cursor, before, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ContentURLChange, 0, len(rows))
	for _, row := range rows {
		change := ContentURLChange{Seq: row.Seq, Announce: row.Announce}
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
