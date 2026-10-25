//go:build integration

package urlnotify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestRefreshOnlyBacklogCompletesWithoutExtraPhases(t *testing.T) {
	st := testutil.NewSEOStore(t)
	var fail atomic.Bool
	fail.Store(true)
	var unexpected atomic.Int32
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cache/invalidate" || r.Method != http.MethodPost {
			unexpected.Add(1)
			w.WriteHeader(500)
			return
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"removed":1}`))
	}))
	defer web.Close()
	require.NoError(t, st.DB.Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,announce)
 SELECT 'https://site.example/apps/fixture-'||n,'/apps/fixture-'||n,'en-US','updated',false FROM generate_series(1,250) n`).Error)
	now := time.Now().UTC().Add(time.Minute)
	n := &Notifier{Store: st, Config: config.Config{WebInternalURL: web.URL, WebInternalSecret: "fixture-secret"}, Now: func() time.Time { return now }}
	n.defaults()
	_, _, err := n.drainOutbox(context.Background(), "fixture-owner")
	require.Error(t, err)
	var completed int64
	require.NoError(t, st.DB.Table("url_notify_outbox").Where("state='announced'").Count(&completed).Error)
	require.Zero(t, completed, "failed invalidations must remain retryable")
	fail.Store(false)
	now = now.Add(10 * time.Minute)
	_, _, err = n.drainOutbox(context.Background(), "fixture-owner")
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("url_notify_outbox").Where("state='announced'").Count(&completed).Error)
	require.EqualValues(t, 250, completed, "all three passes should drain useful work rather than rotate no-op phases")
	require.NoError(t, st.DB.Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,announce,state)
 SELECT 'https://site.example/apps/legacy-'||state,'/apps/legacy-'||state,'en-US','updated',false,state
 FROM unnest(ARRAY['invalidated','probed']) state`).Error)
	now = now.Add(time.Minute)
	_, _, err = n.drainOutbox(context.Background(), "fixture-owner")
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("url_notify_outbox").Where("state='announced'").Count(&completed).Error)
	require.EqualValues(t, 252, completed, "legacy intermediate states are also drained")
	require.Zero(t, unexpected.Load(), "refresh-only jobs never probe or announce")
}
