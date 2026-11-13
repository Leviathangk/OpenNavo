package homebrew

import (
	"errors"
	"regexp"
	"strings"
)

var (
	tapOwnerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}$`)
	tapNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,99}$`)
)

// IsOfficialTap reports whether a tap belongs to Homebrew itself; the official cask sync owns those packages.
func IsOfficialTap(tap string) bool {
	return tap == "" || strings.HasPrefix(tap, "homebrew/")
}

// ValidCaskToken checks the shared catalog and Homebrew token syntax without normalizing input.
func ValidCaskToken(token string) bool {
	return len(token) <= 128 && tokenPattern.MatchString(token)
}

// ParseTap validates a tap name such as swiftvm-app/tap; the GitHub repository is user/homebrew-tap.
// Official Homebrew taps are rejected because the official sync owns them.
func ParseTap(tap string) (string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(tap)), "/")
	if len(parts) != 2 {
		return "", errors.New("expected owner/tap")
	}
	owner, name := parts[0], strings.TrimPrefix(parts[1], "homebrew-")
	if !tapOwnerPattern.MatchString(owner) || strings.Contains(owner, "--") || strings.HasSuffix(owner, "-") || !tapNamePattern.MatchString(name) || name == ".." {
		return "", errors.New("invalid tap name")
	}
	if owner == "homebrew" {
		return "", errors.New("official Homebrew taps are synced automatically")
	}
	return owner + "/" + name, nil
}

// ParseTapCask splits a fully qualified cask name such as swiftvm-app/tap/swiftvm the way brew does:
// names are case-insensitive and a homebrew- prefix on the repository part is optional.
func ParseTapCask(fullToken string) (tap, token string, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(fullToken)), "/")
	if len(parts) != 3 {
		return "", "", errors.New("expected owner/tap/token")
	}
	if tap, err = ParseTap(parts[0] + "/" + parts[1]); err != nil {
		return "", "", err
	}
	if token = parts[2]; !ValidCaskToken(token) {
		return "", "", errors.New("invalid cask token")
	}
	return tap, token, nil
}

// TapRepository is the GitHub repository brew clones for a tap: user/name -> user/homebrew-name.
func TapRepository(tap string) string {
	owner, name, _ := strings.Cut(tap, "/")
	return owner + "/homebrew-" + name
}

// TapCaskPaths lists where a tap may keep a cask file, flat first and then sharded by first letter.
func TapCaskPaths(token string) []string {
	return []string{"Casks/" + token + ".rb", "Casks/" + token[:1] + "/" + token + ".rb"}
}

// tapCaskPathPattern is the same layout tap_casks.source_path allows: Casks/<token>.rb or
// Casks/<first letter>/<token>.rb. Deeper nesting and other directories are never casks.
var tapCaskPathPattern = regexp.MustCompile(`^Casks/([0-9a-z_.-]+/)?[0-9a-z@+_.-]+[.]rb$`)

// TapCaskTokenFromPath reads the cask token a tap repository path declares. It is used when importing a
// whole tap, so a file only counts when the path layout and the token syntax both hold.
func TapCaskTokenFromPath(path string) (string, bool) {
	if !tapCaskPathPattern.MatchString(path) {
		return "", false
	}
	token := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".rb")
	if !ValidCaskToken(token) {
		return "", false
	}
	return token, true
}
