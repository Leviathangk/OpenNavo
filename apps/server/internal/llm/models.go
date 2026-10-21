package llm

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type Attempt struct {
	Result                                                                   string
	Model, ReasoningEffort, FinishReason                                     string
	PromptTokens, CompletionTokens, ReasoningTokens, CachedTokens, LatencyMS int64
}
type Usage struct{ Attempts []Attempt }

func (u Usage) Tokens() int64 {
	var n int64
	for _, a := range u.Attempts {
		n += a.PromptTokens + a.CompletionTokens
	}
	return n
}

type Failure struct {
	Reason           string
	Retryable, Pause bool
	// Violations lists every failed content check; Reason is the first one.
	Violations []ContentViolation
}
type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}
type Totals struct {
	Tokens  int64
	CostUSD float64
}

func (p Price) Cost(usage Attempt) float64 {
	return (float64(usage.PromptTokens)*p.Input + float64(usage.CompletionTokens)*p.Output) / 1000000
}
func (e *Failure) Error() string { return "language request failed: " + e.Reason }

var ErrPaused = errors.New("language queue paused")

func Truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}
func TruncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
