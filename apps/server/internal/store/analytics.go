package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

func (s *Store) AnalyticsPackages(ctx context.Context) ([]domain.AnalyticsPackage, error) {
	var rows []domain.AnalyticsPackage
	if err := s.DB.WithContext(ctx).Raw("SELECT id,kind,token,full_token,tap FROM packages WHERE kind='cask' AND removed_at IS NULL").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load analytics packages: %w", err)
	}
	return rows, nil
}

func (s *Store) ApplyAnalytics(ctx context.Context, rows []domain.AnalyticsRow, date string) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		for start := 0; start < len(rows); start += 500 {
			groups := []string{}
			arguments := []any{}
			for _, row := range rows[start:min(start+500, len(rows))] {
				groups = append(groups, "(?::bigint,?::integer,?::integer,?::integer,?::integer,?::integer,?::integer,?::double precision,?::boolean)")
				arguments = append(arguments, row.ID, row.Installs30d, row.Installs90d, row.Installs365d, row.OnRequest30d, row.OnRequest90d, row.OnRequest365d, row.Popularity, row.IsLibrary)
			}
			query := "UPDATE packages AS p SET installs_30d=v.i30,installs_90d=v.i90,installs_365d=v.i365,on_request_30d=v.r30,on_request_90d=v.r90,on_request_365d=v.r365,popularity=v.popularity,is_library=v.library FROM (VALUES " + strings.Join(groups, ",") + ") AS v(id,i30,i90,i365,r30,r90,r365,popularity,library) WHERE p.id=v.id AND p.kind='cask'"
			if err := tx.WithContext(ctx).Exec(query, arguments...).Error; err != nil {
				return fmt.Errorf("update analytics batch: %w", err)
			}
		}
		if err := refreshPackageRanks(ctx, tx); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Exec("INSERT INTO analytics_snapshots (package_id,snapshot_date,installs_30d,installs_90d,installs_365d,on_request_30d,rank_30d) SELECT id,?::date,installs_30d,installs_90d,installs_365d,on_request_30d,rank_30d FROM packages WHERE kind='cask' AND removed_at IS NULL ON CONFLICT (package_id,snapshot_date) DO UPDATE SET installs_30d=EXCLUDED.installs_30d,installs_90d=EXCLUDED.installs_90d,installs_365d=EXCLUDED.installs_365d,on_request_30d=EXCLUDED.on_request_30d,rank_30d=EXCLUDED.rank_30d", date).Error; err != nil {
			return fmt.Errorf("save daily analytics snapshot: %w", err)
		}
		if err := tx.WithContext(ctx).Exec("INSERT INTO job_outbox (unique_key,job_type,payload) VALUES ('snapshot:build','snapshot:build','{}'::jsonb) ON CONFLICT (unique_key) DO NOTHING").Error; err != nil {
			return fmt.Errorf("queue catalog snapshot: %w", err)
		}
		return nil
	})
}
