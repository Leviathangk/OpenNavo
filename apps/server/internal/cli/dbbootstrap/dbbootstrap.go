package dbbootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func Main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "database permission setup failed")
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	password := os.Getenv("APP_DATABASE_PASSWORD")
	if len(password) < 16 {
		return errors.New("application database password required")
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return st.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Exec("SELECT set_config('opennavo.application_password',?,true)", password).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Exec(`DO $$ BEGIN
IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='opennavo_app') THEN CREATE ROLE opennavo_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF;
EXECUTE format('ALTER ROLE opennavo_app PASSWORD %L',current_setting('opennavo.application_password'));
END $$;
ALTER ROLE opennavo_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM opennavo_app;
GRANT USAGE ON SCHEMA public TO opennavo_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO opennavo_app;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO opennavo_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO opennavo_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE,SELECT ON SEQUENCES TO opennavo_app;`).Error
	})
}
