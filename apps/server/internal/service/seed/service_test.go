package seed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/seeds"
)

type repository struct {
	exists bool
	hash   string
	data   domain.SeedData
}

func (r *repository) AdminExists(context.Context, string) (bool, error) { return r.exists, nil }
func (r *repository) Seed(_ context.Context, data domain.SeedData, _ string, hash string) error {
	r.data = data
	r.hash = hash
	return nil
}
func TestSeedDefinitionsAndBootstrap(t *testing.T) {
	data, err := seeds.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Categories) != 22 || len(data.Roles) != 6 || len(data.Permissions) != 37 || len(data.Mirrors) != 4 || len(data.Configs) != 6 {
		t.Fatal("seed definitions incomplete")
	}
	if !data.Categories[20].Hidden || !data.Categories[21].Hidden {
		t.Fatal("fonts and libraries must be hidden by default")
	}
	cfg := config.Config{AdminBootstrapUsername: "local-admin", AdminBootstrapPassword: "local-development-only"}
	repo := &repository{}
	if err := Run(context.Background(), repo, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(repo.hash, "$argon2id$v=19$m=65536,t=3,p=2$") || strings.Contains(repo.hash, cfg.AdminBootstrapPassword) {
		t.Fatal("bootstrap password was not hashed correctly")
	}
	repo.exists = true
	cfg.AdminBootstrapPassword = ""
	if err := Run(context.Background(), repo, cfg); err != nil {
		t.Fatal(err)
	}
	if repo.hash != "" {
		t.Fatal("seed must preserve existing admin password")
	}
	repo.exists = false
	if err := Run(context.Background(), repo, cfg); err == nil {
		t.Fatal("first seed accepted missing password")
	}
}

func TestInitialDownloadURLUsesRuntimeWebBase(t *testing.T) {
	cfg := config.Config{WebBaseURL: "https://web.example.test/store///"}
	repo := &repository{exists: true}
	if err := Run(context.Background(), repo, cfg); err != nil {
		t.Fatal(err)
	}
	for _, entry := range repo.data.Configs {
		if entry.Key == "web.downloadUrl" {
			var actual string
			if err := json.Unmarshal([]byte(entry.Value), &actual); err != nil {
				t.Fatal(err)
			}
			if actual != "https://web.example.test/store/download" {
				t.Fatalf("initial download URL uses wrong origin: %s", actual)
			}
			return
		}
	}
	t.Fatal("download URL seed missing")
}
