package agentgate

import (
	"context"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// CanonicalEntity names every edit of a package, including its release and
// screenshot translations, by one stable key: pkg:<packageId>.
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
		return "", ErrUnknownEntity
	}
	var packageID int64
	r := db.WithContext(ctx).Table(table).Select("package_id").Where("id=?", id).Scan(&packageID)
	if r.Error != nil {
		return "", r.Error
	}
	if r.RowsAffected == 0 {
		return "", ErrUnknownEntity
	}
	return "pkg:" + strconv.FormatInt(packageID, 10), nil
}
