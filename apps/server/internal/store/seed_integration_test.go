//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationsAndSeedAreIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("opennavo_test"), postgres.WithUsername("opennavo"), postgres.WithPassword("isolated-test-only"), postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, sqlDB, "."))
	require.NoError(t, goose.UpContext(ctx, sqlDB, "."))
	st := &store.Store{DB: db, Redis: redis.NewClient(&redis.Options{Addr: "unused"})}
	t.Cleanup(func() { require.NoError(t, st.Redis.Close()) })
	cfg := config.Config{AdminBootstrapUsername: "local-admin", AdminBootstrapPassword: "isolated-test-only", WebBaseURL: "https://web.example.test/"}
	require.NoError(t, seed.Run(ctx, st, cfg))
	var download string
	require.NoError(t, db.WithContext(ctx).Raw("SELECT value #>> '{}' FROM app_config WHERE key='web.downloadUrl'").Scan(&download).Error)
	require.Equal(t, "https://web.example.test/download", download)
	require.NoError(t, db.WithContext(ctx).Exec("UPDATE app_config SET value=?::jsonb WHERE key='web.downloadUrl'", `"https://custom.example.test/download"`).Error)
	var before string
	require.NoError(t, db.WithContext(ctx).Raw("SELECT password_hash FROM admin_users WHERE user_name=?", cfg.AdminBootstrapUsername).Scan(&before).Error)
	require.NoError(t, seed.Run(ctx, st, cfg))
	require.NoError(t, db.WithContext(ctx).Raw("SELECT value #>> '{}' FROM app_config WHERE key='web.downloadUrl'").Scan(&download).Error)
	require.Equal(t, "https://custom.example.test/download", download)
	var after string
	require.NoError(t, db.WithContext(ctx).Raw("SELECT password_hash FROM admin_users WHERE user_name=?", cfg.AdminBootstrapUsername).Scan(&after).Error)
	require.True(t, before == after, "second seed changed the password hash")
	for table, expected := range map[string]int64{"categories": 22, "category_i18n": 44, "admin_roles": 6, "admin_permissions": 33, "mirrors": 4, "app_config": 6, "admin_users": 1, "admin_user_roles": 1} {
		var count int64
		require.NoError(t, db.WithContext(ctx).Table(table).Count(&count).Error)
		require.Equal(t, expected, count, table)
	}
	var agentPermissions int64
	require.NoError(t, db.Raw("SELECT count(*) FROM admin_role_permissions rp JOIN admin_roles r ON r.id=rp.role_id WHERE r.role_code='R_AGENT'").Scan(&agentPermissions).Error)
	require.EqualValues(t, 23, agentPermissions)
	var articlePermissions []string
	require.NoError(t, db.Raw("SELECT rp.permission_code FROM admin_role_permissions rp JOIN admin_roles r ON r.id=rp.role_id WHERE r.role_code='R_AGENT' AND rp.permission_code LIKE 'content:article:%'").Scan(&articlePermissions).Error)
	require.ElementsMatch(t, []string{"content:article:edit", "content:article:publish"}, articlePermissions)
	var super bool
	require.NoError(t, db.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM admin_user_roles ur JOIN admin_roles r ON r.id=ur.role_id WHERE r.role_code='R_SUPER')").Scan(&super).Error)
	require.True(t, super)
	var extension bool
	require.NoError(t, db.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm')").Scan(&extension).Error)
	require.True(t, extension)
}
