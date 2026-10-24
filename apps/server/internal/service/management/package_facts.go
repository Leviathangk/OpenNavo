package management

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/store"
)

// factWrites record package revisions, so editors and agents can roll them back like any other
// package change; their dry runs also report the evidence gaps left after the write.
var factWrites = []string{"SetPackagePrice", "SetPackageMacosSupport", "SetPackageAlternatives"}

func factActor(ctx context.Context) facts.Actor {
	op := domain.CurrentOperation(ctx)
	if op == nil {
		return facts.Actor{}
	}
	return facts.Actor{Name: op.ActorName, Agent: op.AgentClientID > 0}
}

func validationError(err error, reasons ...string) error {
	if errors.Is(err, facts.ErrInvalid) {
		reason := "fact_invalid"
		if len(reasons) > 0 {
			reason = reasons[0]
		}
		return domain.Rejected(reason)
	}
	return err
}

func evidenceView(e facts.Evidence, now time.Time) map[string]any {
	return map[string]any{
		"appVersion": e.AppVersion, "sourceUrl": e.SourceURL, "method": e.Method,
		"verifiedAt": e.VerifiedAt.UTC().Format(time.RFC3339), "verifiedBy": e.VerifiedBy, "verifier": e.Verifier,
		"status": e.Status, "note": e.Note, "stale": facts.Stale(e.VerifiedAt, now),
	}
}

func priceView(p *facts.Price, now time.Time) any {
	if p == nil {
		return nil
	}
	view := evidenceView(p.Evidence, now)
	view["type"], view["amount"], view["currency"], view["region"], view["plan"], view["period"] = p.Type, p.Amount, p.Currency, p.Region, p.Plan, p.Period
	return view
}

func macosView(m *facts.Macos, now time.Time) any {
	if m == nil {
		return nil
	}
	view := evidenceView(m.Evidence, now)
	view["ranges"] = m.Ranges
	return view
}

func homebrewView(hb facts.Homebrew) map[string]any {
	status := "unknown"
	if hb.Known {
		status = "known"
	}
	return map[string]any{
		"supportStatus": status, "arm64": hb.Arm64, "x86_64": hb.X8664, "requiresRosetta": hb.RequiresRosetta,
		"minimum": nullableText(hb.Minimum), "maximum": nullableText(hb.Maximum),
	}
}

// factsView is the admin and Agent view: stored facts, Homebrew's side, conflicts and gaps.
func factsView(ctx context.Context, st *store.Store, id int64, now time.Time) (map[string]any, error) {
	current, found, err := st.PackageFacts(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(domain.CodeNotFound)
	}
	rows, err := st.Alternatives(ctx, id, false)
	if err != nil {
		return nil, err
	}
	alternatives, unavailable := []any{}, 0
	for _, row := range rows {
		if !row.Available {
			unavailable++
		}
		alternatives = append(alternatives, map[string]any{
			"packageId": row.PackageID, "token": row.Token, "name": row.Name, "displayName": row.DisplayName, "iconUrl": row.IconURL,
			"reasons": row.Reasons, "limits": row.Limits, "bestFor": row.BestFor, "note": row.Note, "source": row.Source, "available": row.Available,
		})
	}
	var updatedAt any
	if current.UpdatedAt != nil {
		updatedAt = current.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"packageId":    id,
		"price":        priceView(current.Price, now),
		"macos":        macosView(current.Macos, now),
		"homebrew":     homebrewView(current.Homebrew),
		"conflicts":    facts.Conflicts(current.Macos, current.Homebrew),
		"alternatives": alternatives,
		"offerReady":   facts.OfferReady(current.Price, now),
		"gaps":         facts.Gaps(current.Price, current.Macos, current.Homebrew, unavailable, now),
		"updatedAt":    updatedAt,
	}, nil
}

func (s *Service) packageFacts(ctx context.Context, op string, in Input) (any, error) {
	now := s.now()
	if op == "GetPackageFacts" {
		return factsView(ctx, s.Store, in.ID, now)
	}
	actor := factActor(ctx)
	var price *facts.Price
	var macos *facts.Macos
	var alternatives []facts.Alternative
	switch op {
	case "SetPackagePrice":
		var body facts.PriceInput
		if err := facts.Decode(in.Body, &body); err != nil {
			return nil, validationError(err, "price_input_invalid")
		}
		var err error
		if price, err = facts.NormalizePrice(body, actor, now); err != nil {
			return nil, validationError(err, "price_invalid")
		}
	case "SetPackageMacosSupport":
		var body facts.MacosInput
		if err := facts.Decode(in.Body, &body); err != nil {
			return nil, validationError(err, "macos_input_invalid")
		}
		var err error
		if macos, err = facts.NormalizeMacos(body, actor, now); err != nil {
			return nil, validationError(err, "macos_support_invalid")
		}
	case "SetPackageAlternatives":
		var body struct {
			Items []facts.Alternative `json:"items"`
		}
		if err := facts.Decode(in.Body, &body); err != nil {
			return nil, validationError(err, "alternative_items_invalid:items")
		}
		if body.Items == nil {
			return nil, domain.Rejected("alternative_items_invalid:items")
		}
		var err error
		if alternatives, err = facts.NormalizeAlternatives(in.ID, body.Items); err != nil {
			return nil, validationError(err, "alternatives_invalid")
		}
	}
	return s.write(ctx, in, op, "package", func(st *store.Store) (any, error) {
		if _, found, err := st.PackageFacts(ctx, in.ID); err != nil || !found {
			if err == nil {
				err = fail(domain.CodeNotFound)
			}
			return nil, err
		}
		var err error
		switch op {
		case "SetPackagePrice":
			var value any
			if price != nil {
				value = price
			}
			err = st.SavePackageFact(ctx, in.ID, "price", value, in.Actor.ID)
		case "SetPackageMacosSupport":
			var value any
			if macos != nil {
				value = macos
			}
			err = st.SavePackageFact(ctx, in.ID, "macos", value, in.Actor.ID)
		case "SetPackageAlternatives":
			ids := make([]int64, 0, len(alternatives))
			for _, item := range alternatives {
				ids = append(ids, item.PackageID)
			}
			targets, lookupErr := st.AlternativeTargets(ctx, ids)
			if lookupErr != nil {
				return nil, lookupErr
			}
			// Alternatives are apps still in the catalog; hidden or disabled ones stay listed but
			// pages skip them and the gaps report says so.
			if slices.ContainsFunc(ids, func(id int64) bool { return !targets[id] }) {
				return nil, domain.Rejected("alternative_target_unavailable:packageId")
			}
			source := "admin"
			if actor.Agent {
				source = "agent"
			}
			err = st.ReplaceAlternatives(ctx, in.ID, alternatives, source)
		}
		if err != nil {
			return nil, err
		}
		if err := st.AppendContentChange(ctx, in.ID); err != nil {
			return nil, err
		}
		return factsView(ctx, st, in.ID, now)
	})
}
