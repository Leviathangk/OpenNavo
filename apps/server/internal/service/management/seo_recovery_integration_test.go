//go:build integration

package management

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func agentOperation(name string) *domain.Operation {
	return &domain.Operation{RequestID: uuid.NewString(), Name: name, AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"*"}}
}

func requireRejected(t *testing.T, err error, rule string) {
	t.Helper()
	var app *domain.AppError
	require.True(t, errors.As(err, &app), err)
	require.Equal(t, domain.CodeInvalidState, app.Code)
	require.Equal(t, "rejected: "+rule, app.Msg)
}

// An app a person asked for becomes one listing task the agent can claim by ID.
// Asking again keeps the queued task; a finished one is opened again.
func TestNominatedAppBecomesAListingTask(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	nominate := func(token string, body map[string]any) (any, error) {
		body["idempotencyKey"] = uuid.NewString()
		return svc.Execute(domain.WithOperation(ctx, agentOperation("nominateSeoListing")), "NominateSeoListing", Input{Key: token, Body: body})
	}
	request := func() map[string]any {
		return map[string]any{"name": "SkillDeck", "homepage": "https://skilldeck.example.test", "reason": "The site owner asked for it"}
	}
	result, err := nominate("skilldeck", request())
	require.NoError(t, err)
	task := result.(seotasks.Task)
	require.Equal(t, "listing", task.Kind)
	require.Equal(t, "listing:skilldeck", task.EntityKey)
	require.Equal(t, "open", task.State)
	require.EqualValues(t, 1000, task.Priority)
	require.Contains(t, string(task.Evidence), `"nominatedBy": "agent:1:2"`)

	again, err := nominate("skilldeck", request())
	require.NoError(t, err)
	require.Equal(t, task.ID, again.(seotasks.Task).ID)

	tasks := &seotasks.Service{DB: st.DB}
	claimed, err := tasks.ClaimWith(ctx, seotasks.Owner(1, 2), seotasks.ClaimFilter{Limit: 1, Kinds: []string{"listing"}, IDs: []int64{task.ID}})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = tasks.Complete(ctx, task.ID, claimed[0].Fencing, seotasks.Owner(1, 2), "skipped", "no official page yet")
	require.NoError(t, err)
	reopened, err := nominate("skilldeck", request())
	require.NoError(t, err)
	require.Equal(t, "open", reopened.(seotasks.Task).State)

	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(901,'cask','listed-app','listed-app','homebrew/cask','Listed','1.0','1.0','{}','fixture')`).Error)
	_, err = nominate("listed-app", request())
	requireRejected(t, err, "already_listed")
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_listing_candidates(token,state,payload) VALUES('refused-app','rejected','{}')`).Error)
	_, err = nominate("refused-app", request())
	requireRejected(t, err, "candidate_rejected")
	insecure := request()
	insecure["homepage"] = "http://skilldeck.example.test"
	_, err = nominate("other-app", insecure)
	var app *domain.AppError
	require.True(t, errors.As(err, &app))
	require.Equal(t, domain.CodeValidation, app.Code)
}

func TestReopenThroughManagementNamesTheRule(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	task, err := (&seotasks.Service{DB: st.DB}).Enqueue(ctx, seotasks.NewTask{Kind: "fixture", EntityKey: "pkg:951", DedupeKey: "reopen-management", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	reopen := func() (any, error) {
		return svc.Execute(domain.WithOperation(ctx, agentOperation("reopenSeoTask")), "ReopenSeoTask", Input{ID: task.ID, Body: map[string]any{"idempotencyKey": uuid.NewString(), "reason": "fixed"}})
	}
	_, err = reopen()
	requireRejected(t, err, "not_reopenable")
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state='quarantined' WHERE id=?", task.ID).Error)
	result, err := reopen()
	require.NoError(t, err)
	require.Equal(t, "open", result.(seotasks.Task).State)
	require.Equal(t, 1, result.(seotasks.Task).ReopenCount)
}
