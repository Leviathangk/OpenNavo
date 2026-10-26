package management

import (
	"context"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
)

// changeFeedView is the URL change feed status shared by the settings page and the agent's
// read-only site settings: the last run plus what is still waiting to be announced.
func (s *Service) changeFeedView(ctx context.Context, st *store.Store) (map[string]any, error) {
	feed, err := st.URLNotifyState(ctx)
	if err != nil {
		return nil, err
	}
	queue, err := st.URLNotifyQueue(ctx, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"lastRunAt": timestamp(feed.LastRunAt), "lastUrls": feed.LastURLs, "lastIndexnowStatus": feed.LastIndexnowStatus,
		"lastPurgeStatus": feed.LastPurgeStatus, "lastError": feed.LastError,
		"pendingAnnouncements": queue.PendingAnnouncements, "oldestPendingAt": timestamp(queue.OldestPendingAt),
		"deadAnnouncements": queue.DeadAnnouncements, "probeCooldownUntil": timestamp(queue.ProbeCooldownUntil),
		"overloadedProbes": queue.OverloadedProbes,
	}, nil
}

func timestamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
