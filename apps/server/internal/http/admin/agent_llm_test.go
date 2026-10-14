package admin

import "testing"

func TestArticleLLMQuotaClassification(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		body      map[string]any
		want      bool
	}{
		{"publish submitted revision", "publishArticle", map[string]any{"expectedRevision": 3}, true},
		{"publish without localized fields", "publishArticle", nil, true},
		{"publish cannot opt out with clear", "publishArticle", map[string]any{"clear": true}, true},
		{"restore", "restoreArticle", map[string]any{"expectedRevision": 3}, true},
		{"withdraw", "withdrawArticle", map[string]any{"expectedRevision": 3}, false},
		{"validate", "validateArticle", map[string]any{"expectedRevision": 3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := consumesLLM(tt.operation, tt.body); got != tt.want {
				t.Fatalf("consumesLLM(%q) = %v, want %v", tt.operation, got, tt.want)
			}
		})
	}
}
