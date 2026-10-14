package store

import "context"

// ReleaseNotesTarget is where editorial notes for a requested version live. A recorded full build
// version (0.1,49) owns its notes; any other recorded version writes the shared note of its base
// version, which every build without a note of its own shows.
type ReleaseNotesTarget struct {
	Base, Build    string
	PackageVersion int64
}

func (t ReleaseNotesTarget) SourceKey() string {
	if t.Build != "" {
		return "editorial:" + t.Build
	}
	return "editorial:" + t.Base
}

// Scope selects the editorial rows of this target among releases sharing its base version.
func (t ReleaseNotesTarget) Scope() (string, []any) {
	if t.PackageVersion > 0 {
		return "package_version_id=?", []any{t.PackageVersion}
	}
	return "package_version_id IS NULL", nil
}

// ResolveReleaseNotesTarget accepts a recorded cask build (2026.4,237457) or base version (2026.4).
func (s *Store) ResolveReleaseNotesTarget(ctx context.Context, packageID int64, version string) (ReleaseNotesTarget, bool, error) {
	var row struct {
		ID                   int64
		Version, VersionBase string
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT v.id,v.version,v.version_base FROM package_versions v JOIN packages p ON p.id=v.package_id WHERE v.package_id=? AND p.kind='cask' AND (v.version=? OR v.version_base=?) ORDER BY v.version=? DESC,v.id DESC LIMIT 1`, packageID, version, version, version).Scan(&row).Error; err != nil || row.ID == 0 {
		return ReleaseNotesTarget{}, false, err
	}
	if row.Version == version && row.Version != row.VersionBase {
		return ReleaseNotesTarget{Base: row.VersionBase, Build: row.Version, PackageVersion: row.ID}, true, nil
	}
	return ReleaseNotesTarget{Base: version}, true, nil
}

// BuildNoteSQL is the visible note owned by the build whose package_versions.id is the expression id.
func BuildNoteSQL(id string) string {
	return `(SELECT r.id FROM releases r WHERE r.package_version_id=` + id + ` AND r.source='editorial' AND NOT r.hidden ORDER BY r.id DESC LIMIT 1)`
}

// BaseNoteSQL is the visible shared note of a base version.
func BaseNoteSQL(packageID, base string) string {
	return `(SELECT r.id FROM releases r WHERE r.package_id=` + packageID + ` AND r.version=` + base + ` AND r.package_version_id IS NULL AND r.source='editorial' AND NOT r.hidden ORDER BY r.id DESC LIMIT 1)`
}

// EditorialNoteSQL is the note a build shows: its own, otherwise the shared note of its base version.
// id may be NULL for versions without a recorded build, which then fall back to the base note.
func EditorialNoteSQL(id, packageID, base string) string {
	return "COALESCE(" + BuildNoteSQL(id) + "," + BaseNoteSQL(packageID, base) + ")"
}

// EditorialScopeSQL names the note EditorialNoteSQL picks: 'build', 'base' or 'none'.
func EditorialScopeSQL(id, packageID, base string) string {
	return "CASE WHEN " + BuildNoteSQL(id) + " IS NOT NULL THEN 'build' WHEN " + BaseNoteSQL(packageID, base) + " IS NOT NULL THEN 'base' ELSE 'none' END"
}
