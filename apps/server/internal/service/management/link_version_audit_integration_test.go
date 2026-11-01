//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Every manual-version write is audited by the generic writer, and the audit after-image is the row
// the write left behind, so the history shows the version and the advanced link revision.
func TestLinkManualVersionIsAudited(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	created, err := svc.Execute(ctx, "AddLinkPackage", Input{Body: map[string]any{"token": "audit-board", "name": "Audit Board", "homepage": "https://vendor.example.test"}})
	require.NoError(t, err)
	id := valueID(created.(map[string]any)["id"])

	type auditRow struct {
		Action   string
		EntityID string
		Before   map[string]any
		After    map[string]any
	}
	audit := func(requestID string) auditRow {
		t.Helper()
		var rows []struct {
			Action, EntityType, EntityID string
			Before, After                []byte
		}
		require.NoError(t, st.DB.Raw("SELECT action,entity_type,entity_id,before,after FROM audit_logs WHERE request_id=?", requestID).Scan(&rows).Error)
		require.Len(t, rows, 1, "one link write records one audit row")
		require.Equal(t, "link_app", rows[0].EntityType)
		row := auditRow{Action: rows[0].Action, EntityID: rows[0].EntityID}
		require.NoError(t, json.Unmarshal(rows[0].Before, &row.Before))
		require.NoError(t, json.Unmarshal(rows[0].After, &row.After))
		return row
	}

	requestID := uuid.NewString()
	_, err = svc.Execute(domain.WithOperation(ctx, &domain.Operation{RequestID: requestID, AgentClientID: 1, AgentTokenID: 2,
		Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}}), "UpdateLinkPackage", Input{ID: id, Body: map[string]any{"version": "1.0.7"}})
	require.NoError(t, err)
	row := audit(requestID)
	require.Equal(t, "UpdateLinkPackage", row.Action)
	require.Equal(t, strconv.FormatInt(id, 10), row.EntityID)
	require.Nil(t, row.Before["version"], "a link without a version audits an empty before-image")
	require.Equal(t, "1.0.7", row.After["version"])
	before, ok := row.Before["revision"].(float64)
	require.True(t, ok)
	after, ok := row.After["revision"].(float64)
	require.True(t, ok)
	require.Equal(t, before+1, after, "the audited after-image carries the advanced link revision")

	// A version the link already carries changes nothing and is not written again.
	requestID = uuid.NewString()
	_, err = svc.Execute(domain.WithOperation(ctx, &domain.Operation{RequestID: requestID, AgentClientID: 1, AgentTokenID: 2,
		Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}}), "UpdateLinkPackage", Input{ID: id, Body: map[string]any{"version": "1.0.7"}})
	require.NoError(t, err)
	row = audit(requestID)
	require.Equal(t, row.Before["revision"], row.After["revision"], "an unchanged version does not bump the revision")
	require.Equal(t, "1.0.7", row.After["version"])

	// A rejected version write leaves neither a row change nor an audit row.
	requestID = uuid.NewString()
	_, err = svc.Execute(domain.WithOperation(ctx, &domain.Operation{RequestID: requestID, AgentClientID: 1, AgentTokenID: 2,
		Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}}), "UpdateLinkPackage", Input{ID: id, Body: map[string]any{"version": "1.0/2"}})
	require.ErrorContains(t, err, "version_invalid")
	var audits int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM audit_logs WHERE request_id=?", requestID).Scan(&audits).Error)
	require.Zero(t, audits)
	app, err := st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "1.0.7", *app.Version)
}

// Two writers that read the same revision and then race: exactly one manual-version write wins, the
// loser gets 1003, and the loser never leaves its version behind. The link write lock and the
// expected-revision check have to hold together for this to pass.
func TestLinkManualVersionConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	created, err := svc.Execute(ctx, "AddLinkPackage", Input{Body: map[string]any{"token": "race-board", "name": "Race Board", "homepage": "https://vendor.example.test"}})
	require.NoError(t, err)
	id := valueID(created.(map[string]any)["id"])
	app, err := st.LinkApp(ctx, id)
	require.NoError(t, err)
	revision := app.Revision

	versions := []string{"2.0.0", "2.0.1"}
	type outcome struct {
		version string
		err     error
	}
	results := make([]outcome, len(versions))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for index, version := range versions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op := &domain.Operation{RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2,
				Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"},
				Execution: &domain.AgentExecution{ExpectedRevisionSet: true, ExpectedRevision: revision}}
			<-start
			_, results[index].err = svc.Execute(domain.WithOperation(ctx, op), "UpdateLinkPackage", Input{ID: id, Body: map[string]any{"version": version}})
			results[index].version = version
		}()
	}
	close(start)
	wg.Wait()

	winners, losers := []string{}, []string{}
	for _, result := range results {
		if result.err == nil {
			winners = append(winners, result.version)
			continue
		}
		var conflict *domain.AppError
		require.ErrorAs(t, result.err, &conflict, "the losing writer is rejected with a conflict")
		require.Equal(t, domain.CodeConflict, conflict.Code)
		losers = append(losers, result.version)
	}
	require.Len(t, winners, 1, "one concurrent writer holds the expected revision")
	require.Len(t, losers, 1)
	app, err = st.LinkApp(ctx, id)
	require.NoError(t, err)
	require.Equal(t, winners[0], *app.Version, "the losing writer never overwrites the winning version")
	require.Equal(t, revision+1, app.Revision)

	var versions8 []string
	require.NoError(t, st.DB.Raw("SELECT version FROM package_versions WHERE package_id=? ORDER BY version", id).Scan(&versions8).Error)
	require.Equal(t, []string{winners[0]}, versions8, "the loser records no package version")
}
