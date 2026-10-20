package agent

import "slices"

const TunerPermission = "seo:automation:tune"

// Tuner tokens may inspect results but cannot execute business mutations.
func ValidPermissionSet(permissions []string) bool {
	if !slices.Contains(permissions, TunerPermission) {
		return true
	}
	for _, p := range permissions {
		if !slices.Contains([]string{TunerPermission, "seo:insight:view", "agent:log:view", "dashboard:view", "catalog:package:view", "ops:job:view", "ops:search:view"}, p) {
			return false
		}
	}
	return true
}
