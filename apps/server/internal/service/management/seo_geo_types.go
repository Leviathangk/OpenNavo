package management

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
)

// These closed DTOs deliberately contain no provider response or answer text.
type geoPrompt struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	Hash           string `json:"hash"`
	Classification string `json:"classification"`
}
type geoEngine struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Surface      string `json:"surface"`
	ModelVersion string `json:"modelVersion"`
	ToolVersion  string `json:"toolVersion"`
}
type geoContext struct {
	Market         string `json:"market"`
	PromptLanguage string `json:"promptLanguage"`
	AnswerLanguage string `json:"answerLanguage"`
	SearchMode     string `json:"searchMode"`
	WindowDays     int    `json:"windowDays"`
}
type geoCell struct {
	ID                   string   `json:"id"`
	PromptID             string   `json:"promptId"`
	EngineKey            string   `json:"engineKey"`
	Status               string   `json:"status"`
	ObservationID        *string  `json:"observationId"`
	AnswerHash           *string  `json:"answerHash"`
	SearchTriggered      *bool    `json:"searchTriggered"`
	CitationExtraction   string   `json:"citationExtraction"`
	EvidenceAvailability string   `json:"evidenceAvailability"`
	Mentioned            *bool    `json:"mentioned"`
	Candidate            *bool    `json:"candidate"`
	Cited                *bool    `json:"cited"`
	OwnURLs              []string `json:"ownUrls"`
	ThirdPartyDomains    []string `json:"thirdPartyDomains"`
	DomainsTruncated     bool     `json:"domainsTruncated"`
}
type geoReported struct {
	Answered      *int     `json:"answered"`
	Mentioned     *int     `json:"mentioned"`
	Candidate     *int     `json:"candidate"`
	Cited         *int     `json:"cited"`
	MissingFields []string `json:"missingFields"`
}
type geoCharge struct {
	VendorTaskID string    `json:"vendorTaskId"`
	CostUSD      *string   `json:"costUsd"`
	Credits      *int64    `json:"credits"`
	IncurredAt   time.Time `json:"incurredAt"`
}
type geoSnapshot struct {
	SchemaVersion        int          `json:"schemaVersion"`
	Provider             string       `json:"provider"`
	SourceProject        string       `json:"sourceProject"`
	ProviderRunKey       string       `json:"providerRunKey"`
	Mode                 string       `json:"mode"`
	PanelID              string       `json:"panelId"`
	PanelVersion         string       `json:"panelVersion"`
	PeriodStart          time.Time    `json:"periodStart"`
	PeriodEnd            time.Time    `json:"periodEnd"`
	Context              geoContext   `json:"context"`
	Prompts              []geoPrompt  `json:"prompts"`
	Engines              []geoEngine  `json:"engines"`
	PlannedCount         int          `json:"plannedCount"`
	Cells                []geoCell    `json:"cells"`
	Reported             *geoReported `json:"reported"`
	ArtifactCompleteness string       `json:"artifactCompleteness"`
	HashAlgorithm        string       `json:"hashAlgorithm"`
	HashNormalization    string       `json:"hashNormalization"`
	Charges              []geoCharge  `json:"charges"`
	ApprovedBatchRef     string       `json:"approvedBatchRef"`
}
type geoRequest struct {
	IdempotencyKey string      `json:"idempotencyKey"`
	Supersedes     *int64      `json:"supersedes"`
	Snapshot       geoSnapshot `json:"snapshot"`
}
type geoMetric struct {
	Positive int `json:"positive"`
	Known    int `json:"known"`
}
type geoCounts struct {
	Planned          int          `json:"planned"`
	Answered         *int         `json:"answered"`
	SearchTriggered  geoMetric    `json:"searchTriggered"`
	Mentioned        geoMetric    `json:"mentioned"`
	Candidate        geoMetric    `json:"candidate"`
	Cited            geoMetric    `json:"cited"`
	CitationComplete int          `json:"citationComplete"`
	Reported         *geoReported `json:"reported"`
}

