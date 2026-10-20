package urlnotify

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/store"
)

// The historical backfill now produces durable work; its marker means queued, not delivered.
func (n *Notifier) backfillPlan(ctx context.Context) (Plan, error) {
	rows, err := n.Store.PausedIndexRows(ctx)
	if err != nil {
		return Plan{}, err
	}
	policy, err := n.Store.IndexPolicy(ctx)
	if err != nil {
		return Plan{}, err
	}
	if err := n.Store.PopulatePageNotes(ctx, rows, 1); err != nil {
		return Plan{}, err
	}
	if err := n.Store.PopulateIntentEligibility(ctx, rows, policy, n.Config.WebBaseURL); err != nil {
		return Plan{}, err
	}
	changes := []store.URLChange{}
	current := map[int64]store.URLChange{}
	index := map[int64]store.IndexRow{}
	for _, row := range rows {
		change := store.URLChange{PackageID: row.ID, Kind: row.Kind, Token: row.Token, Op: "upsert", Reason: "index"}
		changes = append(changes, change)
		current[row.ID] = change
		index[row.ID] = row
	}
	return BuildPlan(changes, current, index, policy, n.Config.WebBaseURL), nil
}
