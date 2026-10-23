package domain

import "context"

type catalogLocalesKey struct{}

// WithAllCatalogLocales marks catalog changes and links requests from a client that reads every desktop language
// (it asks with locales=all). Older clients fail on unknown language keys and keep the original six.
func WithAllCatalogLocales(ctx context.Context, all bool) context.Context {
	return context.WithValue(ctx, catalogLocalesKey{}, all)
}
func AllCatalogLocales(ctx context.Context) bool {
	all, _ := ctx.Value(catalogLocalesKey{}).(bool)
	return all
}
