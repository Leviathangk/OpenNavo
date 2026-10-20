package store

import (
	"context"
	"time"

	sources "github.com/opennavo/opennavo/server/internal/sourcefacts"
)

// SaveAppStorePriceSnapshot stores factual observations only, never Apple's promotional assets.
// The UTC day key makes retries idempotent; a snapshot records what was actually observed then.
func (s *Store) SaveAppStorePriceSnapshot(ctx context.Context, id int64, p sources.Listing, checked time.Time) error {
	return s.DB.WithContext(ctx).Exec(`INSERT INTO app_store_price_snapshots(package_id,observed_on,region,bundle_id,amount,currency,purchase_url,checked_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(package_id,observed_on,region,bundle_id) DO UPDATE SET amount=EXCLUDED.amount,currency=EXCLUDED.currency,purchase_url=EXCLUDED.purchase_url,checked_at=EXCLUDED.checked_at`,
		id, checked.UTC().Format("2006-01-02"), p.Region, p.BundleID, p.Price, p.Currency, p.URL, checked).Error
}
