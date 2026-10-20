package agentgate

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

// Legacy comparison tools must bind the named alternative and every displayed
// reason to one captured official excerpt, just like the unified fact writer.
func validateAlternatives(ctx context.Context, db *gorm.DB, body map[string]any, evidence []Evidence) error {
	var request struct {
		Items []struct {
			PackageID       int64 `json:"packageId"`
			Reasons, Limits []string
			BestFor         *string `json:"bestFor"`
		}
	}
	raw, err := json.Marshal(body)
	if err != nil || json.Unmarshal(raw, &request) != nil {
		return reject("invalid_alternatives")
	}
	for _, item := range request.Items {
		var row struct{ Name, Token string }
		r := db.WithContext(ctx).Raw("SELECT name,token FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL", item.PackageID).Scan(&row)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return reject("unknown_alternative")
		}
		values := []string{}
		for _, claim := range append(item.Reasons, item.Limits...) {
			values = append(values, strings.ReplaceAll(claim, "_", " "))
		}
		if item.BestFor != nil {
			values = append(values, strings.ReplaceAll(*item.BestFor, "_", " "))
		}
		bound := false
		for _, proof := range evidence {
			if proof.Authority != "official" {
				continue
			}
			if bindValues(row.Name, proof) != nil && bindValues(row.Token, proof) != nil {
				continue
			}
			claims := make([]any, len(values))
			for i, value := range values {
				claims[i] = value
			}
			if bindValues(claims, proof) == nil {
				bound = true
				break
			}
		}
		if !bound {
			return reject("alternative_claim_not_in_evidence")
		}
	}
	return nil
}
