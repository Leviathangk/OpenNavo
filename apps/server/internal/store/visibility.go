package store

import "context"

func (s *Store) VisibilityGeneration(ctx context.Context) (int64, error) {
	var generation int64
	err := s.DB.WithContext(ctx).Raw("SELECT generation FROM catalog_visibility WHERE id=true").Scan(&generation).Error
	return generation, err
}
