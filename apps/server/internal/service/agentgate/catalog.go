package agentgate

import "strings"

// AutonomousCatalogWrite identifies ordinary catalog editing. The agent judges
// provenance and wording; SEO task allocation is not authorization to edit.
// Authentication and the operation's permissions remain mandatory.
func AutonomousCatalogWrite(operation string) bool {
	if operation == "" {
		return false
	}
	operation = strings.ToUpper(operation[:1]) + operation[1:]
	switch operation {
	case "UpdatePackageText", "UpdatePackageMeta", "SetPackagePrice", "SetPackageMacosSupport",
		"SetPackageAlternatives", "UpdatePackageI18n", "SetPackageCategories", "SetPackageFactRecords",
		"SetPackageIcon", "DeletePackageIcon", "AddPackageScreenshot", "ReorderPackageScreenshots",
		"UpdatePackageScreenshot", "DeletePackageScreenshot", "UpdatePackageChangelogSettings",
		"UpsertReleaseNotes", "UpdateRelease", "UpdateReleaseI18n":
		return true
	}
	return false
}

func CatalogEntity(entity string) bool {
	return strings.HasPrefix(entity, "pkg:") || strings.HasPrefix(entity, "package:") || strings.HasPrefix(entity, "release:")
}
