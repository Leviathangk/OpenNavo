package management

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestReleaseNotesRejectWikitextBeforeManagementWrites(t *testing.T) {
	svc := &Service{} // Invalid notes must be rejected before a database is needed.
	for _, dryRun := range []bool{false, true} {
		ctx := domain.WithOperation(context.Background(), &domain.Operation{DryRun: dryRun})
		for _, fields := range []map[string]any{
			{"bodyMarkdown": "{{TopMenu}}"},
			{"summary": "[[Category:Release]]"},
			{"sections": []any{map[string]any{"area": "Fixes", "items": []any{"# {{tdf|123}}"}}}},
		} {
			in := Input{ID: 42, Version: "24.8", Locale: "en-US", Params: map[string]any{"dryrun": dryRun}, Body: fields}
			_, err := svc.upsertNotes(ctx, in)
			require.ErrorContains(t, err, "release_notes_wikitext_convert_to_markdown")
			for _, op := range []string{"UpdateReleaseI18n", "UpdateRelease"} {
				_, err = svc.releaseWrite(ctx, op, in)
				require.ErrorContains(t, err, "release_notes_wikitext_convert_to_markdown")
			}
			_, err = svc.writeTranslationLocaleOnce(ctx, translationBatch{Ref: content.Ref{Entity: "release", ID: 42}}, translationItem{Locale: "de-DE", Fields: fields})
			require.ErrorContains(t, err, "release_notes_wikitext_convert_to_markdown")
		}
	}
}
