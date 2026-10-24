package cache

import "time"

// PackageDetailPrefix versions the public detail payload and its invalidation patterns.
const PackageDetailPrefix = "c:pkg:c5:"

// PackageDetailTTL also bounds how long a successful provenance refresh may be cached.
const PackageDetailTTL = 10 * time.Minute

// ReleaseTimelinePrefix invalidates payloads that allowed foreign editorial notes (c3) and versions-page
// decisions made under the source-URL notes rule (c4), so a rollout never serves the older decision.
const ReleaseTimelinePrefix = "c:rel:c4:"
