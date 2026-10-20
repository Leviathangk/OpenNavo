package store

import (
	"context"
	"encoding/json"
	"github.com/opennavo/opennavo/server/internal/articles"
	"time"
)

type ArticleTestArtifact struct {
	ID           int64           `json:"id"`
	ArticleID    int64           `json:"articleId"`
	PackageID    int64           `json:"packageId"`
	Payload      json.RawMessage `json:"payload"`
	SHA256       string          `json:"sha256"`
	RegisteredBy int64           `json:"registeredBy"`
	CreatedAt    time.Time       `json:"createdAt"`
}

func (s *Store) ArticleTestArtifacts(ctx context.Context, id int64) ([]ArticleTestArtifact, error) {
	out := []ArticleTestArtifact{}
	err := s.DB.WithContext(ctx).Table("article_test_artifacts").Where("article_id=?", id).Order("id DESC").Limit(100).Find(&out).Error
	return out, err
}
func (s *Store) ArticleTestArtifact(ctx context.Context, id int64) (ArticleTestArtifact, error) {
	var out ArticleTestArtifact
	err := s.DB.WithContext(ctx).Table("article_test_artifacts").Where("id=?", id).Find(&out).Error
	return out, err
}
func (s *Store) InsertArticleTestArtifact(ctx context.Context, articleID, actorID int64, in articles.TestArtifactInput) (ArticleTestArtifact, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return ArticleTestArtifact{}, err
	}
	var id int64
	err = s.DB.WithContext(ctx).Raw(`INSERT INTO article_test_artifacts(article_id,package_id,payload,sha256,registered_by) VALUES(?,?,?::jsonb,?,?) ON CONFLICT(article_id,sha256) DO UPDATE SET sha256=EXCLUDED.sha256 RETURNING id`, articleID, in.PackageID, string(raw), in.Hash(), actorID).Scan(&id).Error
	if err != nil {
		return ArticleTestArtifact{}, err
	}
	return s.ArticleTestArtifact(ctx, id)
}
