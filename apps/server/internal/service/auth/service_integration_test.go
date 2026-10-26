//go:build integration

package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationLockRotationReplayStatusVersionAuditAndProfile(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	hash, err := seed.HashPassword("test-password-strong")
	require.NoError(t, err)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "tester", hash))
	svc, err := New(st, config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: 2 * time.Hour, JWTRefreshTTL: 7 * 24 * time.Hour})
	require.NoError(t, err)
	now := time.Now().UTC()
	svc.Now = func() time.Time { return now }
	actor := store.AuditActor{IP: "192.0.2.1", UserAgent: "test"}
	_, err = svc.Login(ctx, "missing", "wrong", actor)
	require.ErrorContains(t, err, domain.CodeInvalidCredentials)
	for range 5 {
		_, err = svc.Login(ctx, "tester", "wrong", actor)
		require.ErrorContains(t, err, domain.CodeInvalidCredentials)
	}
	_, err = svc.Login(ctx, "tester", "test-password-strong", actor)
	require.ErrorContains(t, err, domain.CodeAccountLocked)
	now = now.Add(16 * time.Minute)
	pair, err := svc.Login(ctx, "tester", "test-password-strong", actor)
	require.NoError(t, err)
	id, err := svc.Authenticate(ctx, pair.Token)
	require.NoError(t, err)
	actor.ID = &id.ID
	require.True(t, id.Allowed("system:user:edit"))
	require.Len(t, id.Permissions.Buttons, 33)
	require.Contains(t, id.Permissions.Buttons, "agent:manage")
	require.Contains(t, id.Permissions.Buttons, "site:index:view")
	require.NotContains(t, id.Permissions.Buttons, "seo:task:handle")
	require.NotContains(t, id.Permissions.Buttons, "changelog:source:edit")
	require.NotContains(t, id.Permissions.Buttons, "changelog:fetch")
	require.Equal(t, "tester", id.UserInfo().UserName)
	var storedHash string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT token_hash FROM admin_refresh_tokens WHERE id=?", id.RefreshID).Scan(&storedHash).Error)
	require.Equal(t, hashToken(pair.RefreshToken), storedHash)
	require.NotEqual(t, pair.RefreshToken, storedHash)
	rotated, err := svc.Refresh(ctx, pair.RefreshToken, actor)
	require.NoError(t, err)
	require.NotEqual(t, pair.RefreshToken, rotated.RefreshToken)
	_, err = svc.Authenticate(ctx, rotated.Token)
	require.NoError(t, err)
	_, err = svc.Refresh(ctx, pair.RefreshToken, actor)
	require.ErrorContains(t, err, domain.CodeSessionInvalid)
	_, err = svc.Refresh(ctx, rotated.RefreshToken, actor)
	require.ErrorContains(t, err, domain.CodeSessionInvalid)
	_, err = svc.Authenticate(ctx, rotated.Token)
	require.ErrorContains(t, err, domain.CodeUnauthenticated)
	pair, err = svc.Login(ctx, "tester", "test-password-strong", actor)
	require.NoError(t, err)
	id, err = svc.Authenticate(ctx, pair.Token)
	require.NoError(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE admin_users SET status='2' WHERE id=?", id.ID).Error)
	_, err = svc.Authenticate(ctx, pair.Token)
	require.ErrorContains(t, err, domain.CodeAccountDisabled)
	_, err = svc.Refresh(ctx, pair.RefreshToken, actor)
	require.ErrorContains(t, err, domain.CodeAccountDisabled)
	_, err = svc.Login(ctx, "tester", "test-password-strong", actor)
	require.ErrorContains(t, err, domain.CodeAccountDisabled)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE admin_users SET status='1',session_version=session_version+1 WHERE id=?", id.ID).Error)
	_, err = svc.Authenticate(ctx, pair.Token)
	require.ErrorContains(t, err, domain.CodePasswordChanged)
	pair, err = svc.Login(ctx, "tester", "test-password-strong", actor)
	require.NoError(t, err)
	id, err = svc.Authenticate(ctx, pair.Token)
	require.NoError(t, err)
	profile, err := svc.Profile(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "tester", profile.UserName)
	nick := "昵称"
	require.NoError(t, svc.UpdateProfile(ctx, id, ProfilePatch{Value: adminapi.ProfileUpdate{NickName: &nick}}, actor))
	profile, err = svc.Profile(ctx, id)
	require.NoError(t, err)
	require.Equal(t, nick, profile.NickName)
	require.ErrorContains(t, svc.ChangePassword(ctx, id, "wrong", "another-test-password", actor), domain.CodeInvalidCredentials)
	require.NoError(t, svc.ChangePassword(ctx, id, "test-password-strong", "another-test-password", actor))
	_, err = svc.Authenticate(ctx, pair.Token)
	require.ErrorContains(t, err, domain.CodePasswordChanged)
	pair, err = svc.Login(ctx, "tester", "another-test-password", actor)
	require.NoError(t, err)
	id, err = svc.Authenticate(ctx, pair.Token)
	require.NoError(t, err)
	require.NoError(t, svc.Logout(ctx, id, actor))
	_, err = svc.Refresh(ctx, pair.RefreshToken, actor)
	require.ErrorContains(t, err, domain.CodeSessionInvalid)
	_, err = svc.Authenticate(ctx, pair.Token)
	require.ErrorContains(t, err, domain.CodeUnauthenticated)
	pair, err = svc.Login(ctx, "tester", "another-test-password", actor)
	require.NoError(t, err)
	now = now.Add(3 * time.Hour)
	_, err = svc.Authenticate(ctx, pair.Token)
	require.ErrorContains(t, err, domain.CodeAccessExpired)
	now = now.Add(8 * 24 * time.Hour)
	_, err = svc.Refresh(ctx, pair.RefreshToken, actor)
	require.ErrorContains(t, err, domain.CodeUnauthenticated)
	_, err = svc.Refresh(ctx, "invalid", actor)
	require.ErrorContains(t, err, domain.CodeUnauthenticated)
	var logs string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT string_agg(before::text||after::text,'') FROM audit_logs").Scan(&logs).Error)
	require.NotContains(t, logs, "password-strong")
	require.NotContains(t, logs, "another-test-password")
	require.NotContains(t, logs, pair.RefreshToken)
	var count int64
	require.NoError(t, st.DB.WithContext(ctx).Table("audit_logs").Count(&count).Error)
	require.Greater(t, count, int64(15))
}
func TestConcurrentRefreshHasOneWinnerAndRevokesReplayFamily(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	hash, err := seed.HashPassword("test-password-strong")
	require.NoError(t, err)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "tester", hash))
	svc, err := New(st, config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: 2 * time.Hour, JWTRefreshTTL: 7 * 24 * time.Hour})
	require.NoError(t, err)
	pair, err := svc.Login(ctx, "tester", "test-password-strong", store.AuditActor{})
	require.NoError(t, err)
	var mu sync.Mutex
	wins := 0
	errs := []error{}
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			_, err := svc.Refresh(ctx, pair.RefreshToken, store.AuditActor{})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				errs = append(errs, err)
			}
		})
	}
	workers.Wait()
	require.Equal(t, 1, wins)
	require.Len(t, errs, 1)
	require.ErrorContains(t, errs[0], domain.CodeSessionInvalid)
	var n int64
	require.NoError(t, st.DB.WithContext(ctx).Table("admin_refresh_tokens").Where("revoked_at IS NULL").Count(&n).Error)
	require.Zero(t, n)
}
