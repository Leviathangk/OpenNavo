package store

import "context"

// Missing, malformed or false values keep X7 closed. This read is outside the detail cache.
func (s *Store) AppIntegrationsEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.DB.WithContext(ctx).Raw(`SELECT value='true'::jsonb FROM app_config WHERE key='web.appIntegrationsEnabled'`).Scan(&enabled).Error
	return enabled, err
}
