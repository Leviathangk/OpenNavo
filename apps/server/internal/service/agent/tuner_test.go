package agent

import "testing"

func TestTunerCannotCarryExecutionPermissions(t *testing.T) {
	if !ValidPermissionSet([]string{TunerPermission, "seo:insight:view"}) {
		t.Fatal("tuner reads denied")
	}
	for _, p := range Grantable {
		if p == TunerPermission {
			continue
		}
		if p == "translation:review" || p == "seo:task:handle" || p == "catalog:package:edit" || p == "content:article:publish" {
			if ValidPermissionSet([]string{TunerPermission, p}) {
				t.Fatalf("mixed token allowed: %s", p)
			}
		}
	}
	if ValidPermissionSet([]string{TunerPermission, "*"}) {
		t.Fatal("wildcard escalation")
	}
}
