package store

// Only stored responsive objects enter srcset; originals and card crops have
// separate roles. Empty manifests deliberately return [] for legacy assets.
type ImageVariant struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	URL    string `json:"url"`
	Type   string `json:"type"`
}

const assetResponsiveSQL = `COALESCE((SELECT jsonb_agg(jsonb_build_object('width',f->'width','height',f->'height','url',f->'url','type',f->'type') ORDER BY (f->>'width')::int) FROM jsonb_array_elements(a.files) f WHERE f->>'role'='responsive'),'[]'::jsonb)`
const assetThumbnailSQL = `COALESCE((SELECT jsonb_agg(jsonb_build_object('width',f->'width','height',f->'height','url',f->'url','type',f->'type') ORDER BY (f->>'width')::int) FROM jsonb_array_elements(a.files) f WHERE f->>'role'='thumbnail'),'[]'::jsonb)`
