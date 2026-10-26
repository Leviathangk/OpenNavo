package management

import (
	"context"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
)

// Due fact records: verified records whose evidence has expired or expires within withinDays (default 14,
// at most 90), the most popular apps first and then the earliest validUntil, so agents re-check what
// readers see most before it lapses.
const (
	dueFactDefaultDays = 14
	dueFactMaxDays     = 90
)

func (s *Service) dueFactRecords(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	days := dueFactDefaultDays
	if raw, ok := in.Params["withindays"]; ok && raw != nil {
		value, ok := raw.(float64)
		if !ok || value < 0 || value > dueFactMaxDays || value != float64(int(value)) {
			return nil, domain.Rejected("parameter_invalid:withinDays")
		}
		days = int(value)
	}
	now := s.now()
	rows, total, err := s.Store.DueFactRecords(ctx, now.AddDate(0, 0, days), text(in.Params, "kind"), current, size)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(s.Config.WebBaseURL, "/")
	records := []any{}
	for _, row := range rows {
		proof := row.Evidence.At(now)
		var checkedAt, validUntil any
		if proof.CheckedAt != nil {
			checkedAt = proof.CheckedAt.UTC().Format(time.RFC3339)
		}
		if proof.ValidUntil != nil {
			validUntil = proof.ValidUntil.UTC().Format(time.RFC3339)
		}
		records = append(records, map[string]any{"id": row.ID, "packageId": row.PackageID, "token": row.Token, "name": row.Name, "packageKind": row.PackageKind,
			"kind": row.Kind, "key": row.Key, "value": row.Value, "expired": proof.ValidUntil != nil && !now.Before(*proof.ValidUntil),
			"evidence":   map[string]any{"sourceUrl": proof.SourceURL, "checkedAt": checkedAt, "validUntil": validUntil, "status": proof.Status},
			"popularity": row.Popularity, "rank30d": row.Rank30d, "installs30d": row.Installs30d, "webUrl": base + "/apps/" + row.Token})
	}
	return map[string]any{"current": current, "size": size, "total": total, "withinDays": days, "records": records}, nil
}