var geoID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
var geoHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var geoLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._:/-]{0,119}$`)
var geoDomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func geoDomain(value string) bool {
	if len(value) > 253 || value != strings.ToLower(value) || net.ParseIP(value) != nil {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 || slices.Contains([]string{"local", "localhost", "internal", "lan"}, labels[len(labels)-1]) {
		return false
	}
	for _, label := range labels {
		if !geoDomainLabel.MatchString(label) {
			return false
		}
	}
	return true
}
func geoURL(raw, host string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Port() == "" && geoDomain(u.Host) && (host == "" || u.Host == host)
}
func decodeGeo(raw []byte, site string, now time.Time) (geoRequest, error) {
	var r geoRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if len(raw) > 512<<10 || dec.Decode(&r) != nil {
		return r, seotasks.ErrInvalid
	}
	v := r.Snapshot
	if v.Prompts == nil || v.Engines == nil || v.Cells == nil || v.Charges == nil {
		return r, seotasks.ErrInvalid
	}
	if !geoDomain(site) || v.SchemaVersion != 1 || v.Provider != "openseo" || !geoID.MatchString(v.SourceProject) || !geoID.MatchString(v.ProviderRunKey) || !geoID.MatchString(v.PanelID) || !geoID.MatchString(v.PanelVersion) || !geoID.MatchString(v.ApprovedBatchRef) || v.HashAlgorithm != "sha256" || !geoID.MatchString(v.HashNormalization) || (r.Supersedes != nil && *r.Supersedes < 1) {
		return r, seotasks.ErrInvalid
	}
	if !slices.Contains([]string{"summary_only", "imported_observation"}, v.Mode) || !slices.Contains([]string{"complete", "incomplete", "unknown"}, v.ArtifactCompleteness) || v.PlannedCount < 1 || v.PlannedCount > 500 || len(v.Prompts) > 100 || len(v.Prompts) < 1 || len(v.Engines) > 10 || len(v.Engines) < 1 || len(v.Charges) > 100 {
		return r, seotasks.ErrInvalid
	}
	if v.PeriodStart.IsZero() || v.PeriodEnd.Before(v.PeriodStart) || v.PeriodEnd.After(now.Add(5*time.Minute)) || v.Context.WindowDays < 1 || v.Context.WindowDays > 366 || v.PeriodEnd.Sub(v.PeriodStart) > time.Duration(v.Context.WindowDays)*24*time.Hour {
		return r, seotasks.ErrInvalid
	}
	for _, value := range []string{v.Context.Market, v.Context.PromptLanguage, v.Context.AnswerLanguage, v.Context.SearchMode} {
		if !geoLabel.MatchString(value) {
			return r, seotasks.ErrInvalid
		}
	}
	prompts, engines := map[string]bool{}, map[string]bool{}
	for _, p := range v.Prompts {
		if !geoID.MatchString(p.ID) || prompts[p.ID] || len(p.Text) < 1 || len(p.Text) > 500 || strings.ContainsAny(p.Text, "\x00\r\n") || !geoHash.MatchString(p.Hash) || !slices.Contains([]string{"neutral", "brand", "exploration"}, p.Classification) {
			return r, seotasks.ErrInvalid
		}
		prompts[p.ID] = true
	}
	for _, e := range v.Engines {
		if !geoID.MatchString(e.Key) || engines[e.Key] {
			return r, seotasks.ErrInvalid
		}
		for _, value := range []string{e.Name, e.Surface, e.ModelVersion, e.ToolVersion} {
			if !geoLabel.MatchString(value) {
				return r, seotasks.ErrInvalid
			}
		}
		engines[e.Key] = true
	}
	if v.PlannedCount > len(prompts)*len(engines) {
		return r, seotasks.ErrInvalid
	}
	if v.Mode == "summary_only" {
		if len(v.Cells) != 0 || v.Reported == nil || v.ArtifactCompleteness == "complete" || len(v.Reported.MissingFields) < 1 || len(v.Reported.MissingFields) > 20 {
			return r, seotasks.ErrInvalid
		}
		for _, value := range []*int{v.Reported.Answered, v.Reported.Mentioned, v.Reported.Candidate, v.Reported.Cited} {
			if value != nil && (*value < 0 || *value > v.PlannedCount) {
				return r, seotasks.ErrInvalid
			}
		}
		for _, value := range [](*int){v.Reported.Mentioned, v.Reported.Candidate, v.Reported.Cited} {
			if value != nil && v.Reported.Answered != nil && *value > *v.Reported.Answered {
				return r, seotasks.ErrInvalid
			}
		}
		for _, value := range v.Reported.MissingFields {
			if !geoID.MatchString(value) {
				return r, seotasks.ErrInvalid
			}
		}
	} else if v.Reported != nil || len(v.Cells) != v.PlannedCount {
		return r, seotasks.ErrInvalid
	}
	ids, pairs, observations := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range v.Cells {
		if c.OwnURLs == nil || c.ThirdPartyDomains == nil {
			return r, seotasks.ErrInvalid
		}
		pair := c.PromptID + "/" + c.EngineKey
		if !geoID.MatchString(c.ID) || ids[c.ID] || pairs[pair] || !prompts[c.PromptID] || !engines[c.EngineKey] || !slices.Contains([]string{"answered", "not_triggered", "error", "unsupported", "missing"}, c.Status) || !slices.Contains([]string{"complete", "incomplete", "unknown"}, c.CitationExtraction) || !slices.Contains([]string{"available", "unknown", "evidence_unavailable"}, c.EvidenceAvailability) {
			return r, seotasks.ErrInvalid
		}
		ids[c.ID], pairs[pair] = true, true
		if c.ObservationID != nil {
			if !geoID.MatchString(*c.ObservationID) || observations[*c.ObservationID] {
				return r, seotasks.ErrInvalid
			}
			observations[*c.ObservationID] = true
		}
		if c.AnswerHash != nil && !geoHash.MatchString(*c.AnswerHash) {
			return r, seotasks.ErrInvalid
		}
		if c.Status == "answered" {
			if c.ObservationID == nil || c.AnswerHash == nil {
				return r, seotasks.ErrInvalid
			}
		} else if c.Mentioned != nil || c.Candidate != nil || c.Cited != nil || len(c.OwnURLs) > 0 || len(c.ThirdPartyDomains) > 0 || c.AnswerHash != nil || c.CitationExtraction == "complete" {
			return r, seotasks.ErrInvalid
		}
		// Incomplete extraction cannot establish absence. Positive observed citations remain usable.
		if c.Cited != nil && !*c.Cited && c.CitationExtraction != "complete" {
			return r, seotasks.ErrInvalid
		}
		if c.Cited != nil && *c.Cited && len(c.OwnURLs) == 0 || len(c.OwnURLs) > 0 && (c.Cited == nil || !*c.Cited) {
			return r, seotasks.ErrInvalid
		}
		if len(c.OwnURLs) > 20 || len(c.ThirdPartyDomains) > 10 {
			return r, seotasks.ErrInvalid
		}
		seen := map[string]bool{}
		for _, u := range c.OwnURLs {
			if !geoURL(u, site) || seen[u] {
				return r, seotasks.ErrInvalid
			}
			seen[u] = true
		}
		for _, d := range c.ThirdPartyDomains {
			if !geoDomain(d) || d == site || seen[d] {
				return r, seotasks.ErrInvalid
			}
			seen[d] = true
		}
	}
	charges := map[string]bool{}
	for _, c := range v.Charges {
		if !geoID.MatchString(c.VendorTaskID) || charges[c.VendorTaskID] || c.IncurredAt.IsZero() || c.IncurredAt.After(now.Add(5*time.Minute)) || c.Credits != nil && (*c.Credits < 0 || *c.Credits > 1000000000) {
			return r, seotasks.ErrInvalid
		}
		if c.CostUSD != nil {
			if _, err := seobudget.Micros(*c.CostUSD); err != nil {
				return r, err
			}
		}
		charges[c.VendorTaskID] = true
	}
	return r, nil
}
func geoAggregate(v geoSnapshot) geoCounts {
	out := geoCounts{Planned: v.PlannedCount, Reported: v.Reported}
	if v.Mode == "summary_only" {
		return out
	}
	n := 0
	out.Answered = &n
	add := func(m *geoMetric, v *bool) {
		if v != nil {
			m.Known++
			if *v {
				m.Positive++
			}
		}
	}
	for _, c := range v.Cells {
		add(&out.SearchTriggered, c.SearchTriggered)
		if c.Status != "answered" {
			continue
		}
		n++
		add(&out.Mentioned, c.Mentioned)
		add(&out.Candidate, c.Candidate)
		// Citation rates use only complete extraction; a partial positive stays in the detail.
		if c.CitationExtraction == "complete" {
			out.CitationComplete++
			add(&out.Cited, c.Cited)
		}
	}
	return out
}
