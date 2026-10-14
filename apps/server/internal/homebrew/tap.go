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

// ParseTapCask splits a fully qualified cask name such as swiftvm-app/tap/swiftvm the way brew does:
// names are case-insensitive and a homebrew- prefix on the repository part is optional.
func ParseTapCask(fullToken string) (tap, token string, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(fullToken)), "/")
	if len(parts) != 3 {
		return "", "", errors.New("expected owner/tap/token")
	}
	owner, name, token := parts[0], strings.TrimPrefix(parts[1], "homebrew-"), parts[2]
	if !tapOwnerPattern.MatchString(owner) || strings.Contains(owner, "--") || strings.HasSuffix(owner, "-") || !tapNamePattern.MatchString(name) || name == ".." {
		return "", "", errors.New("invalid tap name")
	}
	if owner == "homebrew" {
		return "", "", errors.New("official Homebrew taps are synced automatically")
	}
	if !tokenPattern.MatchString(token) || len(token) > 128 {
		return "", "", errors.New("invalid cask token")
	}
	return owner + "/" + name, token, nil
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
