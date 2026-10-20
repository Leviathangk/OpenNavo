package facts

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

type Attribute struct {
	Name         string   `json:"name"`
	AppVersion   string   `json:"appVersion,omitempty"`
	BooleanValue *bool    `json:"booleanValue,omitempty"`
	Formats      []string `json:"formats,omitempty"`
}

func (v Attribute) Validate() error {
	if len(v.AppVersion) > 100 {
		return ErrInvalid
	}
	switch v.Name {
	case "open_source", "offline", "apple_silicon":
		if v.BooleanValue == nil || v.Formats != nil {
			return ErrInvalid
		}
	case "import_formats", "export_formats":
		if v.BooleanValue != nil || len(v.Formats) == 0 || len(v.Formats) > 32 {
			return ErrInvalid
		}
		for _, f := range v.Formats {
			if strings.TrimSpace(f) == "" || len(f) > 80 {
				return ErrInvalid
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}

type MigrationStep struct {
	Action   string `json:"action"`
	Text     string `json:"text"`
	Evidence Proof  `json:"evidence"`
}
type Migration struct {
	TargetPackageID int64           `json:"targetPackageId"`
	Locale          string          `json:"locale"`
	SourceVersion   string          `json:"sourceVersion,omitempty"`
	TargetVersion   string          `json:"targetVersion,omitempty"`
	Steps           []MigrationStep `json:"steps"`
}

func (v Migration) Validate(now time.Time) error {
	if v.TargetPackageID < 1 || !slices.Contains(i18n.SiteLocales[:], i18n.LocaleCode(v.Locale)) || len(v.Steps) == 0 || len(v.Steps) > 20 || len(v.SourceVersion) > 100 || len(v.TargetVersion) > 100 {
		return ErrInvalid
	}
	for _, s := range v.Steps {
		if !slices.Contains([]string{"backup", "export", "import", "verify"}, s.Action) || strings.TrimSpace(s.Text) == "" || len(s.Text) > 4000 || s.Evidence.Validate(now) != nil {
			return ErrInvalid
		}
	}
	return nil
}

// OfficialDocument confines migration evidence to known vendor domains or a registered repository.
// A GitHub repository grants only its own path, not the whole shared hosting domain.
func OfficialDocument(source string, bases []string) bool {
	if !ValidSourceURL(source) {
		return false
	}
	u, _ := url.Parse(source)
	for _, base := range bases {
		b, err := url.Parse(base)
		if err != nil || b.Hostname() == "" {
			continue
		}
		if b.Hostname() == "github.com" || b.Hostname() == "gitlab.com" {
			path := strings.TrimSuffix(b.Path, "/")
			if u.Hostname() == b.Hostname() && path != "" && (u.Path == path || strings.HasPrefix(u.Path, path+"/")) {
				return true
			}
		} else if u.Hostname() == b.Hostname() || strings.HasSuffix(u.Hostname(), "."+b.Hostname()) {
			return true
		}
	}
	return false
}
