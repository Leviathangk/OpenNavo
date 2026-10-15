package homebrew

import (
	"regexp"
	"strings"
)

// caskVersionLine is a version stanza directly inside the cask block. Homebrew style indents stanzas by
// two spaces, so per-architecture versions (on_arm, on_intel blocks) sit deeper and never match.
var caskVersionLine = regexp.MustCompile(`^  version +(?:"([^"]*)"|'([^']*)'|:(latest))\s*(?:#.*)?$`)

// CaskSourceVersion reads the version a cask file declares without evaluating the rest of its DSL, so
// past revisions that ParseCaskSource would reject can still be dated. It returns "" unless the file has
// exactly one top-level version stanza holding a literal ParseCaskSource accepts; :latest is "latest".
func CaskSourceVersion(source string) string {
	version, found := "", false
	for _, line := range strings.Split(source, "\n") {
		match := caskVersionLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if match == nil {
			continue
		}
		if found {
			return ""
		}
		version, found = match[1]+match[2], true
		if match[3] != "" {
			version = "latest"
		}
	}
	if !found || !versionCharacters.MatchString(version) {
		return ""
	}
	return version
}
