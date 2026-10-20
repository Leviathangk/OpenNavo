package management

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seoexperiments"
)

func (s *Service) proposeSnippet(ctx context.Context, in Input) (any, error) {
	raw, err := json.Marshal(in.Body)
	if err != nil {
		return nil, domain.Validation()
	}
	var proposal seoexperiments.Proposal
	if json.Unmarshal(raw, &proposal) != nil {
		return nil, domain.Validation()
	}
	experiment, err := (&seoexperiments.Service{DB: s.Store.DB, WebURL: s.Config.WebBaseURL}).Start(ctx, s.Store.DB, proposal)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": experiment.ID, "state": experiment.State, "requiredImpressions": experiment.RequiredImpressions, "variant": experiment.Variant, "templateVersion": experiment.TemplateVersion}, nil
}
