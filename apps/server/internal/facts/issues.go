package facts

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

type Issue struct {
	Locale         string   `json:"locale"`
	Title          string   `json:"title"`
	Symptom        string   `json:"symptom"`
	AppVersions    []string `json:"appVersions"`
	MacOSVersions  []string `json:"macosVersions"`
	Steps          []string `json:"steps"`
	CommunityLinks []string `json:"communityLinks"`
}

func (v Issue) Validate() error {
	if !slices.Contains(i18n.SiteLocales[:], i18n.LocaleCode(v.Locale)) || strings.TrimSpace(v.Title) == "" || utf8.RuneCountInString(v.Title) > 200 || strings.TrimSpace(v.Symptom) == "" || utf8.RuneCountInString(v.Symptom) > 1000 || len(v.AppVersions) == 0 || len(v.AppVersions) > 32 || len(v.MacOSVersions) == 0 || len(v.MacOSVersions) > 32 || len(v.Steps) == 0 || len(v.Steps) > 20 || len(v.CommunityLinks) > 5 {
		return ErrInvalid
	}
	for _, version := range v.AppVersions {
		if version == "" || utf8.RuneCountInString(version) > 100 || strings.ContainsAny(version, "*?\r\n") {
			return ErrInvalid
		}
	}
	for _, version := range v.MacOSVersions {
		if !macosPattern.MatchString(version) {
			return ErrInvalid
		}
	}
	for _, step := range v.Steps {
		if strings.TrimSpace(step) == "" || utf8.RuneCountInString(step) > 4000 {
			return ErrInvalid
		}
	}
	for _, link := range v.CommunityLinks {
		if !ValidSourceURL(link) {
			return ErrInvalid
		}
	}
	return nil
}
