package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type testLedger struct {
	status, trigger     string
	stats               map[string]any
	message             *string
	startErr, finishErr error
}

func (l *testLedger) StartJobRun(_ context.Context, _ string, trigger string, _ *int64) (int64, error) {
	l.trigger = trigger
	return 1, l.startErr
}
func (l *testLedger) FinishJobRun(ctx context.Context, _ int64, status string, stats map[string]any, message *string) error {
	if ctx.Err() != nil {
		return errors.New("finish context must survive cancellation")
	}
	l.status, l.stats, l.message = status, stats, message
	return l.finishErr
}

func TestHandlerRecordsEveryOutcomeAndRedactsExternalFailures(t *testing.T) {
	for _, example := range []struct {
		Name                 string
		Handler              Handler
		WantStatus           string
		WantError, SkipRetry bool
	}{
		{"success", func(_ context.Context, p Payload) (map[string]any, error) {
			require.Equal(t, int64(7), p.PackageID)
			return map[string]any{"updated": 1}, nil
		}, "succeeded", false, false},
		{"nil_stats", func(context.Context, Payload) (map[string]any, error) { return nil, nil }, "succeeded", false, false},
		{"failure", func(context.Context, Payload) (map[string]any, error) {
			return nil, errors.New("sensitive upstream response")
		}, "failed", true, false},
		{"missing", nil, "failed", true, true},
		{"panic", func(context.Context, Payload) (map[string]any, error) { panic("sensitive panic") }, "failed", true, false},
	} {
		t.Run(example.Name, func(t *testing.T) {
			ledger := &testLedger{}
			err := WrapHandler(ledger, nil, example.Handler)(context.Background(), asynq.NewTask("changelog:resolve", []byte(`{"packageId":7}`)))
			if example.WantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "sensitive")
				require.NotContains(t, *ledger.message, "sensitive")
			} else {
				require.NoError(t, err)
				require.Nil(t, ledger.message)
			}
			require.Equal(t, example.SkipRetry, errors.Is(err, asynq.SkipRetry))
			require.Equal(t, example.WantStatus, ledger.status)
			require.Equal(t, "schedule", ledger.trigger)
			require.Contains(t, ledger.stats, "durationMs")
		})
	}
	ledger := &testLedger{}
	err := WrapHandler(ledger, nil, nil)(context.Background(), asynq.NewTask("bad", []byte("bad")))
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.Empty(t, ledger.status)
	for _, ledger := range []*testLedger{{startErr: errors.New("start failed")}, {finishErr: errors.New("finish failed")}} {
		err := WrapHandler(ledger, nil, func(context.Context, Payload) (map[string]any, error) { return nil, nil })(context.Background(), asynq.NewTask("catalog:sync", []byte(`{}`)))
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = WrapHandler(&testLedger{}, nil, func(context.Context, Payload) (map[string]any, error) { cancel(); return nil, context.Canceled })(ctx, asynq.NewTask("catalog:sync", []byte(`{}`)))
	require.Error(t, err)
}

func TestDefinitionsSchedulesAndRetryOptions(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	now := time.Date(2026, 10, 5, 2, 53, 0, 0, location)
	expected := map[string]time.Time{
		"update-feeds:verify": time.Date(2026, 10, 6, 2, 15, 0, 0, location),
		"catalog:sync":        time.Date(2026, 10, 5, 3, 0, 0, 0, location), "analytics:sync": time.Date(2026, 10, 5, 3, 0, 0, 0, location),
		"snapshot:build":       time.Date(2026, 10, 5, 3, 5, 0, 0, location),
		"changelog:schedule":   time.Date(2026, 10, 5, 3, 0, 0, 0, location),
		"assets:download-size": time.Date(2026, 10, 5, 5, 0, 0, 0, location), "cleanup": time.Date(2026, 10, 5, 4, 0, 0, 0, location),
		"facts:sources": time.Date(2026, 10, 5, 3, 35, 0, 0, location),
		"links:sync":    time.Date(2026, 10, 5, 3, 40, 0, 0, location),
		"taps:sync":     time.Date(2026, 10, 5, 3, 20, 0, 0, location),
	}
	require.Len(t, Definitions, 20)
	var types []string
	for _, definition := range Definitions {
		types = append(types, definition.Type)
	}
	require.ElementsMatch(t, []string{"catalog:sync", "analytics:sync", "snapshot:build", "changelog:schedule", "changelog:resolve", "changelog:fetch", "assets:download-size", "cleanup", "facts:sources", "taps:sync", "links:sync", "update-feeds:verify", "seo:gsc", "seo:inspect", "seo:bing", "seo:audit", "seo:pages", "seo:experiments", "seo:opportunities", "seo:evaluate"}, types)
	for taskType, want := range expected {
		next, err := NextSchedule(taskType, now, location)
		require.NoError(t, err)
		require.Equal(t, want, next)
	}
	for _, definition := range Definitions {
		payload := Payload{}
		if definition.Cron == "" {
			switch definition.Ref {
			case "content":
				payload.Content = &content.Ref{Entity: "package", ID: 7}
				payload.SourceHash = strings.Repeat("a", 64)
			case "package":
				payload.PackageID = 7
			case "release":
				payload.ReleaseID = 7
			case "source":
				payload.SourceID = 7
			}
		}
		task, options, err := NewTask(definition.Type, payload)
		require.NoError(t, err)
		require.Equal(t, definition.Type, task.Type())
		values := map[asynq.OptionType]any{}
		for _, option := range options {
			values[option.Type()] = option.Value()
		}
		require.Equal(t, definition.Queue, values[asynq.QueueOpt])
		require.Equal(t, definition.Retries, values[asynq.MaxRetryOpt])
		require.Equal(t, uniqueTTL(definition), values[asynq.UniqueOpt])
		if definition.Cron == "" {
			require.Contains(t, definition.Key(payload), ":7")
		} else {
			require.Equal(t, definition.Type, definition.Key(payload))
		}
	}
	require.Equal(t, 12, MainConfig().Concurrency)
	require.Equal(t, map[string]int{"critical": 6, "default": 3}, MainConfig().Queues)
	require.Equal(t, 4, FetchConfig().Concurrency)
	require.Equal(t, map[string]int{"changelog": 1}, FetchConfig().Queues)
	for _, example := range []struct {
		Type    string
		Payload Payload
	}{{"bad", Payload{}}, {"changelog:resolve", Payload{}}, {"changelog:fetch", Payload{SourceID: -1}}, {"catalog:sync", Payload{PackageID: 1}}} {
		_, _, err := NewTask(example.Type, example.Payload)
		require.Error(t, err)
	}
	_, err = NextSchedule("changelog:resolve", now, location)
	require.Error(t, err)
	_, err = NextSchedule("bad", now, location)
	require.Error(t, err)
}
