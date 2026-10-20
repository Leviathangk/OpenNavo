package seotasks

import (
	"context"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// CanonicalEntity serializes all edits of a package, including release and
// screenshot translations, under one stable entity lease.
func CanonicalEntity(ctx context.Context, db *gorm.DB, key string) (string, error) {
	if strings.HasPrefix(key, "package:") {
		return "pkg:" + strings.TrimPrefix(key, "package:"), nil
	}
	kind, raw, ok := strings.Cut(key, ":")
	if !ok {
		return key, nil
	}
	table := ""
	switch kind {
	case "release":
		table = "releases"
	case "screenshot":
		table = "package_screenshots"
	default:
		return key, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return "", ErrInvalid
	}
	var packageID int64
	r := db.WithContext(ctx).Table(table).Select("package_id").Where("id=?", id).Scan(&packageID)
	if r.Error != nil {
		return "", r.Error
	}
	if r.RowsAffected == 0 {
		return "", ErrInvalid
	}
	return "pkg:" + strconv.FormatInt(packageID, 10), nil
}

// Rebind replaces a provisional creation key with its allocated stable identity.
// It runs inside the creation transaction, preserving fencing and replay safety.
func Rebind(ctx context.Context, tx *gorm.DB, task Task, owner, newKey string) error {
	oldKey, err := CanonicalEntity(ctx, tx, task.EntityKey)
	if err != nil {
		return err
	}
	newKey, err = CanonicalEntity(ctx, tx, newKey)
	if err != nil {
		return err
	}
	if oldKey == newKey {
		return nil
	}
	var lease struct {
		Fencing int64
		Active  bool
	}
	r := tx.Raw(`INSERT INTO seo_entity_leases(entity_key,fencing) VALUES(?,0) ON CONFLICT(entity_key) DO UPDATE SET entity_key=EXCLUDED.entity_key RETURNING fencing,(lease_until>clock_timestamp()) active`, newKey).Scan(&lease)
	if r.Error != nil {
		return r.Error
	}
	if lease.Active || lease.Fencing >= task.Fencing {
		return ErrConflict
	}
	if err := tx.Exec("UPDATE seo_entity_leases SET task_id=?,owner=?,fencing=?,lease_until=? WHERE entity_key=?", task.ID, owner, task.Fencing, task.LeaseUntil, newKey).Error; err != nil {
		return err
	}
	if err := tx.Exec("UPDATE seo_tasks SET entity_key=? WHERE id=?", newKey, task.ID).Error; err != nil {
		return err
	}
	return release(tx, task, owner)
}
