package facts

import (
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

type BackgroundContent struct {
	Locale      string   `json:"locale"`
	Description string   `json:"description"`
	ItemType    string   `json:"itemType"`
	CommonPaths []string `json:"commonPaths"`
	Evidence    Proof    `json:"evidence"`
}

func ValidBackgroundLabel(label string) bool {
	return len(label) > 0 && len(label) <= 255 && strings.TrimSpace(label) == label && !strings.ContainsAny(label, "/\\?#") && !strings.ContainsFunc(label, unicode.IsControl)
}
func (v BackgroundContent) Validate(now time.Time) error {
	if !slices.Contains(i18n.SiteLocales[:], i18n.LocaleCode(v.Locale)) || strings.TrimSpace(v.Description) == "" || len(v.Description) > 10000 || len(v.CommonPaths) > 30 || !slices.Contains([]string{"launch_agent", "launch_daemon", "login_item", "package_receipt", "unknown"}, v.ItemType) {
		return ErrInvalid
	}
	for _, path := range v.CommonPaths {
		if len(path) > 1024 || strings.ContainsFunc(path, unicode.IsControl) || !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~/") {
			return ErrInvalid
		}
	}
	return v.Evidence.Validate(now)
}
