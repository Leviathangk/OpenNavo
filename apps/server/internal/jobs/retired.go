package jobs

import (
	"context"

	"github.com/hibiken/asynq"
)

// Retired jobs are acknowledged and discarded only; do not parse old payloads, access business ledgers or call the LLM.
// Match enrich by prefix to cover historical subtypes; match others explicitly so unknown new jobs are not swallowed.
func RegisterRetiredHandlers(mux *asynq.ServeMux) {
	for _, name := range []string{"enrich:", "translate:release", "translate:schedule", "assets:icons", "seo:gsc", "seo:inspect", "seo:bing", "seo:audit", "seo:opportunities", "seo:evaluate", "seo:experiments"} {
		mux.HandleFunc(name, func(ctx context.Context, task *asynq.Task) error {
			if name != "enrich:" && task.Type() != name {
				return asynq.NotFound(ctx, task)
			}
			return nil
		})
	}
}
