package desktoppublish

import (
	"encoding/json"
	"testing"
)

func TestValidateArtifactsPinsVersionRepositoryAndTargets(t *testing.T) {
	rows := []map[string]string{}
	for target, name := range map[string]string{"darwin-aarch64": "OpenNavo_aarch64.app.tar.gz", "darwin-x86_64": "OpenNavo_x64.app.tar.gz", "dmg-universal": "OpenNavo_1.2.3_universal.dmg"} {
		rows = append(rows, map[string]string{"target": target, "url": "https://github.com/opennavo/OpenNavo/releases/download/desktop-v1.2.3/" + name})
	}
	raw, _ := json.Marshal(rows)
	if err := validateArtifacts("1.2.3", raw); err != nil {
		t.Fatal(err)
	}
	if err := validateArtifacts("1.2.4", raw); err == nil {
		t.Fatal("accepted wrong version")
	}
	rows[0]["url"] = "https://example.com/untrusted.dmg"
	raw, _ = json.Marshal(rows)
	if err := validateArtifacts("1.2.3", raw); err == nil {
		t.Fatal("accepted third-party artifact")
	}
	rows[0] = rows[1]
	raw, _ = json.Marshal(rows)
	if err := validateArtifacts("1.2.3", raw); err == nil {
		t.Fatal("accepted duplicate target")
	}
}
