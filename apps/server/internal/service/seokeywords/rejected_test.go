package seokeywords

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestKeywordValidationReasons(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		edit   func(*Observation)
		reason string
	}{
		{func(o *Observation) { o.Keyword = " " }, "keyword_invalid:keyword"},
		{func(o *Observation) { o.Language = "private-locale" }, "locale_invalid:language"},
		{func(o *Observation) { o.Source = "private-source" }, "source_invalid:source"},
		{func(o *Observation) { o.Period = "2026-99" }, "period_invalid:period"},
		{func(o *Observation) { o.Country = "U1" }, "country_invalid:country"},
		{func(o *Observation) { o.ObservedAt = now.Add(time.Hour) }, "observed_at_invalid:observedAt"},
		{func(o *Observation) { o.Source = "openseo:dataforseo"; o.Country = "CN" }, "paid_market_unsupported:country"},
	} {
		o := Observation{Keyword: "Fixture Mac utility", Source: "gsc", Engine: "google", Country: "US", Language: "en-US", Device: "desktop", Period: "2026-10", VolumeState: "unknown", ObservedAt: now}
		tc.edit(&o)
		var app *domain.AppError
		require.ErrorAs(t, validate(&o, now), &app)
		require.Equal(t, domain.CodeValidation, app.Code)
		require.Equal(t, "rejected: "+tc.reason, app.Msg)
	}
}

func TestTargetValidationReasons(t *testing.T) {
	svc := &Service{WebURL: "https://example.com"}
	for _, tc := range []struct {
		target Target
		reason string
	}{
		{Target{EntityKey: "private-key", Intent: "download", Locale: "en-US", TargetURL: "https://example.com/apps/fixture"}, "target_invalid"},
		{Target{EntityKey: "topic:fixture", Intent: "topic", Locale: "zh-CN", TargetURL: "https://example.com/topics/fixture"}, "target_path_invalid"},
		{Target{EntityKey: "pkg:99999999999999999999999", Intent: "download", Locale: "en-US", TargetURL: "https://example.com/apps/fixture"}, "target_entity_invalid:entityKey"},
	} {
		var app *domain.AppError
		require.ErrorAs(t, svc.ReserveTargets(context.Background(), nil, []Target{tc.target}), &app)
		require.Equal(t, "rejected: "+tc.reason, app.Msg)
	}
}
