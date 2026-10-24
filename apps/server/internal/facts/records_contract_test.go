package facts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestRecordDefaultsMissingExpiryFromCheckedAt(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, expiry := range []string{"", `,"validUntil":null`} {
		var record Record
		require.NoError(t, json.Unmarshal([]byte(`{"kind":"attribute","key":"offline","value":{"name":"offline","booleanValue":true},"evidence":{"status":"verified","sourceUrl":"https://vendor.example/docs","checkedAt":"2026-10-20T12:00:00Z"`+expiry+`}}`), &record))
		require.Equal(t, "unknown", record.Evidence.At(now).Status, "legacy reads do not manufacture validity")
		normalized, err := NormalizeRecord(record, now)
		require.NoError(t, err)
		require.Equal(t, record.Evidence.CheckedAt.Add(30*24*time.Hour), *normalized.Evidence.ValidUntil)
		require.Equal(t, "verified", normalized.Evidence.At(now).Status)
		require.Equal(t, "stale", normalized.Evidence.At(*normalized.Evidence.ValidUntil).Status)
		require.Nil(t, record.Evidence.ValidUntil, "normalization does not mutate its input")
		until := now.Add(time.Hour)
		record.Evidence.ValidUntil = &until
		normalized, err = NormalizeRecord(record, now)
		require.NoError(t, err)
		require.Equal(t, until, *normalized.Evidence.ValidUntil, "explicit expiry is preserved")
	}
}

func TestRecordRejectionsNameRuleWithoutInput(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		edit func(*Record)
	}{
		{"evidence_status_invalid", func(r *Record) { r.Evidence.Status = "private-input" }},
		{"evidence_source_url_invalid", func(r *Record) { r.Evidence.SourceURL = "private-input" }},
		{"evidence_checked_at_invalid", func(r *Record) { r.Evidence.CheckedAt = nil }},
		{"evidence_checked_at_invalid", func(r *Record) { future := now.Add(time.Hour); r.Evidence.CheckedAt = &future }},
		{"evidence_checked_at_invalid", func(r *Record) { old := time.Time{}; r.Evidence.CheckedAt = &old }},
		{"evidence_valid_until_invalid", func(r *Record) { r.Evidence.ValidUntil = &now }},
		{"attribute_value_invalid", func(r *Record) { r.Value = json.RawMessage(`{"name":"private-input"}`) }},
		{"unknown_field", func(r *Record) {
			r.Value = json.RawMessage(`{"name":"offline","booleanValue":true,"private-input":"secret"}`)
		}},
		{"record_kind_invalid", func(r *Record) { r.Kind = "private-input" }},
		{"record_key_invalid", func(r *Record) { r.Key = " private-input" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := Record{Kind: "attribute", Key: "offline", Value: json.RawMessage(`{"name":"offline","booleanValue":true}`), Evidence: NewProof("https://vendor.example/docs", now, time.Hour, "verified", now)}
			test.edit(&r)
			_, err := NormalizeRecord(r, now)
			require.ErrorIs(t, err, ErrInvalid)
			var app *domain.AppError
			require.ErrorAs(t, err, &app)
			require.Equal(t, domain.CodeValidation, app.Code)
			require.Equal(t, "rejected: "+test.name, app.Msg)
			require.NotContains(t, err.Error(), "private-input")
		})
	}
}

func TestMigrationStepDefaultExpiryIsPersistable(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	r := Record{Kind: "migration", Key: "migration", Evidence: Proof{SourceURL: "https://vendor.example/docs", Status: "verified", CheckedAt: &now}, Value: json.RawMessage(`{"targetPackageId":2,"locale":"en-US","steps":[{"action":"backup","text":"Back up documents.","evidence":{"status":"verified","sourceUrl":"https://vendor.example/backup","checkedAt":"2026-10-20T12:00:00Z","validUntil":null}}]}`)}
	normalized, err := NormalizeRecord(r, now)
	require.NoError(t, err)
	var migration Migration
	require.NoError(t, json.Unmarshal(normalized.Value, &migration))
	require.Equal(t, migration.Steps[0].Evidence.CheckedAt.Add(30*24*time.Hour), *migration.Steps[0].Evidence.ValidUntil)
}
