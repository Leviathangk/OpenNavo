package sourcefacts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
)

type NVDSource interface {
	ExactMatches(context.Context, string) ([]facts.Vulnerability, error)
}

func (s *Service) runNVD(ctx context.Context, deadline time.Time) error {
	if s.NVD == nil {
		return nil
	}
	rows, err := s.Store.SecuritySubjects(ctx)
	if err != nil {
		return err
	}
	count := 0
	for _, row := range rows {
		if count >= 20 || !s.now().Before(deadline) {
			break
		}
		var value facts.SecurityObservation
		var proof facts.Proof
		if json.Unmarshal(row.Value, &value) != nil || json.Unmarshal(row.Evidence, &proof) != nil || value.Validate() != nil || proof.At(s.now()).Status != "verified" || value.ProductCPE == "" || value.AppVersion != row.Version {
			continue
		}
		if row.CheckedAt != nil && s.now().Before(row.CheckedAt.Add(24*time.Hour)) {
			continue
		}
		if count > 0 {
			if err = s.sleep(ctx, 6*time.Second); err != nil {
				return err
			}
		}
		if !s.now().Before(deadline) {
			break
		}
		count++
		requestCtx, cancel := context.WithDeadline(ctx, deadline)
		matches, lookupErr := s.NVD.ExactMatches(requestCtx, value.ProductCPE)
		cancel()
		if lookupErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			break
		}
		// The injectable provider is still checked at the boundary before persistence.
		exact := []facts.Vulnerability{}
		for _, v := range matches {
			if v.ProductCPE == value.ProductCPE && v.AppVersion == row.Version {
				exact = append(exact, v)
			}
		}
		if err = s.Store.SaveCVECheck(ctx, row.PackageID, row.Token, value.ProductCPE, row.Version, exact, s.now()); err != nil {
			return err
		}
	}
	return nil
}
