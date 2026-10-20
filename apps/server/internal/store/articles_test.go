package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArticleCachePatternsCoverArticleReadsAndListedApps(t *testing.T) {
	require.Equal(t, []string{"c:art:*"}, ArticleCachePatterns(nil))
	pages := []string{"/articles/pick", "/articles", "/authors/lin-wei", "/apps/shottr", "/apps/python@3.14"}
	require.Equal(t, []string{"c:art:*", "c:pkg:c5:cask:shottr:*", "c:pkg:c5:cask:python@3.14:*"}, ArticleCachePatterns(pages))
}
